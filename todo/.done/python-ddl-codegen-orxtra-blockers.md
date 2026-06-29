# Python DDL codegen blockers for orxtra migration

orxtra is ready to replace hand-maintained `_schema.py` files with pgdesign-generated DDL. Two issues block the migration.

## 1. String default double-quoting

When a TOML column has `default = "'internal'"` (SQL-quoted string literal), the generated Python DDL emits `DEFAULT '''internal'''` — pgdesign wraps the already-quoted value in another layer of SQL quotes.

Reproduction: `schema/trace.toml` column `events.source` has `default = "'internal'"`. Running `pgdesign codegen schema/ --lang python --mode ddl` produces `source text NOT NULL DEFAULT '''internal'''` instead of `source text NOT NULL DEFAULT 'internal'`.

Expected: the TOML `default` value should be emitted verbatim as a SQL expression, not re-quoted. The value `'internal'` is already valid SQL.

## 2. No per-schema-file output split

`pgdesign codegen schema/ --lang python --mode ddl` produces a single merged file covering all TOML files in the directory. orxtra has two schema owners (trace: 16 tables, dispatch: 4 tables) and needs separate generated files so each module owns its own DDL.

Generating dispatch.toml alone fails because it uses custom types defined in trace.toml. So per-file generation requires pgdesign to load all TOML files for type resolution but emit output split by source file.

Possible approaches:
- A `--split-by-file` flag that produces one output per input TOML
- A `--filter` flag that includes only tables from a specific TOML file in the output
- Output to a directory with one `.py` per source TOML (`trace_ddl.py`, `dispatch_ddl.py`)

## Context

orxtra (`smm-h/orxtra`) has 20 PG tables across 2 schema files. The hand-maintained `_schema.py` files have 5 remaining divergences from the TOML (enum types, default values, FK ON DELETE, index coverage, immutability mechanism). All 5 are resolved in the generated output — these two tooling issues are the only blockers for full migration.
