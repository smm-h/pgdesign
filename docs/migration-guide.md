---
title: "Migration Guide"
description: "Guide to pgdesign's migration system covering generation, planning, application, rollback, squash consolidation, safety linting, and risk classification."
---

# Migration Guide

pgdesign generates migrations by diffing your TOML schema against a live database. Migrations are TOML files containing DDL and DML operations with rollback instructions and safety diagnostics.

## Migration file format

Migration files are TOML documents containing `[[ddl]]` and `[[dml]]` operation arrays, where each operation specifies a schema change and its corresponding rollback instruction. The file starts with a description field summarizing the migration's purpose, followed by DDL operations for structural changes like creating tables, adding columns, and creating indexes, and DML operations for data migrations like backfills and transformations. Each operation includes risk classification and safety metadata.

```toml
description = "Add posts table, add status column to users"

[[ddl]]
op = "create_table"
table = "public.posts"
comment = "User-authored posts"
pk = ["id"]

[[ddl]]
op = "add_column"
table = "public.users"
column = "status"
type = "text"
not_null = true
default = "'active'"
down = { op = "drop_column", table = "public.users", column = "status" }

[[ddl]]
op = "add_fk"
table = "public.posts"
name = "fk_posts_author"
columns = ["author_id"]
ref_table = "public.users"
ref_cols = ["id"]
on_delete = "CASCADE"
down = { op = "drop_fk", table = "public.posts", name = "fk_posts_author" }

[[ddl]]
op = "create_index"
table = "public.posts"
name = "idx_posts_author_id"
columns = ["author_id"]
down = { op = "drop_index", table = "public.posts", name = "idx_posts_author_id" }

[[dml]]
op = "backfill"
sql = "UPDATE public.users SET status = COALESCE(status, 'active') WHERE status IS NULL"
down = { irreversible = true }
```

### DDL operations

| Operation | Description |
|-----------|-------------|
| `create_table` | Create a new table |
| `drop_table` | Drop a table |
| `add_column` | Add a column |
| `drop_column` | Drop a column |
| `alter_column_type` | Change a column's type |
| `set_not_null` | Add NOT NULL constraint |
| `drop_not_null` | Remove NOT NULL constraint |
| `alter_column_default` | Change column default |
| `drop_column_default` | Remove column default |
| `rename_column` | Rename a column |
| `rename_table` | Rename a table |
| `add_fk` | Add a foreign key constraint |
| `drop_fk` | Drop a foreign key constraint |
| `create_index` | Create an index |
| `drop_index` | Drop an index |
| `create_index_concurrently` | Create an index concurrently |
| `drop_index_concurrently` | Drop an index concurrently |
| `add_unique` | Add a unique constraint |
| `drop_unique` | Drop a unique constraint |
| `add_check` | Add a check constraint |
| `drop_check` | Drop a check constraint |
| `create_enum` | Create an enum type |
| `drop_enum` | Drop an enum type |
| `alter_enum_add_value` | Add a value to an enum type |
| `create_partition` | Create a partition child table |
| `create_view` | Create a view |
| `drop_view` | Drop a view |
| `create_or_replace_view` | Create or replace a view |
| `create_materialized_view` | Create a materialized view |
| `drop_materialized_view` | Drop a materialized view |
| `refresh_materialized_view` | Refresh a materialized view |
| `alter_index_set` | Alter index storage parameters |
| `create_function` | Create a function |
| `drop_function` | Drop a function |
| `create_trigger` | Create a trigger |
| `drop_trigger` | Drop a trigger |

### DML operations

| Operation | Description |
|-----------|-------------|
| `backfill` | Run a data migration SQL statement |
| `transform` | Run a data transformation SQL statement |

### Down (rollback) operations

Each DDL or DML operation can include a `down` key describing how to reverse it during rollback. The down key supports three forms: an inline single rollback operation as a table, an `irreversible = true` marker for operations that cannot be undone like dropping a column with data, or a `[[down.ops]]` array for operations that require multiple rollback steps. When running `migrate rollback`, pgdesign executes these down operations in reverse order to restore the database to its previous state.

