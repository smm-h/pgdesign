# internal/discover

Functional dependency discovery from live data. Implements the TANE algorithm.

## Purpose

When users don't declare functional dependencies manually, pgdesign can discover them by sampling data from a live database. This enables NF auditing without manual FD declarations.

## TANE algorithm

Based on: "TANE: An Efficient Algorithm for Discovering Functional and Approximate Dependencies" (Huhtala et al., 1999).

Core concepts:
1. **Partition refinement**: For each attribute set X, compute equivalence classes (groups of rows sharing the same X values). If X->A holds, every equivalence class of X maps to exactly one A value.
2. **Lattice traversal**: Start at level 1 (single attributes), move up level by level. At each level, test X\A -> A for all A in X.
3. **Pruning**: If X is a superkey (its partition has only singleton classes), no superset of X needs checking. If X->A is found, no superset of X determines A minimally.

## Interface

`Discover(conn *pgx.Conn, schema, table string, opts Options) ([]FuncDep, []Diagnostic)`

Options:
- SampleSize (int, default 5000) -- How many rows to sample (ORDER BY RANDOM() LIMIT N)
- MaxColumns (int, default 20) -- Skip tables wider than this (exponential blowup)
- ApproximateThreshold (float64, default 0.0) -- For approximate FDs (0.0 = exact only)

## Implementation details

- Partition representation: map of equivalence class hash -> []rowID
- Partition product: intersect two partitions by iterating equivalence classes
- Memory: O(|rows| * levels) -- levels bounded by MaxColumns
- Time: O((|rows| + |cols|^2.5) * 2^|cols|) worst case; pruning makes it practical for <=20 columns

## Practical limits

| Columns | Max partitions to check | Approximate time (5000 rows) |
|---------|------------------------|------------------------------|
| 5 | 32 | <1s |
| 10 | 1024 | 2-5s |
| 15 | 32768 | 10-30s |
| 20 | ~1M | 1-5min |

Tables with >20 columns: emit Info diagnostic, skip discovery, suggest user declare FDs manually or select a subset of columns.

## Integration

`pgdesign audit --db <url>` calls discover/ for each table lacking declared FDs. Discovered FDs are passed to audit/ for NF analysis. Results clearly marked as "inferred from data sample" (vs. declared).

## Limitations

- Discovered FDs are empirical, not semantic. The data might satisfy X->Y by coincidence.
- Sample-based: rare violations (in rows not sampled) can be missed.
- Approximate FDs (holding for 99% of rows) can be reported with --approximate flag.
