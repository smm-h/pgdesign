---
title: "Design Intelligence"
description: "How pgdesign analyzes schema structure for cascade risks, constraint subsumption, dead columns, row size estimation, and natural key candidates."
---

# Design Intelligence

The design intelligence subsystem analyzes schema structure beyond basic validation. It surfaces structural issues, optimization opportunities, and design anti-patterns through 12 diagnostics registered under the `"design"` tag. All diagnostics are implemented in `internal/validate/validate.go` and registered via `cmd/pgdesign/checks.go`.

Running `pgdesign check --tag design` executes the full validation suite and filters results to: W013, W014, W015, I001, W016, W017, W018, W019, I002, I003, W021, I004.

## FK Graph Infrastructure

The FK graph is built once during `Schema.Build()` and `BuildMulti()` as the final step of schema resolution and stored persistently on the `Schema` struct as `FKGraph *FKGraph`, which is excluded from JSON serialization. Located in `internal/model/fkgraph.go`, the graph provides forward and reverse adjacency maps plus fan-in and fan-out counts. Multiple consumers access the graph for cascade analysis, codegen relationship generation, GraphQL field derivation, and workload N+1 detection.

### FKEdge

Represents a single FK relationship between two columns in the schema. Multi-column foreign keys produce one FKEdge per column pair, so a two-column FK like `(order_id, product_id) REFERENCES orders(id, product_id)` generates two edges. Each edge records the source table and column, target table and column, the ON DELETE action, and the constraint name. This per-column granularity enables precise cascade chain analysis and column-level relationship tracking.

| Field | Type | Description |
|-------|------|-------------|
| `FromTable` | `string` | Table that owns the FK |
| `FromColumn` | `string` | Column in the owning table |
| `ToTable` | `string` | Referenced table |
| `ToColumn` | `string` | Referenced column |
| `OnDelete` | `string` | ON DELETE action (CASCADE, RESTRICT, etc.) |
| `FKName` | `string` | Constraint name |

### FKGraph

| Field | Type | Description |
|-------|------|-------------|
| `Forward` | `map[string][]FKEdge` | table -> tables it references (outgoing) |
| `Reverse` | `map[string][]FKEdge` | table -> tables that reference it (incoming) |
| `FanIn` | `map[string]int` | table -> count of incoming FK constraints |
| `FanOut` | `map[string]int` | table -> count of outgoing FK constraints |

`FanIn` and `FanOut` count FK constraints (not individual columns in multi-column FKs). They are incremented once per FK, outside the column loop.

### BuildFKGraph()

Iterates all tables, then each table's FKs, then each FK's column pairs. Creates one `FKEdge` per column pair, populating both `Forward` and `Reverse` maps simultaneously. Multi-column FK column mapping is bounded by `len(fk.RefColumns)` for safety.

### Consumers

| Consumer | Edges Used | Purpose |
|----------|-----------|---------|
| validate W013/W014 | Forward (via CascadeDepth/CascadeBreadth) | Cascade analysis |
| validate W015 | Reverse | Mixed ON DELETE detection |
| codegen (gorm, jpa, drizzle, sqlalchemy) | Reverse | Has-many relationship generation |
| generate (graphql) | Reverse | List-type field generation |
| workload (N+1 detection) | Forward | Parent-child query ratio analysis |

All codegen consumers guard with `if schema.FKGraph == nil { schema.BuildFKGraph() }` to handle schemas constructed without `Build()` (e.g., in tests). Multi-column FKs are filtered out by counting edges per `FKName` and skipping any where count != 1.

## Cascade Analysis

BFS and DFS walkers over the FK graph that follow only CASCADE edges to assess the potential impact of DELETE operations. Located in `internal/model/fkgraph.go`, these walkers measure two dimensions of cascade risk: depth measures the longest chain of cascading deletes from a single table, while breadth measures the total number of distinct tables affected by a cascade. Both metrics are needed because a shallow but wide cascade affects many tables simultaneously, while a narrow but deep cascade creates long dependency chains.

### CascadeDepth (used by W013)

DFS with backtracking. Follows edges in the `Reverse` graph where `OnDelete` equals `"CASCADE"` (case-insensitive via `strings.EqualFold`). The visited map is reset after exploring each node, allowing the same table to be reached via different paths. This finds the **longest** cascade chain, not just any path. Returns the maximum depth from a given table.

### CascadeBreadth (used by W014)

Delegates to `CascadeChain()` and returns the length of the resulting list, which counts the number of distinct tables reachable from the starting table via CASCADE edges, excluding the starting table itself. This metric represents the blast radius of a DELETE with CASCADE on the given table. A breadth of 5 means that deleting a row from this table could trigger cascading deletes in up to 5 other tables, potentially affecting many rows across the schema.

### CascadeChain (used by W015)