```toml
# Inline single rollback op
down = { op = "drop_column", table = "public.users", column = "status" }

# Irreversible operation
down = { irreversible = true }

# Multiple rollback ops
[down]
[[down.ops]]
op = "drop_fk"
table = "public.posts"
name = "fk_posts_author"
[[down.ops]]
op = "drop_column"
table = "public.posts"
column = "author_id"
```

## Commands

### migrate generate

Generate a versioned migration file by comparing your TOML schema definitions against a live PostgreSQL database. The command connects to the database, introspects its current schema, computes the structural diff, classifies each change by risk level, generates both forward and rollback operations, and writes the resulting TOML migration file to the migrations directory with safety linting annotations.

```
pgdesign migrate generate schema.toml --db "postgres://user:pass@localhost/mydb" --version 0.2.0
```

| Flag | Description |
|------|-------------|
| `--version` | Migration version (semver format) |
| `--dir` | Migrations directory (default: `migrations/`) |

The generated file is saved as `migrations/<version>.toml`.

### migrate plan

Preview the migration operations that would be generated without writing any files to disk. This command performs the same schema diff and risk classification as `migrate generate` but displays the results in the terminal instead of creating a migration file. Use this to review what changes pgdesign detects before committing to a migration version, verify that expected changes are captured, and check risk levels and safety diagnostics.

```
pgdesign migrate plan schema.toml --db "postgres://user:pass@localhost/mydb"
```

Shows the list of operations, risk classifications, and safety diagnostics.

### migrate apply

Apply all pending migrations to the target database in semver order, running each migration inside its own transaction with advisory locking to prevent concurrent execution. Non-transactional operations like CREATE INDEX CONCURRENTLY and ALTER TYPE ADD VALUE are automatically detected and executed outside transactions. Applied migrations are tracked in the `pgdesign_migrations` table, which is created automatically on first use.

```
pgdesign migrate apply --db "postgres://user:pass@localhost/mydb"
```

| Flag | Description |
|------|-------------|
| `--dir` | Migrations directory (default: `migrations/`) |
| `--dry-run` | Show SQL without executing |

Migrations are applied in semver order. Each migration runs in a transaction, except for non-transactional operations (like `CREATE INDEX CONCURRENTLY` or `ALTER TYPE ADD VALUE`) which are committed and re-started around.

An advisory lock prevents concurrent migration execution. Applied migrations are tracked in the `pgdesign_migrations` table (created automatically).

### migrate rollback

Roll back applied migrations to a specified target version by executing the down operations from each migration in reverse application order. The rollback acquires an advisory lock to prevent concurrent execution and verifies that all operations in the rollback path are reversible before starting. If any operation is marked `irreversible = true`, the rollback is refused with a clear error message identifying the blocking operation.

```
pgdesign migrate rollback --db "postgres://user:pass@localhost/mydb"
```

| Flag | Description |
|------|-------------|
| `--dir` | Migrations directory (default: `migrations/`) |

Rollback executes the `down` operations in reverse order. If any operation is marked `irreversible`, the rollback is refused.

### migrate status

Show which migrations have been applied to the target database and which are still pending. The command reads the migration tracking table and compares it with the files in the migrations directory, displaying each migration's version, applied timestamp, and current status. This is useful for verifying the state of a database before applying new migrations or diagnosing issues with migration ordering.

```
pgdesign migrate status --db "postgres://user:pass@localhost/mydb"
```

### migrate squash

Squash a range of sequential migrations into a single consolidated migration file that produces the same final schema state. The squash command reads all migrations in the specified version range, merges their DDL and DML operations, eliminates redundant operations like columns added then dropped, merges sequential type changes, and folds column additions into CREATE TABLE statements where possible. Only squash migrations that have been applied to all target environments.

```
pgdesign migrate squash --from 0.1.0 --to 0.5.0
```

| Flag | Description |
|------|-------------|
| `--from` | Start version (inclusive) |
| `--to` | End version (inclusive) |
| `--dir` | Migrations directory (default: `migrations/`) |

