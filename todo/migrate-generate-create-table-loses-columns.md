# migrate generate: create_table ops lose all column definitions on serialization

## Context

`pgdesign migrate generate --db <empty-db> schema.toml` against a database that
does not yet contain the schema's tables produces a migration file whose
`create_table` ops have **no column definitions**. Applying that file to a
fresh database creates empty tables (`CREATE TABLE x ();`), after which the
subsequent `create_index` / `add_fk` ops fail with `column ... does not exist`
(SQLSTATE 42703).

Reproduction (pgdesign v0.24.0):

```
createdb scratch
pgdesign migrate generate --dir migrations --db "host=/var/run/postgresql dbname=scratch user=$USER" --version 0.1.0 schema.toml
psql -d scratch -c "CREATE SCHEMA myschema"
pgdesign migrate apply --dir migrations --db "host=/var/run/postgresql dbname=scratch user=$USER"
# -> error: migration 0.1.0: phase contract: DDL op 0 (create_index):
#    ERROR: column "email" does not exist (SQLSTATE 42703)
# -> \d shows all tables created but with zero columns
```

## Problem

The round trip generate -> write file -> parse file -> apply is lossy for
newly added tables:

- `internal/migrate/generate.go` (~line 300, `TablesAdded` loop) builds the
  `create_table` op with the full table definition attached as
  `DDLOp.TableDef`.
- `internal/migrate/migration.go:65` documents `TableDef` as "full table def
  for create_table (**not serialized**)".
- `internal/migrate/parse_migration.go` `WriteMigrationFile`/`writeDDLOp`
  serialize only the scalar op fields (op, table, comment, pk, phase, down),
  so the columns never reach the file.
- On apply, `internal/migrate/sql_gen.go` `opCreateTable` finds
  `op.TableDef == nil` and `len(op.ConsolidatedOps) == 0` and falls back to
  `CREATE TABLE %s ();` (sql_gen.go:177).

The in-memory path (plan, and the lint/risk pass inside generate) sees the
full definition, so nothing warns at generate time. The failure only appears
when the written file is applied -- on a fresh database, typically the very
environment bootstrap the migration was generated for.

Note the file format already supports a complete representation: the squash
consolidation path serializes `[[ddl.consolidated]]` add_column/add_fk/index
ops under a `create_table` op, and `opCreateTableConsolidated` rebuilds a full
`CREATE TABLE` from them. `migrate generate` just never populates it.

## Possible solutions

1. **Populate `ConsolidatedOps` at generate time** (recommended). In the
   `TablesAdded` loop, derive `add_column` consolidated ops (plus PK already
   carried by `pk = [...]`) from the table definition, exactly as squash
   consolidation does. Serialization and apply already handle this format;
   `opCreateTable` keeps preferring `TableDef` for the in-memory path.
   - Pros: smallest change; reuses tested consolidation SQL generation;
     file format unchanged (already parseable).
   - Cons: identity/generated column coverage in the consolidated path needs
     verification.
2. **Serialize `TableDef` itself** into the migration file (new TOML shape
   under the `create_table` op).
   - Pros: single source of truth for the table shape.
   - Cons: new file format surface; parser + writer + docs churn.
3. **Hard error** in `WriteMigrationFile` when a `create_table` op has a
   `TableDef` that cannot be represented in the file.
   - Pros: converts silent data loss into a loud failure (no silent
     degradation).
   - Cons: makes fresh-table migrations unusable until 1 or 2 lands; only a
     stopgap, but worth adding as a guard regardless of which fix is chosen.

A red-green regression test should cover the full round trip: generate against
an empty database, write the file, re-parse it, apply to a fresh database, and
assert the created tables have their columns.

## Affected files

- `internal/migrate/generate.go` (TablesAdded loop, ~line 296)
- `internal/migrate/migration.go` (TableDef field contract)
- `internal/migrate/parse_migration.go` (writeDDLOp / WriteMigrationFile)
- `internal/migrate/sql_gen.go` (opCreateTable fallback, line ~164-178)

## Effort estimate

Small-to-medium: ~30-60 lines for option 1 plus a round-trip integration test
against a live PG (the repo already has live-DB test infrastructure).