BFS-ordered list of all tables reachable via CASCADE edges from a starting table, where tables appear in order of their distance from the start. The first elements are tables directly referenced by the starting table with ON DELETE CASCADE, followed by tables one hop further, and so on. Returns `nil` if no CASCADE edges exist from the starting table. This ordered list is used by W015 to detect mixed ON DELETE actions in cascade chains, where some edges use CASCADE and others use RESTRICT or SET NULL.

### Why Both BFS and DFS

Depth (DFS) measures worst-case chain length -- how deep a cascade goes. Breadth (BFS via CascadeChain) measures blast radius -- how many tables are affected. A shallow but wide cascade is different from a narrow but deep one. Both metrics are needed for complete cascade risk assessment.

| Method | Algorithm | Cycle Handling | Computes |
|--------|-----------|----------------|----------|
| CascadeDepth | DFS + backtracking | Visited set prevents revisiting in current path; reset after backtrack allows different paths to same node | Longest CASCADE chain depth |
| CascadeChain | BFS | Standard BFS visited dedup | Ordered list of reachable tables |
| CascadeBreadth | BFS (via CascadeChain) | Same as CascadeChain | Count of reachable tables |

### Configurable Thresholds

Cascade analysis thresholds are defined in `validate.Config` and control when diagnostics are emitted for cascade depth and breadth. The thresholds use intentionally asymmetric comparison operators: depth uses strict greater-than because 3 levels of cascade is considered acceptable, while breadth uses greater-than-or-equal because affecting 5 tables already warrants review. Both thresholds are configurable through the validation configuration for projects with different risk tolerance levels.

| Field | Default | Diagnostic | Comparison |
|-------|---------|-----------|------------|
| `CascadeMaxDepth` | 3 | W013 | strict `>` (depth of 3 does NOT warn) |
| `CascadeMaxBreadth` | 5 | W014 | `>=` (breadth of 5 DOES warn) |

The threshold asymmetry is intentional: depth 3 is considered acceptable (three levels of cascade), but breadth 5 already warrants review (five tables in the blast radius).

W015 (mixed ON DELETE) reads `Reverse[table]`, deduplicates edges by `FKName`, collects distinct ON DELETE actions, and fires when 2+ distinct actions target the same table.

## Constraint Subsumption

Constraint subsumption detection operates across three tiers of increasing complexity, all implemented in `internal/validate/validate.go`. Each tier handles a specific class of redundant constraints: structural rules detect obvious cases like a PK that makes a UNIQUE constraint redundant, expression normalization catches domain CHECK constraints duplicated at the column level, and range containment identifies numeric range constraints where one is strictly contained within another. Each tier is decidable for its target patterns.

### Tier 1: Structural Rules (always decidable)

**W016 -- PK Subsumes UNIQUE** (`checkPKSubsumesUnique`). Builds a set of PK column names. For each UNIQUE constraint, checks if all its columns are in the PK set. Pure set containment -- no expression parsing needed.

**W017 -- Redundant NULL Check** (`checkRedundantNullCheck`). Builds a set of NOT NULL columns. For each CHECK constraint, calls `extractIsNotNullColumn` to detect the pattern `col IS NOT NULL`. If the column is already NOT NULL, the CHECK is redundant. Detection uses AST-based parsing (unwraps `ParenExpr`, checks for `UnaryOp` with op `"IS NOT NULL"` and a single `ColumnRef`), with regex fallback: `(?i)^\s*\(?\s*(\w+)\s+IS\s+NOT\s+NULL\s*\)?\s*$`.

### Tier 2: Expression Normalization (decidable for common patterns)

**W018 -- Domain CHECK Duplicates Column CHECK** (`checkDomainCheckDuplicate`) detects cases where a column has its own CHECK constraint that is logically equivalent to the CHECK constraint inherited from its domain type. When a column uses a domain type that has a CHECK constraint, and the column also declares its own CHECK, the column-level CHECK may be redundant because the domain already enforces the same rule at the type level. The detection proceeds through five steps:

1. Build a map of domains by name.
2. Extract the single referenced column from the column CHECK via `extractCheckColumn` (walks AST, returns the column only if exactly one distinct column is referenced).
3. Look up the column's semantic type to find the corresponding domain.
4. Substitute `VALUE` in the domain's CHECK with the column name (regex `(?i)\bVALUE\b`).
5. Compare AST-normalized forms via `normalizeExpr`, which parses to AST then produces a canonical string: commutative `AND`/`OR` operands sorted alphabetically, operators/functions uppercased, redundant parens stripped. Falls back to lowercased whitespace-collapsed string on parse failure.

If the canonical forms match, the column CHECK is redundant.

### Tier 3: Range Containment (decidable for numeric ranges)