The squash command reads all migrations in the specified range, merges their DDL and DML operations into a single migration file, eliminates redundant operations (e.g., a column added then dropped), and writes the result. The squashed migration replaces the individual files.

Squashing is useful for reducing migration count in long-lived projects. Only squash migrations that have already been applied to all environments -- squashing unapplied migrations changes their checksums.

### migrate test

Test migrations against a staging database to verify they apply and roll back cleanly before deploying to production. The test command applies all pending migrations, then rolls them back, verifying that every migration applies without errors, all reversible migrations roll back cleanly, and the database returns to its original state after the full rollback cycle. With --shadow mode, the command replays all migrations into a fresh database and diffs the result against the TOML schema.

```
pgdesign migrate test --db "postgres://user:pass@localhost/staging"
```

| Flag | Description |
|------|-------------|
| `--db` | Staging database connection URL |
| `--dir` | Migrations directory (default: `migrations/`) |
| `--timeout` | Timeout in seconds (default: 60) |

The test command applies all pending migrations to the staging database, then rolls them back, verifying that:
1. All migrations apply without errors
2. All reversible migrations roll back cleanly
3. The database returns to its original state after rollback

Use a dedicated staging database for migration testing -- the test modifies and restores the schema. Irreversible operations (marked `irreversible = true` in the migration) are reported but do not fail the test.

## Safety linting and risk classification

Every DDL operation in a generated migration is classified by risk level based on the type of schema change, the PostgreSQL lock it requires, and the estimated size of the affected table. This classification helps teams assess the impact of migrations before applying them to production databases. Risk levels are displayed in `migrate plan` output and annotated in migration files for review.

| Risk Level | Meaning |
|------------|---------|
| **Safe** | No data loss, minimal locking |
| **Caution** | May require locks or have side effects |
| **Dangerous** | Data loss possible or heavy locking on large tables |

### Risk by operation

| Operation | Base Risk | Lock | Notes |
|-----------|-----------|------|-------|
| `create_table` | Safe | None | |
| `drop_table` | Dangerous | AccessExclusive | Data loss |
| `add_column` (nullable) | Safe | AccessExclusive | Metadata-only |
| `add_column` (NOT NULL + default, PG11+) | Safe | AccessExclusive | Metadata-only |
| `add_column` (NOT NULL + default, pre-PG11) | Dangerous | AccessExclusive | Table rewrite |
| `add_column` (NOT NULL, no default) | Dangerous | AccessExclusive | Fails on non-empty tables |
| `drop_column` | Dangerous | AccessExclusive | Data loss |
| `alter_column_type` (widening) | Caution | AccessExclusive | |
| `alter_column_type` (narrowing) | Dangerous | AccessExclusive | Data loss possible |
| `set_not_null` | Caution | AccessExclusive | Full table scan |
| `drop_not_null` | Safe | AccessExclusive | |
| `add_fk` | Caution | ShareRowExclusive | |
| `create_index` | Caution | ShareLock | Blocks writes |
| `create_index_concurrently` | Safe | ShareUpdateExclusive | |
| `drop_index` | Caution | AccessExclusive | |
| `add_unique` | Caution | ShareLock | |
| `add_check` | Caution | ShareRowExclusive | |
| `alter_enum_add_value` | Safe | None | Irreversible |

### Table size escalation

Risk is escalated based on estimated row counts retrieved from `pg_stat_user_tables` during migration generation. Large tables amplify the impact of lock-heavy operations because AccessExclusive locks block all concurrent reads and writes for the duration of the operation. Tables with over one million rows have their Caution-level lock operations escalated to Dangerous, and tables with over ten million rows receive additional lock_timeout configuration suggestions.

- Tables with >1M rows: Caution + AccessExclusive is escalated to Dangerous
- Tables with >10M rows: lock_timeout suggestion is added

### Large FK threshold

When adding a foreign key to a table with more than 10,000 rows (configurable), pgdesign warns that `ADD CONSTRAINT` without `NOT VALID` will lock the table during validation. The recommendation is to add with `NOT VALID` first, then `VALIDATE CONSTRAINT` in a separate step.

## Expand-contract decomposition

