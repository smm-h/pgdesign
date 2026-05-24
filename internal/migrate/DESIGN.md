# internal/migrate

Migration system. Generates, applies, and rolls back schema migrations.

## Migration file format

Files live in `migrations/` directory. Named by semver: `0.1.0.toml`, `0.2.0.toml`. Sorted by semver for execution order.

```toml
description = "Add game_like table and player level"

[[ddl]]
op = "create_table"
table = "game.game_like"
columns = { player_id = "ref", game_id = "ref", created_at = "timestamp" }
pk = ["player_id", "game_id"]
comment = "Player likes on games"
down = { op = "drop_table", table = "game.game_like" }

[[ddl]]
op = "add_column"
table = "game.players"
column = "level"
type = "integer"
default = 1
not_null = true
down = { op = "drop_column", table = "game.players", column = "level" }

[[ddl]]
op = "create_index_concurrently"
table = "game.game_like"
name = "idx_game_like_game_id"
columns = ["game_id"]
down = { op = "drop_index_concurrently", name = "idx_game_like_game_id" }

[[dml]]
op = "backfill"
sql = "UPDATE game.players SET level = 1 WHERE level IS NULL"
down = { irreversible = true }
```

## DDL op types

create_table, drop_table, add_column, drop_column, alter_column_type, set_not_null, drop_not_null, alter_column_default, drop_column_default, rename_column, rename_table, add_fk, drop_fk, add_index, drop_index, create_index_concurrently, drop_index_concurrently, add_unique, drop_unique, add_check, drop_check, create_enum, alter_enum_add_value, drop_enum, set_owner.

## DML op types

backfill (raw SQL), transform (bulk UPDATE with WHERE).

## Down ops

Every op MUST have a `down` field. Two forms:
- Structured: `down = { op = "drop_column", table = "...", column = "..." }` (or array of ops)
- Irreversible: `down = { irreversible = true }` -- blocks rollback

Auto-generation: when `migrate generate` produces a migration file, it auto-populates obvious down ops:
- create_table -> drop_table
- add_column -> drop_column
- add_index -> drop_index
- add_fk -> drop_fk
- add_unique -> drop_unique
- add_check -> drop_check
- create_enum -> drop_enum

Complex cases (alter_column_type, DML) get `down = { irreversible = true }` by default. User can override.

## Migration generation

`Generate(diff *diff.SchemaDiff, schema *model.Schema) (*Migration, []Diagnostic)`

1. Convert each diff change to the appropriate op
2. Order ops: creates before adds, drops after removes
3. Auto-generate down ops
4. Run safety linter (internal/risk) on each op. Emit diagnostics for dangerous ops.
5. Detect DML needs (NOT NULL without default on non-empty table, column rename, type narrowing). Generate [[dml]] placeholders.
6. If DML is needed, add expand-contract steps (add nullable -> backfill -> set_not_null) instead of direct NOT NULL addition.
7. Write migration file to migrations/ dir with next semver version.

## State tracking

Table: `pgdesign_migrations` in the target database.

```sql
CREATE TABLE IF NOT EXISTS pgdesign_migrations (
    version text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now(),
    checksum text NOT NULL,
    description text
);
```

Checksum = SHA256 of migration file contents. Detects tampering.

## Apply

`Apply(conn *pgx.Conn, migrationsDir string) ([]AppliedMigration, error)`

1. Acquire session-level advisory lock: `SELECT pg_try_advisory_lock(hashtext('pgdesign_migrate'))`. If fails: error "another migration in progress." Session-level lock survives transaction boundaries (needed because non-transactional ops commit mid-migration). Released explicitly at end of apply, or automatically on disconnect.
2. Read pgdesign_migrations state. Determine pending migrations (version > max applied, sorted by semver).
3. For each pending migration:
   a. Begin transaction (for transactional ops)
   b. Execute ops in declaration order
   c. Non-transactional ops (CONCURRENTLY, ALTER TYPE ADD VALUE): commit current transaction, execute outside, begin new transaction for remaining ops
   d. Insert into pgdesign_migrations
   e. Commit transaction
4. If any op fails: transaction rollback. State unchanged for that migration. Report which op failed.
5. Release advisory lock (automatic on transaction end).

## Rollback

`Rollback(conn *pgx.Conn, migrationsDir string) error`

1. Acquire session-level advisory lock (`pg_try_advisory_lock`).
2. Read most recent applied migration version from pgdesign_migrations.
3. Load that migration file. Check for irreversible ops -- if any, block with error.
4. Execute down ops in REVERSE declaration order.
5. Delete the version row from pgdesign_migrations.
6. Commit.

## Safety linter

Each op is classified via internal/risk.Classify(). Diagnostics emitted:
- DANGEROUS ops: error-level diagnostic (blocks apply unless --force)
- CAUTION ops: warning-level diagnostic with suggestion
- Large table context: if connected to DB, query pg_stat_user_tables for row estimates

Suggestions:
- create_index -> "Consider CONCURRENTLY" (auto-converts if table has >10K rows)
- add_fk -> "Add with NOT VALID + separate VALIDATE" (auto-splits if table has >10K rows)
- add_column NOT NULL -> "Expand-contract pattern" (auto-generates multi-step)

## Expand-contract planner

For operations that need multi-step execution on large tables:

Column rename (game.players.name -> game.players.username):
1. add_column username (nullable)
2. backfill: UPDATE SET username = name
3. set_not_null on username
4. drop_column name

Type change (text -> integer):
1. add_column new_col (target type, nullable)
2. backfill: UPDATE SET new_col = old_col::integer
3. set_not_null (if original was NOT NULL)
4. drop_column old_col
5. rename_column new_col -> old_col

Generated as a single migration file with all steps in order.

## Plan command

`pgdesign migrate plan <file> --db <url>` -- Like `terraform plan`. Diffs desired vs live, computes migration ops (including expand-contract transformations), prints the full plan without writing any files. Subsumes what diff/ shows but adds the migration-specific logic (DML detection, expand-contract, safety warnings).

## Expand-contract for large tables

When estimated table size exceeds a configurable threshold (default 10M rows from pgdesign.toml `[migrate].expand_contract_threshold`), expand-contract steps are generated as SEPARATE migration files rather than a single file. This gives independent atomicity to each step:
- `0.2.0.toml` -- add nullable column
- `0.2.1.toml` -- backfill DML
- `0.2.2.toml` -- set NOT NULL + drop old column

For tables below the threshold, all steps remain in one file.

## Dry-run

`--dry-run` on apply: executes all ops in a transaction, then ROLLBACK instead of COMMIT. Prints what would have been applied. For non-transactional ops (CONCURRENTLY): skip execution, print as "would execute."

## lock_timeout

All migration transactions set `SET lock_timeout = '5s'` at the start. If any DDL can't acquire its lock within 5s, the statement fails (and the transaction rolls back). Configurable via pgdesign.toml.
