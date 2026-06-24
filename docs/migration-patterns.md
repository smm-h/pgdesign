---
description: "How pgdesign generates safe, phased migrations with risk classification, NOT VALID auto-split, batched DML, and squash consolidation."
---

# Migration Intelligence

How pgdesign generates safe, phased migrations with risk classification.

This document covers the design rationale and algorithms behind the
`internal/migrate` and `internal/risk` packages. It explains why operations
are split, phased, and classified the way they are, and how the system
compares to other migration tools.

## Expand / Migrate / Contract Pattern

Every DDL and DML operation in a generated migration is classified into one of three phases based on its impact on the database: expand operations are additive and non-destructive, migrate operations move or validate data, and contract operations are destructive or require exclusive locks. This classification enables the executor to run operations in a safe order: all expand operations first, then data migrations, then destructive changes, minimizing the window where exclusive locks are held.

| Phase | Purpose | Examples |
|---|---|---|
| **expand** | Additive, non-destructive schema changes. No locks on existing data beyond brief metadata locks. | `CREATE TABLE`, `ADD COLUMN` (nullable or with default on PG 11+), `CREATE INDEX CONCURRENTLY`, `CREATE FUNCTION`, `CREATE VIEW` |
| **migrate** | Data movement and validation. Requires row-level access but no exclusive schema locks. | `UPDATE` backfills, `VALIDATE CONSTRAINT` |
| **contract** | Destructive or lock-heavy operations. Requires exclusive locks and may lose data. | `DROP TABLE`, `DROP COLUMN`, `ALTER COLUMN TYPE` (narrowing), `SET NOT NULL`, `RENAME TABLE`, `DROP INDEX` |

### Phase classification algorithm

Phase assignment happens in `AnnotatePhases` defined in `phase.go`, which processes each DDL and DML operation to determine whether it belongs in the expand, migrate, or contract phase. The classification uses a combination of operation type, risk level from the risk classification system, and hardcoded rules for known destructive operations. DML operations are unconditionally assigned to the migrate phase regardless of their risk level. For each DDL operation, the system:

1. Builds a `risk.OpContext` from the operation's own fields (nullable,
   has-default, PG version).
2. Calls `risk.Classify` to get the risk level.
3. Maps the operation to a phase via `classifyPhase`:
   - `validate_constraint` is always **migrate** (it scans data but holds
     only `SHARE UPDATE EXCLUSIVE`).
   - A hardcoded set of destructive operation types (`drop_table`,
     `drop_column`, `alter_column_type`, `set_not_null`, `rename_table`,
     `rename_column`, `drop_fk`, `drop_index`, `drop_index_concurrently`,
     `drop_unique`, `drop_view`, `drop_materialized_view`,
     `refresh_materialized_view`, `drop_sequence`, `drop_composite_type`,
     `drop_domain`, `drop_function`, `drop_trigger`, `drop_enum`) is always
     **contract**.
   - Remaining operations: `risk.Safe` maps to **expand**, anything else
     maps to **contract**.
4. All DML operations are unconditionally **migrate**.

### Single-file phase annotations

Phase metadata is stored as a `Phase` field on each `DDLOp` and `DMLOp` within a single migration TOML file, keeping all operations for a version change together in one document. This design contrasts with tools like pgroll and Reshape that use separate files or phases per migration step, and with tools like Flyway and Liquibase that use imperative migrations with no automatic phasing at all. The single-file approach simplifies version tracking while still enabling phased execution through per-operation annotations.

- **pgroll** uses separate expand/contract phases backed by column proxying
  and view-based dual-write, enabling zero-downtime column renames and type
  changes. More complex but handles cases pgdesign does not attempt
  (transparent application-level routing during migration).
- **Reshape** uses a declarative migration manifest and automatically
  derives expand/contract operations. More opinionated about the migration
  lifecycle.
- **Atlas** performs declarative schema diffing (similar to pgdesign) but
  without explicit phase annotations. It uses transaction advisory locks for
  concurrency control.