For large tables exceeding the configured row threshold (10 million rows by default, configurable via `expand_contract_threshold` in pgdesign.toml), pgdesign automatically decomposes certain high-risk operations into safer multi-step sequences. This expand-contract decomposition pattern reduces lock duration on large tables by splitting a single blocking operation into multiple smaller steps that each hold locks for shorter periods. The threshold is checked against pg_stat_user_tables estimates during migration generation.

**SET NOT NULL on large tables:**
1. A DML `backfill` step fills NULL values with appropriate defaults
2. A DDL `set_not_null` step adds the constraint

**Type narrowing on large tables:**
A warning is emitted suggesting the expand-contract pattern:
1. Add a new column with the target type
2. Backfill data from the old column
3. Swap columns (rename)
4. Drop the old column

## Append-only trigger migrations

When a table's `append_only` attribute changes between schema versions, pgdesign generates trigger-based migrations to enforce or remove row immutability at the database level. The append-only enforcement uses a shared PL/pgSQL function that raises an exception on any UPDATE or DELETE attempt, with a per-table BEFORE trigger that invokes the function. This design reuses a single function across all append-only tables while maintaining per-table trigger control for enabling and disabling the protection independently.

**Enabling append-only (`false` to `true`):**
1. Creates a shared `pgdesign_deny_mutation()` function if this is the first append-only table (the function raises an exception on UPDATE or DELETE attempts)
2. Creates a per-table `BEFORE UPDATE OR DELETE` trigger that calls the shared function

```sql
-- Shared function (created once, reused across all append-only tables)
CREATE OR REPLACE FUNCTION pgdesign_deny_mutation()
RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION 'mutations not allowed on append-only table %', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

-- Per-table trigger
CREATE TRIGGER trg_audit_log_deny_mutation
  BEFORE UPDATE OR DELETE ON public.audit_log
  FOR EACH ROW EXECUTE FUNCTION pgdesign_deny_mutation();
```

**Disabling append-only (`true` to `false`):**
1. Drops the per-table trigger
2. Drops the shared `pgdesign_deny_mutation()` function if no other append-only tables remain

## Array type migrations

Changing a column between scalar and array types (or vice versa) is treated as a type change by diff/migrate. For example, changing a column from `text` to `text[]` (by adding `array = true`) generates an `alter_column_type` migration operation:

```toml
[[ddl]]
op = "alter_column_type"
table = "public.posts"
column = "tags"
from = "text"
to = "text[]"
```

## JSON Schema constraint migrations

Adding a `json_schema` attribute to a JSONB column generates CHECK constraints based on the referenced JSON Schema's required properties. These constraints validate that the JSONB value contains the expected top-level keys.

When the `json_schema` reference changes (pointing to a different schema file or the schema file is updated), pgdesign generates updated CHECK constraints -- dropping the old constraint and adding the new one.

## View migrations

pgdesign generates view migrations when the diff engine detects changes to view definitions between the TOML schema and the live database. Views support three migration operations: creation, removal, and replacement. PostgreSQL's CREATE OR REPLACE VIEW can update a view definition in place without dropping dependent objects as long as the output column list remains compatible, which makes view changes generally safer than table modifications. Views are ordered after table operations in the migration file.

**Adding a view:** Generates `CREATE VIEW` with the full query definition.

**Removing a view:** Generates `DROP VIEW`.

**Changing a view:** Generates `CREATE OR REPLACE VIEW` with the updated query. PostgreSQL's `CREATE OR REPLACE VIEW` updates the view definition in place without dropping dependent objects, as long as the column list remains compatible.

Views are ordered after table operations in the migration file to ensure referenced tables exist.

## Materialized view migrations

Materialized views cannot be altered in place using CREATE OR REPLACE like regular views, so any change to the query definition or WITH DATA setting requires a full drop-and-recreate cycle. This means materialized view migrations are inherently more disruptive than regular view migrations because the stored data must be recomputed. Index definitions on materialized views are also recreated after the view rebuild, and index-only changes that do not affect the view query are handled individually without triggering a full rebuild.

**Adding a materialized view:** Generates `CREATE MATERIALIZED VIEW` followed by `CREATE INDEX` for any defined indexes.

