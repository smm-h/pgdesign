---
title: "Migration Guide"
description: "Guide to pgdesign's migration system: generating, planning, applying, and rolling back database migrations."
---

# Migration Guide

pgdesign generates migrations by diffing your TOML schema against a live database. Migrations are TOML files containing DDL and DML operations with rollback instructions and safety diagnostics.

## Migration file format

Migration files are TOML with `[[ddl]]` and `[[dml]]` operation arrays:

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

Each DDL or DML op can include a `down` key describing how to reverse it:

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

Generate a migration file by diffing your schema against a live database:

```
pgdesign migrate generate schema.toml --db "postgres://user:pass@localhost/mydb" --version 0.2.0
```

| Flag | Description |
|------|-------------|
| `--version` | Migration version (semver format) |
| `--dir` | Migrations directory (default: `migrations/`) |

The generated file is saved as `migrations/<version>.toml`.

### migrate plan

Preview the migration without writing a file:

```
pgdesign migrate plan schema.toml --db "postgres://user:pass@localhost/mydb"
```

Shows the list of operations, risk classifications, and safety diagnostics.

### migrate apply

Apply all pending migrations:

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

Roll back the most recently applied migration:

```
pgdesign migrate rollback --db "postgres://user:pass@localhost/mydb"
```

| Flag | Description |
|------|-------------|
| `--dir` | Migrations directory (default: `migrations/`) |

Rollback executes the `down` operations in reverse order. If any operation is marked `irreversible`, the rollback is refused.

### migrate status

Show which migrations have been applied:

```
pgdesign migrate status --db "postgres://user:pass@localhost/mydb"
```

### migrate squash

Squash a range of migrations into a single consolidated migration:

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

Test migrations against a staging database to verify they apply and roll back cleanly:

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

Every DDL operation is classified by risk level:

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

Risk is escalated based on estimated row counts (from `pg_stat_user_tables`):

- Tables with >1M rows: Caution + AccessExclusive is escalated to Dangerous
- Tables with >10M rows: lock_timeout suggestion is added

### Large FK threshold

When adding a foreign key to a table with more than 10,000 rows (configurable), pgdesign warns that `ADD CONSTRAINT` without `NOT VALID` will lock the table during validation. The recommendation is to add with `NOT VALID` first, then `VALIDATE CONSTRAINT` in a separate step.

## Expand-contract decomposition

For large tables (>10M rows by default, configurable via `expand_contract_threshold`), pgdesign automatically decomposes certain operations:

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

When a table's `append_only` attribute changes, pgdesign generates trigger-based migrations to enforce or remove immutability:

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

pgdesign generates view migrations when diffing detects view changes:

**Adding a view:** Generates `CREATE VIEW` with the full query definition.

**Removing a view:** Generates `DROP VIEW`.

**Changing a view:** Generates `CREATE OR REPLACE VIEW` with the updated query. PostgreSQL's `CREATE OR REPLACE VIEW` updates the view definition in place without dropping dependent objects, as long as the column list remains compatible.

Views are ordered after table operations in the migration file to ensure referenced tables exist.

## Materialized view migrations

Materialized views cannot be altered in place -- any change requires a full rebuild:

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

Use `--dry-run` on `migrate apply` to see the SQL that would be executed without actually running it:

```
pgdesign migrate apply --dry-run --db "postgres://user:pass@localhost/mydb"
```

## Lock timeout configuration

Lock timeout is configurable in `pgdesign.toml`:

```toml
[migrate]
lock_timeout = "5s"
```

The default is `5s`. This is set via `SET lock_timeout` before each migration executes. If a lock cannot be acquired within this time, the migration fails rather than waiting indefinitely.

## Non-transactional operations

Some PostgreSQL operations cannot run inside a transaction:

- `CREATE INDEX CONCURRENTLY`
- `DROP INDEX CONCURRENTLY`
- `ALTER TYPE ADD VALUE` (adding enum values)

pgdesign handles these by committing the current transaction before the non-transactional operation, executing it, then starting a new transaction for subsequent operations.

## Migration tracking

Applied migrations are tracked in the `pgdesign_migrations` table:

| Column | Type | Description |
|--------|------|-------------|
| `version` | text (PK) | Semver version string |
| `applied_at` | timestamptz | When the migration was applied |
| `checksum` | text | SHA-256 of the migration file |
| `description` | text | Auto-generated description |

An advisory lock (`pg_try_advisory_lock`) prevents concurrent migration execution. If another migration process is running, the command fails immediately rather than waiting.