- **Flyway/Liquibase** use imperative migrations with no automatic phasing.
  Developers must manually order operations for safety.

pgdesign's approach keeps the migration as a single TOML file with per-operation
annotations. The executor (`applyPhased` in `apply.go`) groups operations by
phase and runs them in expand-then-migrate-then-contract order, each group in
its own transaction.

### Safe-ops collapse

When every operation in a migration is expand-phase (the common case of only
adding new tables or columns), `collapseSinglePhase` strips all phase
annotations. The migration runs as a single flat transaction with no phasing
overhead. This means simple additive migrations have zero ceremony -- phase
annotations only appear when the migration genuinely needs phased execution.

### Squash strips phases

When multiple migrations are squashed (`squash.go`), phase annotations are
removed from the result. A squashed migration represents a fresh schema
creation rather than an incremental change, so phasing is not meaningful.

## NOT VALID + VALIDATE CONSTRAINT

### The problem: lock queue cascades

Adding a foreign key constraint on a large table acquires an ACCESS EXCLUSIVE lock on the source table and a SHARE ROW EXCLUSIVE lock on the referenced table, blocking all concurrent reads and writes. PostgreSQL's lock queue is FIFO, so if the lock waits behind a running transaction, every subsequent query on that table queues behind the lock request, creating a pile-up that can exhaust the connection pool and cause an outage.

### The solution: two-phase constraint addition

`splitLargeFKOp` in `generate.go` detects when a foreign key is being added to a table exceeding the `largeFKThreshold` (default 10,000 rows from `pg_stat_user_tables`). It splits the single `add_fk` into two phases: first adding with NOT VALID for a brief metadata lock, then validating under a SHARE UPDATE EXCLUSIVE lock that allows concurrent reads and writes. The two operations are:

1. **`add_fk_not_valid`** -- `ALTER TABLE ... ADD CONSTRAINT ... NOT VALID`.
   Registers the constraint in the catalog without scanning existing rows.
   Takes only a brief `ACCESS EXCLUSIVE` lock (metadata update only). Risk
   classification: `Safe`.

2. **`validate_constraint`** -- `ALTER TABLE ... VALIDATE CONSTRAINT ...`.
   Scans all existing rows to verify compliance. Takes only a
   `SHARE UPDATE EXCLUSIVE` lock, which allows concurrent reads and writes.
   Risk classification: `Safe`. Phase: always **migrate**.

This pattern also applies to CHECK constraints on large tables. The split is
transparent -- the generated migration contains both operations in the correct
order with appropriate phase annotations.

## Batched DML

### The problem: MVCC dead tuple bloat

When pgdesign generates data migrations (backfills for new NOT NULL columns,
data transformations), large tables present an MVCC problem. A single
`UPDATE ... SET col = value WHERE col IS NULL` on a 10M-row table creates
10M dead tuples simultaneously. These dead tuples consume disk space and
degrade query performance until `VACUUM` reclaims them. Autovacuum may not
trigger quickly enough or may be unable to keep up with the sudden burst.

### The solution: batched execution

The `DMLOp` struct has a `BatchSize` field that controls how many rows are processed in each iteration. When `BatchSize > 0`, the executor (`executeDMLOp` in `apply.go`) runs the SQL in a loop, processing one batch at a time with a commit between batches. This gives autovacuum an opportunity to clean up dead tuples between batches and keeps the transaction size bounded. The SQL itself must include a LIMIT clause matching the batch size:

```
for {
    result := exec(sql)  // SQL must include LIMIT <batchsize>
    if result.RowsAffected() == 0 {
        break
    }
}
```

Each batch processes a bounded number of rows, then commits. Between batches,
autovacuum has an opportunity to clean up dead tuples. The SQL itself must
include the appropriate `LIMIT` clause -- the batch loop simply re-executes
until zero rows match.

### Backfill SQL generation

`buildBackfillSQL` in `generate.go` produces the UPDATE statement used to fill NULL values when adding a NOT NULL constraint to a column with existing data. The generated SQL uses COALESCE to preserve non-NULL values while filling NULL rows with type-appropriate defaults from `typeZeroValue`: `false` for boolean, empty string for text, zero UUID for UUID, empty JSON for JSONB, and zero for numerics.