**Removing a materialized view:** Generates `DROP MATERIALIZED VIEW`.

**Changing a materialized view:** Generates `DROP MATERIALIZED VIEW` followed by `CREATE MATERIALIZED VIEW` and re-creation of all indexes. This applies when the query or `WITH DATA` setting changes. Unlike regular views, materialized views do not support `CREATE OR REPLACE`.

**Index-only changes on materialized views:** When the query and `WITH DATA` setting are unchanged but indexes differ, index additions, removals, or modifications are handled individually (the materialized view itself is not rebuilt).

Materialized views are ordered after regular views in the migration file.

## Index WITH parameter migrations

When index storage parameters (the `with` field) change between schema versions, pgdesign treats it as an index change and generates `DROP INDEX` followed by `CREATE INDEX` with the new parameters. This applies regardless of the index method (btree, hash, gin, gist, brin, hnsw, ivfflat, etc.).

```toml
# Changing HNSW parameters triggers drop + recreate
[[ddl]]
op = "drop_index"
table = "public.items"
name = "idx_items_embedding"

[[ddl]]
op = "create_index"
table = "public.items"
name = "idx_items_embedding"
columns = ["embedding"]
method = "hnsw"
opclass = "vector_cosine_ops"
with = { m = "16", ef_construction = "200" }
```

The `alter_index_set` op type is available for manually authored migrations that want to use `ALTER INDEX ... SET (key = value)` to update built-in index parameters in place without rebuilding, but the automatic migration generator always uses the drop+create approach for consistency.

## Dry-run mode

Use `--dry-run` on `migrate apply` to preview the exact SQL statements that would be executed against the database without actually running them. The dry-run output shows each migration's DDL and DML statements in execution order, including transaction boundaries, advisory lock acquisition, and non-transactional operation handling. This is useful for reviewing generated SQL before deployment, verifying that the migration tool produces the expected statements, and sharing migration plans with team members for review.

```
pgdesign migrate apply --dry-run --db "postgres://user:pass@localhost/mydb"
```

## Lock timeout configuration

Lock timeout is configurable in `pgdesign.toml` and controls how long each migration waits to acquire a PostgreSQL lock before failing. This prevents migrations from blocking indefinitely when other transactions hold conflicting locks on the target tables. The timeout is applied via SET lock_timeout before each migration executes, and if a lock cannot be acquired within the configured duration, the migration fails immediately rather than queuing behind other transactions.

```toml
[migrate]
lock_timeout = "5s"
```

The default is `5s`. This is set via `SET lock_timeout` before each migration executes. If a lock cannot be acquired within this time, the migration fails rather than waiting indefinitely.

## Non-transactional operations

Some PostgreSQL operations cannot run inside a transaction block and must be executed as standalone statements. pgdesign automatically detects these non-transactional operations during migration execution, commits the current transaction before the operation, executes it outside any transaction context, then starts a new transaction for subsequent operations. This handling is transparent and requires no manual intervention, ensuring that migrations containing a mix of transactional and non-transactional operations execute correctly.

- `CREATE INDEX CONCURRENTLY`
- `DROP INDEX CONCURRENTLY`
- `ALTER TYPE ADD VALUE` (adding enum values)

pgdesign handles these by committing the current transaction before the non-transactional operation, executing it, then starting a new transaction for subsequent operations.

## Migration tracking

Applied migrations are tracked in the `pgdesign_migrations` table, which pgdesign creates automatically on first use. Each row records the migration version, when it was applied, a SHA-256 checksum of the migration file for tampering detection, and an auto-generated description. An advisory lock using `pg_try_advisory_lock` prevents concurrent migration execution across multiple processes or application instances connecting to the same database.

| Column | Type | Description |
|--------|------|-------------|
| `version` | text (PK) | Semver version string |
| `applied_at` | timestamptz | When the migration was applied |
| `checksum` | text | SHA-256 of the migration file |
| `description` | text | Auto-generated description |

An advisory lock (`pg_try_advisory_lock`) prevents concurrent migration execution. If another migration process is running, the command fails immediately rather than waiting.
