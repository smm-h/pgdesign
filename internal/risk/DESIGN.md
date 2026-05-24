# internal/risk

Shared risk classification for schema change operations. Used by diff/ and migrate/.

## Types

- `RiskLevel` -- enum: Safe, Caution, Dangerous.
- `LockType` -- enum: None, ShareRowExclusive, AccessExclusive, ShareUpdateExclusive.
- `Classification` -- struct: RiskLevel, LockType, Reversible (bool), DataLoss (bool), RequiresDML (bool), Suggestion (string -- safe alternative).

## Classifier

`Classify(op OpType, context OpContext) Classification`

OpContext provides: table size estimate (rows), PG version, column details (type, nullable, has default).

## Rules (PostgreSQL-specific)

| Operation | Risk | Lock | Reversible | Notes |
|-----------|------|------|-----------|-------|
| create_table | Safe | None | Yes (drop) | |
| drop_table | Dangerous | AccessExclusive | No | Data loss |
| add_column (nullable, no default) | Safe | AccessExclusive (brief) | Yes (drop) | Metadata-only |
| add_column (NOT NULL + immutable default, PG11+) | Safe | AccessExclusive (brief) | Yes (drop) | Metadata-only |
| add_column (NOT NULL + volatile default) | Dangerous | AccessExclusive | Yes (drop) | Table rewrite |
| add_column (NOT NULL, no default) | Dangerous | AccessExclusive | Yes (drop) | Fails on non-empty table |
| drop_column | Dangerous | AccessExclusive | No | Data loss |
| alter_column_type (widening) | Caution | AccessExclusive | Yes | May rewrite |
| alter_column_type (narrowing) | Dangerous | AccessExclusive | No | Data loss risk |
| set_not_null | Caution | AccessExclusive | Yes | Full table scan for validation |
| drop_not_null | Safe | AccessExclusive (brief) | Yes | Metadata-only |
| add_fk (NOT VALID) | Safe | ShareRowExclusive | Yes (drop) | No validation scan |
| add_fk (without NOT VALID) | Caution | ShareRowExclusive | Yes (drop) | Full scan |
| validate_constraint | Safe | ShareUpdateExclusive | N/A | Allows concurrent writes |
| create_index | Caution | ShareLock | Yes (drop) | Blocks writes |
| create_index_concurrently | Safe | ShareUpdateExclusive | Yes (drop) | Allows writes |
| drop_index | Caution | AccessExclusive | No | Performance impact |
| drop_index_concurrently | Safe | ShareUpdateExclusive | No | |
| rename_table | Caution | AccessExclusive | Yes | Breaks clients |
| rename_column | Caution | AccessExclusive | Yes | Breaks clients |
| alter_enum_add_value | Safe | None | No | Non-transactional (PG<12) |

## Suggestions

When a dangerous/caution operation is detected, Suggestion provides the safe alternative:
- create_index -> "Use CONCURRENTLY to avoid blocking writes"
- add_fk -> "Add with NOT VALID, then VALIDATE CONSTRAINT separately"
- add_column NOT NULL -> "Add as nullable, backfill, then SET NOT NULL"
- drop_column -> "Consider marking as deprecated first; data will be lost"

## Table size context

Risk can escalate based on table size:
- <10K rows: most operations are fast regardless
- 10K-1M rows: standard caution rules apply
- >1M rows: any AccessExclusive operation gets elevated risk (lock duration matters)
- >10M rows: even metadata-only operations should use lock_timeout

The classifier accepts estimated row count from pg_stat_user_tables.n_live_tup (available during migrate with --db connection).