**W019 -- Range Subsumption** (`checkRangeSubsumption`) detects when one numeric range CHECK constraint is strictly contained within another on the same column, making the wider constraint redundant. For example, if a column has both `CHECK (age >= 0 AND age <= 150)` and `CHECK (age >= 18 AND age <= 65)`, the first constraint is subsumed by normal business logic but the second is the effective constraint. For tables with two or more CHECK constraints, the algorithm proceeds as follows:

1. Extract `rangeInfo` from each CHECK via `extractRangeFromAST`: handles single comparisons and `AND` combinations, flips operator when column is on the right side. `mergeRanges` combines two single-bound ranges.
2. For each pair of ranges on the same column, check asymmetric subsumption: if `ri.subsumes(rj) && !rj.subsumes(ri)`, the narrower constraint is redundant.

`rangeInfo` fields: `Column`, `Low`/`High` (`*float64`, nil = unbounded), `LowIncl`/`HighIncl` (bool). The `subsumes` method checks column match, then `lowBoundCovers` and `highBoundCovers`.

### Decidability Summary

| Check | Decidable? | Method |
|-------|-----------|--------|
| PK subsumes UNIQUE (W016) | Always | Set containment |
| Redundant NOT NULL (W017) | Always | Column metadata + pattern match |
| Domain vs column CHECK (W018) | Common patterns | AST comparison after normalization |
| Range containment (W019) | Numeric | Bound extraction and comparison |
| Arbitrary expression equivalence | Undecidable | Not attempted |

## Dead Column Detection (I002)

`checkDeadColumn` performs schema-only analysis to identify columns that are not referenced by any index, constraint, foreign key, view, function, or RLS policy in the schema. PostgreSQL does not expose per-column access statistics in `pg_catalog` -- `pg_stat_user_tables` provides only table-level stats and `pg_stats` has value distribution but not access frequency. This means the detection is a necessary-but-not-sufficient condition: columns accessed only by application queries appear dead to schema-only analysis.

### Reference Scanning

For each table, the dead column detector builds a comprehensive `referenced` set by scanning every schema object that could reference a column. The scan covers primary keys, foreign key columns, unique constraints, all index columns and their INCLUDE lists, index WHERE clause expressions, exclusion constraints, partition keys, CHECK constraint expressions, generated column expressions, RLS policy expressions, trigger WHEN clauses, and incoming FK references from other tables. Views, materialized views, and functions use a conservative heuristic.

- PK columns
- FK source columns
- UNIQUE constraint columns
- Index columns and INCLUDE columns
- Index WHERE clauses (parsed via `extractColumnRefs`)
- Exclusion constraint columns and WHERE clauses
- Partition columns
- CHECK constraint expressions (parsed)
- Generated column expressions (parsed)
- RLS policy USING and WITH CHECK expressions (parsed)
- Trigger WHEN clauses (parsed)
- FK references from other tables (`RefColumns` on FKs pointing to this table)
- Views: if a view's query mentions the table name, all columns are marked referenced
- Materialized views: same heuristic as views
- Functions: if any function depends on this table, all columns are marked referenced

Any column not in the `referenced` set emits I002.

### Limitations

The schema-only approach is a necessary-but-not-sufficient condition for identifying truly unused columns. A column accessed exclusively by application SQL queries, ORM-generated queries, or ad-hoc reporting appears dead to this analysis because those access patterns are not captured in the schema definition. For this reason, the diagnostic is informational (I-level) rather than a warning, serving as a starting point for manual investigation rather than an actionable recommendation to drop the column.

View and function detection uses a conservative heuristic: if the object references the table at all, all columns are marked referenced. This avoids false positives at the cost of reduced sensitivity.

## Row Size Estimation (I003, W021, I004)

Row size estimation calculates the approximate on-disk tuple size for each table based on its column types, alignment requirements, and nullable columns. Located in `internal/validate/validate.go`, this analysis detects tables whose rows exceed the PostgreSQL TOAST threshold (2048 bytes, reducing cache efficiency) or the page size (8192 bytes, requiring TOAST storage for every row). It also identifies potential savings from column reordering to minimize alignment padding waste.

### PostgreSQL Tuple Layout

| Component | Size | Notes |
|-----------|------|-------|
| HeapTupleHeaderData | 23 bytes, MAXALIGN'd to **24** | Fixed header per tuple |
| Null bitmap | ceil(ncols/8) bytes, MAXALIGN'd | Only present if any column is nullable |
| ItemIdData | **4 bytes** per tuple | Line pointer in page header (not part of tuple, but per-tuple overhead) |

### Type Alignment

| Code | Alignment | Types |
|------|-----------|-------|
| `d` (double) | 8 bytes | int8, float8, timestamp, timestamptz, interval, numeric |
| `i` (int) | 4 bytes | int4, float4, date, oid, varlena types (text, varchar, jsonb) |
| `s` (short) | 2 bytes | int2, smallint |
| `c` (char) | 1 byte | bool, char, uuid |

