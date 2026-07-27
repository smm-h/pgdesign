# Tracking-write-path reconciliation (5.0)

Today there are TWO divergent tracking write paths plus one dead helper. 5.5
adopts a SINGLE write path. This note records the disposition.

## Current state (source-verified)

| Path | Callers | Operation |
|---|---|---|
| `state.go` `RecordMigration` | `baseline.go:120` (alive) | `INSERT INTO pgdesign_migrations (version, checksum, description)` |
| `state.go` `RemoveMigration` | **none in production** — only `migrate_test.go` | `DELETE FROM pgdesign_migrations WHERE version = $1` |
| inline SQL in `apply.go` | `apply.go:233`, `apply.go:263` | same `INSERT` (flat + phased paths) |
| inline SQL in `rollback.go` | `rollback.go:130`, `rollback.go:297` | `DELETE FROM pgdesign_migrations WHERE version = $1` |

So: writes happen via `RecordMigration` (baseline), inline `INSERT` (apply, two
sites), and inline `DELETE` (rollback, two sites). `RemoveMigration` is dead
(zero production callers).

## The single write path 5.5 adopts

The old table and all four write sites RETIRE. 5.5 introduces ONE journal writer
against the new structures:

- **Write:** a `journalOp(...)` helper writing a `pgdesign_migration_ops` row
  (intent then confirm, per the state machine) and advancing
  `pgdesign_chain_position.current_revision` in the same transaction as the
  edge's final-op confirm. This is the SOLE tracking write path — apply, upgrade's
  prefix fold, and baseline all go through it. No inline INSERTs remain.
- **Rollback (5.6):** does NOT delete tracking rows via a second inline path.
  Journal-driven rollback marks ops reverted / rewinds `current_revision` through
  the same writer; files are never consulted. The two inline `DELETE`s retire.
- **`RemoveMigration`:** DELETED (dead code; superseded by the journal writer per
  the dead-code policy). Its only references are in `migrate_test.go`, which 5.5
  updates alongside the rewrite (the tests are not deleted — they are retargeted
  to the new writer as the rewrite lands).
- **`RecordMigration` / `EnsureMigrationsTable` / `AppliedVersions` (semver
  sort):** retire with the old table; `AppliedVersions` is replaced by a read of
  `pgdesign_applied_migrations`.

Verification (5.5): "single write path by grep" — after the rewrite, exactly one
function issues `INSERT`/`UPDATE` against the tracking structures.