```sql
UPDATE "table" SET "col" = COALESCE("col", <default>) WHERE "col" IS NULL
```

`typeZeroValue` provides type-appropriate defaults: `false` for boolean,
`''` for text types, `'00000000-0000-0000-0000-000000000000'` for UUID,
`'{}'` for JSON/JSONB, `now()` for timestamp/date types, `0` for numeric
types. Backfill DML ops are generated when adding `NOT NULL` to an existing
column on tables exceeding the `expandContractThreshold` (default: 10M rows).

## Non-immutable Default Detection

### PG 11+ metadata-only ADD COLUMN

PostgreSQL 11 introduced an optimization: adding a column with an
immutable default no longer rewrites the table. The default is stored in
`pg_attribute.attmissingval` and applied lazily when rows are read. This
makes `ADD COLUMN ... DEFAULT 'constant'` a metadata-only operation
regardless of table size.

### Non-immutable defaults still require a rewrite

Non-immutable functions that return different values on each call cannot use the PG 11 metadata-only optimization because PostgreSQL must compute a distinct value for each existing row. The `isNonImmutableDefault` function in `generate.go` detects these volatile defaults by checking for known function calls like `gen_random_uuid()` and `now()`. When detected, the risk classification is escalated because the operation requires a full table rewrite or batched DML.

- `now()`
- `clock_timestamp()`
- `random()`
- `nextval(...)`
- `gen_random_uuid()`
- `uuid_generate_v4()`
- `uuid_generate_v7()`
- `txid_current()`
- `statement_timestamp()`

When a non-immutable default is detected on a NOT NULL column addition, the risk
classification escalates. `classifyAddColumn` in `risk.go` handles this:

- Nullable column, no default: **Safe** (metadata-only `ACCESS EXCLUSIVE`).
- NOT NULL with immutable default on PG 11+: **Safe** (metadata-only).
- NOT NULL with default on pre-PG 11: **Dangerous** (full table rewrite,
  `RequiresDML` set).
- NOT NULL without any default: **Dangerous** (fails immediately on
  non-empty tables).

## Squash Consolidation

`SquashMigrations` in `squash.go` combines a range of sequential migrations into a single optimized migration that produces the same final schema state. After concatenating all DDL and DML operations from the source migrations, the `optimizeDDLOps` function runs a three-pass optimization pipeline that eliminates redundant operations, merges sequential type changes, and folds additive operations into CREATE TABLE statements. The result is a minimal migration that is typically much shorter than the sum of its source migrations.

### Pass 1: Cancel inverse pairs

Scans for adjacent pairs of operations that cancel each other out. When an
`add` and its corresponding `drop` (or vice versa) target the same object,
both are removed. The system recognizes 12 inverse pair types:

| Forward | Inverse |
|---|---|
| `add_column` | `drop_column` |
| `create_table` | `drop_table` |
| `create_index` | `drop_index` |
| `create_index_concurrently` | `drop_index` |
| `create_index_concurrently` | `drop_index_concurrently` |
| `add_fk` | `drop_fk` |
| `add_unique` | `drop_unique` |
| `add_check` | `drop_check` |
| `create_enum` | `drop_enum` |
| `set_not_null` | `drop_not_null` |
| `create_function` | `drop_function` |
| `create_trigger` | `drop_trigger` |

Target matching (`sameTarget`) compares Table+Column for column-level ops,
Table for table-level ops, and Name for named objects (indexes, enums,
functions).

### Pass 2: Merge sequential type changes

Multiple `alter_column_type` operations on the same table.column collapse
to just the final type change. If a column was changed from `int` to
`bigint` in one migration and then to `text` in another, the squashed
result contains only the `int -> text` change.

### Pass 3: Consolidate into CREATE TABLE