The `pgTypeWidths` table maps approximately 50 PostgreSQL type names to `{Len, Align}` pairs. Variable-length types use `Len = -1`.

### Varlena Size Estimates

When actual data is unavailable because the analysis runs from the schema definition alone, estimated average sizes are used for variable-length types. These estimates are intentionally conservative, using half the declared maximum for varchar and the full declared length for char, plus the 4-byte varlena header that PostgreSQL prepends to all variable-length values. JSONB and bytea use fixed estimates based on typical production data sizes rather than worst-case maximums.

| Type | Estimate |
|------|----------|
| `varchar(N)` / `character varying(N)` | N/2 + 4 |
| `char(N)` / `character(N)` | N + 4 |
| `jsonb` / `json` | 64 bytes |
| `bytea` | 32 bytes |
| `numeric` / `decimal` | 16 bytes |
| Default (text, etc.) | 32 bytes |

The 4-byte addition accounts for the varlena header.

### Alignment Algorithm

`estimateRowSize` processes columns in their declaration order within the TOML schema, simulating how PostgreSQL lays out tuple data on disk. The algorithm follows PostgreSQL's tuple layout rules: starting after the fixed 24-byte header, adding the null bitmap if any column is nullable, then iterating columns with alignment padding between them, and finally applying MAXALIGN to the total. The function returns both the total estimated row size and the total padding bytes wasted on alignment.

1. Start at offset 24 (HeapTupleHeaderData after MAXALIGN).
2. If any column is nullable, add null bitmap: `ceil(ncols/8)` bytes, MAXALIGN to 8-byte boundary.
3. For each column: look up type info, pad to required alignment boundary, add column size.
4. MAXALIGN final offset to 8-byte boundary.
5. Add ItemIdData (4 bytes).
6. Return total size and total padding.

### Column Reordering Optimization (I004)

`estimateRowSizeOptimal` sorts columns by alignment descending (`d` > `i` > `s` > `c`), then by size descending within the same alignment class. Runs the same `estimateRowSize` on the sorted copy to compute the tightest possible packing.

### Diagnostic Thresholds

| Diagnostic | Threshold | Severity | Condition |
|-----------|-----------|----------|-----------|
| W021 | 8192 bytes (page size) | Warning | `currentSize > 8192` |
| I003 | 2048 bytes (TOAST threshold) | Info | `currentSize > 2048` (and <= 8192) |
| I004 | 16 bytes savings | Info | `currentSize - optimalSize > 16` |

W021 indicates rows that require TOAST storage and suggests table splitting. I003 indicates rows that cross the TOAST threshold, reducing cache efficiency. I004 reports potential savings from column reordering -- an informational hint since PostgreSQL does not support column reordering without table rewrite.

## Natural Key Surfacing (I001)

`checkNaturalKey` detects candidate keys derived from declared functional dependencies that differ from the table's primary key, surfacing potential natural key alternatives that the schema designer may want to consider. The detection is driven by mathematical analysis of functional dependencies rather than naming conventions, ensuring that only true candidate keys (column sets that functionally determine all other columns) are reported. Candidate keys containing surrogate columns like `id` and `auto_id` are filtered out.

### Algorithm

1. Skip tables with no declared functional dependencies (`t.Dependencies`).
2. Call `t.CandidateKeys()` to compute all candidate keys from the functional dependencies.
3. For each candidate key:
   - Skip if it matches the PK (`sameColumns`: order-independent set equality via sorted comparison).
   - Skip if it contains a surrogate column (`containsSurrogateCol`).
4. Remaining candidate keys emit I001.

### Surrogate Column Detection

A column is considered surrogate if its `SemanticTypeName` is one of: `"id"`, `"auto_id"`, `"ref"`. These are pgdesign's built-in semantic types for surrogate identifiers and foreign key references. Candidate keys containing surrogate columns are not surfaced because they are not meaningful natural keys.

### Design Rationale

The check is driven by functional dependencies, not name patterns. This avoids false positives from column naming conventions and ensures candidates are mathematically valid keys (they functionally determine all other columns in the table). The check is informational -- it surfaces candidates for the developer to evaluate whether a surrogate PK is truly needed.

## Check Registration

All design intelligence diagnostics are registered in `cmd/pgdesign/checks.go`. The `designCodes` map defines the 12 codes. The `checkDesign` handler runs the full `validate.Validate(schema, config)` suite and filters results to only diagnostics whose codes appear in the map.

Registration in `cmd/pgdesign/cli.go`:

```go
app.RegisterCheck("design", checkDesign)
```

Other check tags registered alongside it: `"validation"`, `"nf"`, `"coverage"`, `"structural"`, `"workload"`.
