---
title: "Migration Guide"
description: "Guide to pgdesign's content-addressed migration chain: generating edges, path-finder apply, journal-driven rollback, squash, rebase, and baseline."
---

# Migration Guide

pgdesign generates migrations by diffing your TOML schema against the last recorded schema state. A migration is a content-addressed **chain edge** — a self-contained transition between two schema revisions whose operations and object payloads live in the on-disk store. Edges carry DDL and DML operations, per-operation inverses (for rollback), and safety diagnostics.

## The migration chain

A chain-format project keeps all migration state under `migrations/` in 5 subdirectories, separating live edges, archived history, content-addressed objects, revision manifests, and rebase mappings. These files are committed to the repository and fully self-contained -- no external state is required to reconstruct the migration graph:

| Directory | Contents |
|-----------|----------|
| `migrations/chain/` | one file per LIVE edge (the current history) |
| `migrations/archive/` | retired originals (superseded by squash, or rebased away) |
| `migrations/objects/` | the content-addressed object store (every object and op payload) |
| `migrations/revisions/` | one manifest per revision (a map of object key to content id) |
| `migrations/remap.json` | the rebase revision-remap table (present only after a `migrate rebase`) |

Each edge file is named by its content hash and its parent/target revisions. Because identity is content-derived, regenerating the same schema change always produces the same edge — git never sees spurious churn, and two branches that make different changes produce distinct edges (a fork, resolved with `migrate rebase`).

Applied history is recorded in the database, not inferred from files: the `pgdesign_chain_position` row tracks which revision a database is at, and the `pgdesign_migration_ops` journal records each applied operation with its recorded inverse. `migrate rollback` reads that journal — it never trusts or re-reads the on-disk files.

An edge's operations are drawn from the same DDL and DML op inventory used throughout the tool.

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