When a `create_table` is followed by operations that modify the same table
(add columns, add foreign keys, create indexes, add unique constraints, add
check constraints, add exclusion constraints), those operations fold into the
`CREATE TABLE` statement's `ConsolidatedOps` field. The SQL generator
(`opCreateTableConsolidated` in `sql_gen.go`) builds a `model.Table` from
the base `create_table` fields plus the consolidated operations and emits a
single complete `CREATE TABLE` statement.

### Down-op handling

`buildSquashedDown` examines the down operations of all source migrations.
If any original down operation is marked `Irreversible`, the entire squashed
migration's down is marked irreversible (conservative: if you cannot undo
one step, you cannot undo the combined result). Otherwise, existing down
operations are preserved.

### Result reporting

`SquashResult` reports the complete outcome of the squash operation including the squashed `Migration` struct, the list of original file paths that were consumed, and counts of how many operations were optimized at each pass: cancelled inverse pairs, merged sequential type changes, and operations consolidated into CREATE TABLE statements. These counts are displayed to the user to explain how the squash reduced the migration's complexity and help verify that the optimization produced the expected result.

## Multi-Step Rollback

### Single rollback

`Rollback` in `rollback.go` reverts the most recently applied migration by executing its down operations in reverse order within a transaction. The function acquires an advisory lock to prevent concurrent rollback or apply operations, loads the migration file from disk, verifies that all operations are reversible before executing any rollback steps, and removes the version from the tracking table on successful completion. Non-transactional operations are handled by committing and reopening the transaction as needed.

1. Acquires advisory lock (prevents concurrent rollback/apply).
2. Queries the `pgdesign_migrations` table for the latest applied version.
3. Loads and parses the migration file from disk.
4. Runs `checkReversibility` -- verifies no `DDLOp` or `DMLOp` has
   `Down.Irreversible == true`. Aborts with a descriptive error if any
   operation is irreversible.
5. Sets `lock_timeout` (default: `5s`).
6. Opens a transaction.
7. Executes DML down operations in reverse order.
8. Executes DDL down operations in reverse order (handling non-transactional
   ops by committing and reopening the transaction as needed).
9. Removes the version from `pgdesign_migrations`.
10. Commits.

### Range rollback

`RollbackTo` rolls back all migrations applied after a specified target version, reverting them in reverse application order. The critical design feature is the pre-check: before executing any rollback step, the function verifies that ALL migrations in the rollback range are fully reversible. Without this pre-check, a partial rollback could leave the database in an intermediate state when a later migration turns out to contain an irreversible operation. Each migration is rolled back in its own transaction for isolation.

1. Acquires advisory lock.
2. Validates that the target version is currently applied.
3. **Pre-checks ALL migrations for reversibility** before executing any
   rollback. This is critical -- without the pre-check, a partial rollback
   could leave the database in an intermediate state when a later migration
   turns out to be irreversible.
4. Rolls back each migration in reverse order, each in its own transaction.
5. On partial failure, returns both the list of successfully rolled-back
   versions and the error, so the caller knows the exact state.

### Reversibility

Each `DDLOp` carries a `Down *DownOp` containing the inverse operations.
`DownOp.Irreversible` is set to `true` for operations that cannot be
reversed (e.g., `drop_column` loses column data, `alter_enum_add_value`
cannot remove enum values in PostgreSQL). `checkReversibility` scans all ops
and returns a descriptive error identifying which operation is irreversible.

## Risk Classification

### Risk levels

Every DDL operation receives a risk level from `risk.Classify` in `risk/risk.go`, which evaluates the operation type, its parameters, and the PostgreSQL version context to determine whether the operation is Safe, Caution, or Dangerous. The classification drives multiple downstream decisions including phase assignment, NOT VALID splitting, safe-ops collapse, and diagnostic generation. Risk levels are also displayed in migration plan output for human review.

