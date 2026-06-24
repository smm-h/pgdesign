---
title: "Workload Analysis"
description: "How pgdesign analyzes schemas and live query patterns to recommend indexes, detect N+1 queries, and flag performance anti-patterns in PostgreSQL databases."
---

# Workload Analysis

pgdesign provides a two-tier approach to index recommendations and performance diagnostics. The **structural tier** analyzes the TOML schema without a database connection. The **workload tier** analyzes live query patterns from `pg_stat_statements` and `pg_stat_user_tables`. Both tiers produce diagnostics through the standard `diagnostic.Diagnostics` system.

## Architecture: Structural vs Workload Tiers

The split into two tiers exists because schema analysis and query analysis answer fundamentally different questions and have different requirements. Structural analysis is deterministic and always available because it works from the TOML schema definition alone, while workload analysis requires a running PostgreSQL database with the pg_stat_statements extension enabled. By separating these concerns, teams can run structural checks in CI without database access and add workload checks when a live database is available.

- **Structural analysis** answers: "Given these column types and index definitions, are there obvious gaps?" This is deterministic and always available.
- **Workload analysis** answers: "Given actual query patterns, where are the real bottlenecks?" This requires a running database with `pg_stat_statements` enabled.

This mirrors the approach of tools like pganalyze (which separates schema analysis from query analysis) but is simpler. pgdesign does not maintain a persistent query log or index advisor state.

### Comparison with Industry Tools

| Tool | Approach | Data Source |
|------|----------|-------------|
| Dexter | Hypothetical index testing via `hypopg` | pg_stat_statements + explain |
| pg_qualstats | Extension that logs WHERE clause predicates | Live query sampling |
| pganalyze | SaaS with persistent history | pg_stat_statements + logs |
| pgdesign | Schema structural + live stats | Schema TOML + pg_stat_statements |

pgdesign does not use hypothetical indexes (`hypopg`) because that requires a running database and the ability to create hypothetical indexes. The structural tier works purely from schema definitions.

## Check Registration

Workload checks are registered as two named checks in strictcli's check framework, allowing them to be invoked individually or as part of a full check run. The structural check runs without any database connection and analyzes the resolved schema model for index coverage gaps, while the workload check connects to a live PostgreSQL database to analyze actual query patterns and table access statistics. Both checks produce diagnostics through the standard diagnostic system.

- **`structural`** -- runs all schema-only recommendations. No database connection needed. Invoked via `pgdesign check --name structural`.
- **`workload`** -- runs live analysis against a database. Requires a database URL (from `database.url` in `pgdesign.toml` or `PGDESIGN_DB` environment variable). If no URL is available, the check returns "skip". Invoked via `pgdesign check --name workload`.

Both checks also run as part of `pgdesign check --all`.

## Structural Recommendations (Schema-Only)

All structural checks operate on the resolved schema model without requiring a database connection. They iterate over every table and column in the schema, checking column types against the declared index coverage to identify common patterns where a missing index would cause performance problems. The checks cover JSONB columns, array columns, tsvector columns, append-only tables, boolean columns, and excessive index counts, producing warnings or informational diagnostics as appropriate.

### W022: JSONB Column Without GIN Index

JSONB columns used for querying benefit significantly from GIN indexes because the containment (`@>`), existence (`?`), and any/all existence (`?|`, `?&`) operators require full column scans without an index. This check fires when a JSONB column has no GIN index covering it, suggesting that queries using these operators will perform sequential scans. The check explicitly excludes JSONB array columns (`jsonb[]`) because those are handled by the W023 array check instead.

The check explicitly excludes `jsonb[]` (array of JSONB) -- those trigger W023 instead.

**Condition:** `col.PGType == "jsonb" && !col.Array && !columnHasIndexMethod(table, col.Name, "gin")`

### W023: Array Column Without GIN Index

Array columns benefit from GIN indexes for the containment (`@>`, `<@`) and overlap (`&&`) operators, which are the primary array query operators in PostgreSQL. Without a GIN index, these operators require scanning every row and comparing array contents element by element. This check fires when any array-typed column, including JSONB arrays, text arrays, integer arrays, and all other array types, has no GIN index covering it.

**Condition:** `col.Array && !columnHasIndexMethod(table, col.Name, "gin")`

### W024: tsvector Column Without GIN Index