Generate a new chain edge by comparing your TOML schema against the current chain head. Generation is pure: it reads only the on-disk chain (the head revision's reconstructed model), never a database, so the same schema edit always yields the same edge regardless of any database's state.

It computes the structural diff, classifies each change by risk level, records each operation's inverse, and writes the edge, its object payloads, and its target manifest into the store. Large-table-safe forms (NOT VALID + VALIDATE, backfill-then-set-not-null, expand/contract phasing) are always emitted, since a manifest carries no row counts.

```
pgdesign migrate generate schema.toml
```

| Flag | Description |
|------|-------------|
| `--dir` | Migrations directory (default: `migrations/`) |

The edge is written under `migrations/chain/` with a content-derived filename. If a plausible rename is detected but not declared in a `[renames]` section, generation is refused, naming the pair.

### migrate plan

Preview the migration operations that would be generated without writing any files to disk. This command performs the same schema diff and risk classification as `migrate generate` but displays the results in the terminal instead of creating a migration file. Use this to review what changes pgdesign detects before committing to a migration version, verify that expected changes are captured, and check risk levels and safety diagnostics.

```
pgdesign migrate plan schema.toml --db "postgres://user:pass@localhost/mydb"
```

Shows the list of operations, risk classifications, and safety diagnostics.

### migrate apply

Apply pending edges to bring the target database from its recorded chain position to the single live head. The path-finder walks the edge graph (live plus archive) from the database's `pgdesign_chain_position` to the head -- order is determined by graph topology, not version strings.

Each edge runs inside its own transaction with advisory locking to prevent concurrent execution; non-transactional operations like CREATE INDEX CONCURRENTLY and ALTER TYPE ADD VALUE are detected and executed outside transactions. The chain tracking structures are created automatically on a fresh database. After applying, a reconcile step introspects the database and verifies it matches the target model.

```
pgdesign migrate apply --db "postgres://user:pass@localhost/mydb"
```

| Flag | Description |
|------|-------------|
| `--dir` | Migrations directory (default: `migrations/`) |
| `--dry-run` | Show SQL without executing |

If the path-finder finds more than one reachable head, apply refuses with a fork error pointing at `migrate rebase`. A database at a rebased-away revision is served forward via `migrations/remap.json`, never orphaned. An advisory lock prevents concurrent execution.

### migrate rollback

Roll back applied edges by executing the recorded inverses from the journal (`pgdesign_migration_ops`) in reverse application order. Rollback reads the database's own record of what was applied, never the on-disk edge files, ensuring edited files cannot mislead a rollback.

It acquires an advisory lock and, before executing any step, verifies that every operation in the rollback range is reversible. If any operation is non-invertible, the rollback is refused with a clear error identifying the blocking operation. Rollback stops at the upgrade/baseline boundary: it cannot cross a frozen boundary revision.

```
pgdesign migrate rollback --db "postgres://user:pass@localhost/mydb"
```

| Flag | Description |
|------|-------------|
| `--dir` | Migrations directory (default: `migrations/`) |
| `--to` | Target revision to roll back to (resolved via the journal and remap) |

If any operation in the range is non-invertible, the rollback is refused.

### migrate status

Show a database's chain position and the edges still pending to reach the live head. The command reads `pgdesign_chain_position` and asks the path-finder for the remaining edges, displaying the current revision, the head revision, and each pending edge. This is useful for verifying a database's state before applying new edges or diagnosing a fork.

```
pgdesign migrate status --db "postgres://user:pass@localhost/mydb"
```

### migrate squash

Consolidate a range of sequential edges into a single **consolidation edge** whose op-list is the ordered concatenation of the range. Squash is never a rewrite: it mints an additional edge from the range's start revision to its end revision, and retires the superseded originals INTACT to `migrations/archive/`. A database mid-range resumes through the path-finder by walking the archived originals, so squashing is legal regardless of applied state. `--from`/`--to` are revision-or-edge references (`genesis`, a revision string, or a live edge-id prefix).

```
pgdesign migrate squash --from genesis --to <edge-id> --db "postgres://user:pass@localhost/mydb"
```

| Flag | Description |
|------|-------------|
| `--from` | Start of the range (revision-or-edge reference) |
| `--to` | End of the range (revision-or-edge reference) |
| `--dir` | Migrations directory (default: `migrations/`) |
| `--db` | Connection URL (the pre-upgrade guard runs against it) |

The consolidation edge and its archived originals coexist; the path-finder prefers the consolidation edge as a shorter route to the head. Because originals are archived intact, squashing never invalidates a database that has already applied part of the range.

### migrate rebase

Resolve a two-head fork. When two branches each append an edge to the same parent, the chain has 2 live heads and apply refuses until the fork is resolved. `migrate rebase --head <ref>` re-parents the tail of the other head onto the named head.

It re-simulates each re-parented edge's operations to recompute its revision and content-derived edge file. The rebased-away originals retire intact to `migrations/archive/`, and the rebase revision-remap table (`migrations/remap.json`) is written so a database stamped at a rebased-away revision is served forward to the new head. A pure file operation -- no database required.

```
pgdesign migrate rebase --head <revision-or-edge-ref>
```

### migrate upgrade

One-time adoption of a legacy database (the single legacy tracking table, no chain position) onto the on-disk chain. It verifies the schema matches the live database exactly (refusing to stamp over drift), folds the existing applied history into the chain journal, writes the content-addressed genesis prefix edge, and stamps this database's upgrade boundary in a single transaction. Run once per database; a fresh database uses `migrate apply` directly.

```
pgdesign migrate upgrade schema.toml --db "postgres://user:pass@localhost/mydb"
```

### migrate baseline

Adopt an existing or intentionally-drifted database onto the chain without executing any migration SQL. In chain mode it introspects the live database, synthesizes a genesis edge carrying the introspected manifest, and stamps a baseline boundary (rollback-frozen). Use this for a database whose schema was created by other means, or one that has drifted from the schema — `migrate upgrade` refuses drift; `migrate baseline` adopts it.

```
pgdesign migrate baseline schema.toml --db "postgres://user:pass@localhost/mydb"
```

### migrate test

Test migrations against a staging database to verify they apply and roll back cleanly before deploying to production. The test command applies the pending edges, then rolls them back, verifying that every edge applies without errors, all reversible edges roll back cleanly, and the database returns to its original state after the full rollback cycle. With --shadow mode, the command replays the chain EDGES into a fresh shadow database (chain-format projects) and diffs the result against the TOML schema.

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

Every DDL operation in a generated migration is assigned 1 of 3 risk levels (Safe, Caution, Dangerous) based on the type of schema change, the PostgreSQL lock it requires, and the estimated size of the affected table. This classification helps teams assess the impact of migrations before applying them to production databases. Risk levels are displayed in `migrate plan` output and annotated in migration files for review.

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

The `json_schema` attribute is a pgdesign-only authoring construct: it lowers to CHECK constraints, and PostgreSQL has no notion of the originating schema reference. It therefore does **not** round-trip from a live database — introspection recovers the emitted CHECKs but not the `json_schema` attribute, and post-apply reconcile excludes it from comparison (the same documented exclusion as the state-machine kind). This is deliberate, not a limitation to be fixed.

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

3 PostgreSQL operations cannot run inside a transaction block and must be executed as standalone statements. pgdesign automatically detects these non-transactional operations during migration execution, commits the current transaction before the operation, executes it outside any transaction context, then starts a new transaction for subsequent operations. This handling is transparent and requires no manual intervention, ensuring that migrations containing a mix of transactional and non-transactional operations execute correctly.

- `CREATE INDEX CONCURRENTLY`
- `DROP INDEX CONCURRENTLY`
- `ALTER TYPE ADD VALUE` (adding enum values)

pgdesign handles these by committing the current transaction before the non-transactional operation, executing it, then starting a new transaction for subsequent operations.

## Migration tracking

A chain-format database carries 3 managed structures (1 position table, 1 journal table, 1 convenience view), created automatically on first `migrate apply`. These structures record what the database has applied, enabling rollback and status queries without trusting on-disk files:

| Structure | Role |
|-----------|------|
| `pgdesign_chain_position` | this database's position: its current revision, the upgrade/baseline boundary, and any in-progress edge |
| `pgdesign_migration_ops` | the per-operation journal: each applied operation with its recorded inverse and intent/confirm status |
| `pgdesign_applied_migrations` | a view over the journal presenting fully-confirmed edges (version label, applied timestamp, description, checksum) |

Rollback reads the journal's recorded inverses; it never re-reads the on-disk edge files. An advisory lock using `pg_try_advisory_lock` prevents concurrent migration execution across processes connecting to the same database — if another migration is running, the command fails immediately rather than waiting.