| Level | Meaning | Examples |
|---|---|---|
| **Safe** | No data loss possible, minimal locking. | `CREATE TABLE`, `ADD COLUMN` (nullable or default on PG 11+), `CREATE INDEX CONCURRENTLY`, `add_fk_not_valid`, `validate_constraint` |
| **Caution** | Brief locks or semantic changes. May escalate to Dangerous on large tables. | `ADD NOT NULL`, `ADD CONSTRAINT`, `RENAME TABLE`, `CREATE INDEX` (non-concurrent), `DROP INDEX`, `ENABLE/DISABLE/FORCE RLS` |
| **Dangerous** | Data loss, full table rewrite, or extended exclusive locks. | `DROP TABLE`, `DROP COLUMN`, `ALTER COLUMN TYPE` (narrowing), `DROP MATERIALIZED VIEW`, `DROP COMPOSITE TYPE`, `DROP DOMAIN` |

### Lock types

The risk system also tracks the PostgreSQL lock mode that each operation acquires, because the lock type determines whether concurrent reads and writes can proceed during the operation. PostgreSQL uses a hierarchy of lock modes from the least restrictive (no lock) to the most restrictive (ACCESS EXCLUSIVE which blocks everything). Understanding the lock mode is essential for assessing the real-world impact of a migration on a production database under load.

| Lock | Concurrent reads | Concurrent writes | Concurrent DDL |
|---|---|---|---|
| `LockNone` | yes | yes | yes |
| `LockShareUpdateExclusive` | yes | yes | no |
| `LockShareLock` | yes | no | no |
| `LockShareRowExclusive` | yes | limited | no |
| `LockAccessExclusive` | no | no | no |

### Table size escalation

`applyTableSizeEscalation` modifies the base risk classification based on the estimated row count of the affected table, because the same operation has very different impacts on a 1000-row table versus a 10-million-row table. An ACCESS EXCLUSIVE lock on a small table completes in milliseconds, but on a large table it blocks all concurrent access for a duration proportional to the data volume. The function uses two escalation thresholds based on row count estimates from `pg_stat_user_tables`.

- **>1M rows** with `ACCESS EXCLUSIVE` lock and `Caution` base risk:
  escalated to **Dangerous**. The reasoning is that an exclusive lock on a
  million-row table will block all reads for a non-trivial duration.
- **>10M rows**: a `lock_timeout` suggestion is appended, recommending the
  operator set a timeout to prevent indefinite blocking.

### ADD COLUMN classification

`classifyAddColumn` has special-case logic because the risk of adding a column depends heavily on the combination of nullability, default value presence, default immutability, and PostgreSQL version. A nullable column addition is always metadata-only, but a NOT NULL column with a volatile default like `gen_random_uuid()` on pre-PG 11 requires a full table rewrite that holds an ACCESS EXCLUSIVE lock for the duration. The function evaluates all four factors to produce the correct risk classification.

| Scenario | Risk | Lock | Reason |
|---|---|---|---|
| Nullable, no default | Safe | ACCESS EXCLUSIVE (brief) | Metadata-only |
| NOT NULL + default, PG 11+ | Safe | ACCESS EXCLUSIVE (brief) | Metadata-only (PG 11 optimization) |
| NOT NULL + default, PG <11 | Dangerous | ACCESS EXCLUSIVE | Full table rewrite; `RequiresDML` |
| NOT NULL, no default | Dangerous | ACCESS EXCLUSIVE | Fails on non-empty tables |

### How risk levels are used

1. **Migration plan output**: operations are annotated with risk levels for
   human review (color-coded in the CLI).
2. **Phase assignment**: risk level feeds into `classifyPhase` to determine
   expand/migrate/contract placement.
3. **NOT VALID splitting**: `splitLargeFKOp` uses the row count estimate
   and threshold to decide whether to split FK additions.
4. **Safe-ops collapse**: `collapseSinglePhase` checks whether all ops are
   expand-phase (which correlates with Safe risk) to decide whether phasing
   can be stripped.
5. **Diagnostic generation**: `classifyOp` in `generate.go` produces
   warnings for `Caution` operations and errors for `Dangerous` ones,
   including the risk system's `Suggestion` text.

## Non-Transactional Operations

