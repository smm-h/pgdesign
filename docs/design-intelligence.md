---
title: "Design Intelligence"
description: "How pgdesign analyzes schema structure for cascade risks, constraint subsumption, dead columns, row size estimation, and natural key candidates."
---

# Design Intelligence

The design intelligence subsystem analyzes schema structure beyond basic validation. It surfaces structural issues, optimization opportunities, and design anti-patterns through 12 diagnostics registered under the `"design"` tag. All diagnostics are implemented in `internal/validate/validate.go` and registered via `cmd/pgdesign/checks.go`.

Running `pgdesign check --tag design` executes the full validation suite and filters results to: W013, W014, W015, I001, W016, W017, W018, W019, I002, I003, W021, I004.

## FK Graph Infrastructure

The FK graph is built once during `Schema.Build()` (and `BuildMulti()`) as the final step and stored on the `Schema` struct (`FKGraph *FKGraph`, excluded from JSON). Located in `internal/model/fkgraph.go`.

### FKEdge

Represents a single FK relationship between two columns. Multi-column FKs produce one edge per column pair.

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

BFS and DFS walkers over the FK graph, following only CASCADE edges. Located in `internal/model/fkgraph.go`.

### CascadeDepth (used by W013)

DFS with backtracking. Follows edges in the `Reverse` graph where `OnDelete` equals `"CASCADE"` (case-insensitive via `strings.EqualFold`). The visited map is reset after exploring each node, allowing the same table to be reached via different paths. This finds the **longest** cascade chain, not just any path. Returns the maximum depth from a given table.

### CascadeBreadth (used by W014)

Delegates to `CascadeChain()` and returns its length. Counts distinct reachable tables (excluding the starting table).

### CascadeChain (used by W015)

BFS-ordered list of all reachable tables via CASCADE edges. Tables appear in order of distance from the starting table. Returns `nil` if no CASCADE edges exist.

### Why Both BFS and DFS

Depth (DFS) measures worst-case chain length -- how deep a cascade goes. Breadth (BFS via CascadeChain) measures blast radius -- how many tables are affected. A shallow but wide cascade is different from a narrow but deep one. Both metrics are needed for complete cascade risk assessment.

| Method | Algorithm | Cycle Handling | Computes |
|--------|-----------|----------------|----------|
| CascadeDepth | DFS + backtracking | Visited set prevents revisiting in current path; reset after backtrack allows different paths to same node | Longest CASCADE chain depth |
| CascadeChain | BFS | Standard BFS visited dedup | Ordered list of reachable tables |
| CascadeBreadth | BFS (via CascadeChain) | Same as CascadeChain | Count of reachable tables |

### Configurable Thresholds

Defined in `validate.Config`:

| Field | Default | Diagnostic | Comparison |
|-------|---------|-----------|------------|
| `CascadeMaxDepth` | 3 | W013 | strict `>` (depth of 3 does NOT warn) |
| `CascadeMaxBreadth` | 5 | W014 | `>=` (breadth of 5 DOES warn) |

The threshold asymmetry is intentional: depth 3 is considered acceptable (three levels of cascade), but breadth 5 already warrants review (five tables in the blast radius).

W015 (mixed ON DELETE) reads `Reverse[table]`, deduplicates edges by `FKName`, collects distinct ON DELETE actions, and fires when 2+ distinct actions target the same table.

## Constraint Subsumption

Three tiers of subsumption detection, all in `internal/validate/validate.go`.

### Tier 1: Structural Rules (always decidable)

**W016 -- PK Subsumes UNIQUE** (`checkPKSubsumesUnique`). Builds a set of PK column names. For each UNIQUE constraint, checks if all its columns are in the PK set. Pure set containment -- no expression parsing needed.

**W017 -- Redundant NULL Check** (`checkRedundantNullCheck`). Builds a set of NOT NULL columns. For each CHECK constraint, calls `extractIsNotNullColumn` to detect the pattern `col IS NOT NULL`. If the column is already NOT NULL, the CHECK is redundant. Detection uses AST-based parsing (unwraps `ParenExpr`, checks for `UnaryOp` with op `"IS NOT NULL"` and a single `ColumnRef`), with regex fallback: `(?i)^\s*\(?\s*(\w+)\s+IS\s+NOT\s+NULL\s*\)?\s*$`.

### Tier 2: Expression Normalization (decidable for common patterns)

**W018 -- Domain CHECK Duplicates Column CHECK** (`checkDomainCheckDuplicate`). When a column uses a domain type that has a CHECK constraint, and the column also has its own CHECK:

1. Build a map of domains by name.
2. Extract the single referenced column from the column CHECK via `extractCheckColumn` (walks AST, returns the column only if exactly one distinct column is referenced).
3. Look up the column's semantic type to find the corresponding domain.
4. Substitute `VALUE` in the domain's CHECK with the column name (regex `(?i)\bVALUE\b`).
5. Compare AST-normalized forms via `normalizeExpr`, which parses to AST then produces a canonical string: commutative `AND`/`OR` operands sorted alphabetically, operators/functions uppercased, redundant parens stripped. Falls back to lowercased whitespace-collapsed string on parse failure.

If the canonical forms match, the column CHECK is redundant.

### Tier 3: Range Containment (decidable for numeric ranges)

**W019 -- Range Subsumption** (`checkRangeSubsumption`). For tables with 2+ CHECK constraints:

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

`checkDeadColumn` performs schema-only analysis. PostgreSQL does not expose per-column access statistics in `pg_catalog` (`pg_stat_user_tables` has table-level stats only; `pg_stats` has value distribution but not access frequency).

### Reference Scanning

For each table, builds a `referenced` set by scanning:

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

This is a necessary-but-not-sufficient condition. A column accessed exclusively by application queries appears "dead" to schema-only analysis. The diagnostic is informational (I-level), not a warning.

View and function detection uses a conservative heuristic: if the object references the table at all, all columns are marked referenced. This avoids false positives at the cost of reduced sensitivity.

## Row Size Estimation (I003, W021, I004)

Estimates on-disk tuple size to detect tables with potential performance issues. Located in `internal/validate/validate.go`.

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

When actual data is unavailable, estimated averages are used:

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

`estimateRowSize` processes columns in declaration order:

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

`checkNaturalKey` detects candidate keys derived from declared functional dependencies that differ from the primary key.

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