Full-text search columns with the `tsvector` type require a GIN index for the `@@` text search match operator to be efficient. Without a GIN index, every full-text search query must scan every row in the table and compare the search query against each tsvector value, which becomes prohibitively slow as the table grows. This is one of the most impactful missing index patterns because full-text search is almost always used in user-facing query features where performance is critical.

**Condition:** `col.PGType == "tsvector" && !columnHasIndexMethod(table, col.Name, "gin")`

### I005: Append-Only Table Without BRIN Index

Block Range Index (BRIN) is effective when data has high physical correlation -- column values are naturally ordered on disk. This is common for timestamp columns on append-only tables, where rows are inserted in time order.

The `append_only = true` attribute is a strong signal because it guarantees rows are never updated or deleted (via BEFORE UPDATE OR DELETE triggers), preserving physical correlation. BRIN indexes are 100-1000x smaller than btree for the same column.

Fires when an append-only table has a timestamp column (`timestamptz` or `timestamp`) with no BRIN index.

**Condition:** `table.AppendOnly && isTimestamp(col.PGType) && !columnHasIndexMethod(table, col.Name, "brin")`

### I006: Boolean Column With Dedicated Index

Boolean columns have at most 3 distinct values (true, false, NULL). A btree index on a boolean column has very low selectivity -- an index scan must still read roughly 50% of the table. A partial index (`WHERE active = true`) is usually more effective.

Fires only for **single-column** indexes on boolean columns. Multi-column indexes that include a boolean column are not flagged.

### I007: Excessive Indexes

Tables with 10 or more indexes may have excessive write overhead because every INSERT, UPDATE, and DELETE operation must maintain all indexes on the table. Each additional index adds write amplification and increases the amount of WAL generated per write operation. This check is informational rather than a warning because some tables with complex query patterns legitimately need many indexes, but it serves as a prompt to review whether all indexes are actually used by production queries.

**Threshold:** `len(table.Indexes) >= 10` (exactly 9 indexes does not trigger).

### Duplicate Index Detection

Detects indexes that share a leading-column prefix with another index on the same table. The shorter index is usually redundant because PostgreSQL can use a multi-column index to satisfy queries on its leading columns.

This is implemented in `FindDuplicateIndexes`, which operates on `[]IndexInfo` structs. Key behaviors:

- Groups indexes by (schema, table) to prevent cross-table comparisons.
- Only flags **strict** leading-column prefixes: if index A's columns are a prefix of index B's columns and A is shorter, A is reported as a duplicate of B.
- Same-length column lists are NOT considered duplicates.
- Returns `[]DuplicateIndex` structs rather than diagnostics -- callers format the output.

`FindDuplicateIndexes` is used in three places:

1. The `structural` check (from the schema model).
2. The `pgdesign stats` command (from live database index metadata queried via `pg_index` + `pg_attribute`).
3. The `pgdesign serve` HTTP API (per-table index stats endpoint).

### Index Method Matching

All structural checks use the `columnHasIndexMethod` helper function, which matches index methods case-insensitively via `strings.EqualFold`. This means "GIN", "gin", and "Gin" all satisfy the check for a GIN index on a column. The function searches through all indexes on a table for one that includes the specified column name and uses the specified index method, returning true if any matching index is found regardless of whether additional columns are also indexed.

## Live Analysis (Requires Database)

### pg_stat_statements Column Extraction

`pg_stat_statements` stores normalized queries where literal values are replaced with `$1`, `$2`, etc. Column references are preserved -- `SELECT name FROM users WHERE email = $1` retains `name` and `email`. This makes it possible to extract which columns are actually queried without parsing application code.

The `QueryStatements` function:

1. Verifies that `pg_stat_statements` is installed.
2. Queries the top N statements ordered by `total_exec_time DESC` (N=100 when called from the check handler).
3. Filters out utility statements via regex, excluding: CREATE, ALTER, DROP, GRANT, REVOKE, SET, RESET, BEGIN, COMMIT, ROLLBACK, VACUUM, ANALYZE, COPY, COMMENT, EXPLAIN.
4. Extracts table references from each query using `sqlparse.ExtractTableRefs` (the go-pgquery WASM-based PostgreSQL parser).

Returns `[]StatementStats` with fields:

| Field | Type | Description |
|-------|------|-------------|
| `QueryID` | int64 | pg_stat_statements query identifier |
| `Query` | string | Normalized query text |
| `Calls` | int64 | Total number of executions |
| `TotalExecTime` | float64 | Cumulative execution time in milliseconds |
| `MeanExecTime` | float64 | Average execution time in milliseconds |
| `Rows` | int64 | Total rows returned |
| `Tables` | []string | Tables referenced (extracted via go-pgquery) |

### W025: N+1 Query Pattern Detection

N+1 patterns cannot be detected from schema alone. A parent-child FK relationship tells you that N+1 is possible, but not whether it happens. The application might use JOINs, batch loading, or query result caching.

**Detection method:** Cross-reference `pg_stat_statements` call counts between parent and child table queries against the schema's FK graph.

Algorithm:

1. Build a map from table name to its statement stats.
2. Iterate FK graph edges (forward direction: child references parent).
3. For each FK edge, find statements touching the child table and statements touching the parent table.
4. If `childCalls >= MinSignificantCalls` AND `parentCalls >= MinSignificantCalls` AND `childCalls / parentCalls >= NPlusOneThreshold`, emit W025.
5. Deduplicate per (FromTable, ToTable) pair -- multiple FK edges between the same table pair produce at most one warning.

**Constants:**

| Name | Value | Purpose |
|------|-------|---------|
| `NPlusOneThreshold` | 100 | Minimum child/parent call ratio to flag |
| `MinSignificantCalls` | 100 | Minimum calls on both sides to consider |

### W026: Sequential Scan Heavy

Queries `pg_stat_user_tables` for tables where sequential scans vastly outnumber index scans, indicating that the PostgreSQL query planner is choosing full table scans over indexed lookups. This pattern typically means either the table is missing a needed index, the existing indexes do not cover the columns used in WHERE clauses, or the table is small enough that sequential scans are actually faster. The check uses a 10x threshold to filter out tables where sequential scans are normal.

The `QueryTableScanStats` function reads `pg_stat_user_tables` for the configured schema names (defaulting to `["public"]`), returning `[]TableScanStats` with fields:

| Field | Type | Description |
|-------|------|-------------|
| `Schema` | string | Schema name |
| `Table` | string | Table name |
| `SeqScan` | int64 | Number of sequential scans initiated |
| `IdxScan` | int64 | Number of index scans initiated |

The `DetectSeqScanHeavy` function fires W026 when `stat.SeqScan > 0 && stat.SeqScan > 10 * stat.IdxScan`. The 10x threshold accounts for small tables where sequential scans are appropriate (the planner correctly chooses sequential scans for small tables). At exactly 10x (e.g., SeqScan=100, IdxScan=10), the check does NOT trigger -- the comparison is strictly greater than.

## Data Structures

The workload package exports several data structures used by the check handlers, the stats command, and the serve HTTP API. These types represent query execution statistics from pg_stat_statements, table-level scan statistics from pg_stat_user_tables, index metadata for duplicate detection, and the results of duplicate index analysis. All types use standard Go struct conventions with JSON tags for the serve API endpoints.

```go
type StatementStats struct {
    QueryID       int64
    Query         string
    Calls         int64
    TotalExecTime float64
    MeanExecTime  float64
    Rows          int64
    Tables        []string
}

type TableScanStats struct {
    Schema  string
    Table   string
    SeqScan int64
    IdxScan int64
}

type IndexInfo struct {
    Schema  string
    Table   string
    Name    string
    Columns []string
}

type DuplicateIndex struct {
    Schema       string `json:"schema"`
    Table        string `json:"table"`
    Index        string `json:"index"`
    SupersetIndex string `json:"superset_index"`
}
```

## Diagnostic Code Summary

| Code | Severity | Tier | Description |
|------|----------|------|-------------|
| W022 | Warning | Structural | JSONB column without GIN index |
| W023 | Warning | Structural | Array column without GIN index |
| W024 | Warning | Structural | tsvector column without GIN index |
| W025 | Warning | Live | Potential N+1 query pattern |
| W026 | Warning | Live | Sequential scan heavy table |
| I005 | Info | Structural | Append-only timestamp without BRIN index |
| I006 | Info | Structural | Boolean column with dedicated single-column index |
| I007 | Info | Structural | Table with 10+ indexes |