Some PostgreSQL DDL statements cannot execute inside a transaction block and will fail with an error if attempted. The `IsNonTransactional` function in `sql_gen.go` identifies these operations so the migration executor can handle them by committing the current transaction, executing the operation on the bare connection, and then opening a new transaction for subsequent operations. This transparent handling means migrations can mix transactional and non-transactional operations without manual intervention. Three operation types are identified:

- **`create_index_concurrently`** -- `CREATE INDEX CONCURRENTLY` builds
  the index without holding a lock that blocks writes, but requires running
  outside a transaction.
- **`drop_index_concurrently`** -- `DROP INDEX CONCURRENTLY` similarly
  cannot run inside a transaction.
- **`alter_enum_add_value`** -- `ALTER TYPE ... ADD VALUE` adds a new enum
  member and cannot be transactional (PostgreSQL restriction since PG 10).

When `applyOne` encounters a non-transactional operation during flat
(non-phased) execution, it commits the current transaction, executes the
operation on the bare connection, then opens a new transaction for subsequent
operations. `applyPhaseOps` handles the same case during phased execution.
The SQL generator adds comments to non-transactional blocks so the output is
self-documenting.

## Advisory Locks

Migrations use PostgreSQL session-level advisory locks to prevent concurrent execution by multiple migration processes connecting to the same database. The lock is acquired at the beginning of every apply, rollback, and rollback-to operation, ensuring that no two migration processes can modify the schema simultaneously. The lock uses non-blocking acquisition via `pg_try_advisory_lock`, which returns false immediately if another process holds the lock rather than waiting indefinitely.

```sql
SELECT pg_try_advisory_lock(hashtext('pgdesign_migrate'))
```

- **Non-blocking acquisition**: `pg_try_advisory_lock` returns `false`
  immediately if the lock is held, rather than waiting. The migration aborts
  with a clear error: "another migration is in progress."
- **String-based hash**: The lock key is `hashtext('pgdesign_migrate')`,
  which produces a stable `int4` from a human-readable identifier. This
  avoids magic numbers and makes the lock identifiable in
  `pg_locks`.
- **Session-level scope**: The lock persists for the connection lifetime,
  released explicitly via `pg_advisory_unlock(hashtext('pgdesign_migrate'))`
  at the end of the migration or on connection close.
- **Both apply and rollback**: Advisory locks are acquired at the start of
  `Apply`, `Rollback`, and `RollbackTo`. This prevents conflicts between
  concurrent apply and rollback operations, not just apply-apply races.

## Shadow Database Testing

pgdesign does not currently use a shadow database for migration testing.
Instead, diffs are computed from the parsed TOML model -- `diff.SchemaDiff`
compares two `model.Schema` values (or a model against a live database via
`introspect`), and migration generation operates entirely on the diff result.
No SQL is applied to a real database during generation.

This contrasts with:

- **Prisma**, which spins up a shadow database to detect schema drift
  (applies migrations to a temporary database and compares the result
  against the expected state).
- **Atlas**, which uses a "dev database" for SQL normalization (parses and
  re-serializes SQL through a real PostgreSQL instance to handle
  dialect-specific behavior).

If shadow database testing were added to pgdesign, the approach would use
`CREATE DATABASE` (requiring `CREATEDB` privilege) rather than schema-level
isolation, because PostgreSQL extensions must be installed at the database
level -- a schema-isolated test could not exercise extension-dependent types
(e.g., `vector` from pgvector, `ltree`, `hstore`).

## Migration Tracking

Applied migrations are recorded in a `pgdesign_migrations` table (created
automatically by `EnsureMigrationsTable` in `state.go`). Each row stores the
version string, the SHA-256 checksum of the migration file at apply time,
and the application timestamp. The checksum enables drift detection -- if a
migration file is modified after being applied, the mismatch is detectable.

Migration files are discovered by scanning the migrations directory for
`*.toml` files. Filenames must be valid semver versions. Files are sorted in
semver order and applied sequentially. The `lock_timeout` parameter
(default: `5s`) is set on the connection before each migration to prevent
indefinite blocking on lock acquisition.
