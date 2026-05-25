# Expand-contract migration planner

The internal/migrate/DESIGN.md describes an expand-contract planner that decomposes dangerous operations into safe multi-step migrations. This is entirely unimplemented.

## Missing components

### DML detection
GenerateMigration does not detect cases that need DML:
- NOT NULL addition on non-empty tables
- Column renames
- Type narrowing

### Expand-contract decomposition
No multi-step decomposition exists for:
- NOT NULL addition: should become add nullable -> backfill -> set_not_null
- Column rename: should become add new column -> copy data -> drop old column
- Type change: should become add new column -> transform data -> swap columns

### Plan command
`pgdesign migrate plan` is registered in the CLI but has no implementation. Should show what migrations would be generated without writing files.

### Dry-run mode
`pgdesign migrate apply --dry-run` is described but Apply has no dry-run parameter or mode.

### Row-estimate-based safety
Generate never queries pg_stat_user_tables for row estimates. OpContext.EstimatedRows is always zero. This means:
- No auto-conversion of create_index to create_index_concurrently for large tables
- No auto-split of FK operations into NOT VALID + VALIDATE
- Table size escalation in risk.Classify is never triggered during generation

### Threshold-based file splitting
For large tables, expand-contract steps should be split into separate migration files. Not implemented.

## Effort

Large. This is the biggest missing feature in the migration system.
