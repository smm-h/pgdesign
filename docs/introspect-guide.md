---
title: "Introspect Guide"
description: "Reverse-engineer a running PostgreSQL database into pgdesign.toml format with extension discovery and the introspect-review-diff adoption workflow."
---

# Introspect Guide

pgdesign's `introspect` command reads a live PostgreSQL database's schema via `pg_catalog` and produces TOML output in pgdesign's schema format. It is the primary tool for adopting pgdesign on an existing database.

:-: ref path="internal/introspect" lang="go"

## What introspect does

`introspect` connects to a running PostgreSQL database, queries the system catalogs (`pg_class`, `pg_attribute`, `pg_constraint`, `pg_type`, `pg_proc`, `pg_policy`, `pg_trigger`, `pg_views`, `pg_matviews`, `pg_sequence`, `pg_partitioned_table`, etc.), and builds pgdesign's internal model from the live state. It then serializes that model to TOML, producing output that can be used directly as a `pgdesign.toml` schema file or as a starting point for one.

The introspected schema includes:

- **Tables** with columns, primary keys, foreign keys, indexes, unique constraints, check constraints, exclusion constraints, triggers, and RLS policies
- **Enums** with their values and comments
- **Composite types** with their fields
- **Domains** (scalar types) with base types, check constraints, defaults, and NOT NULL
- **Views** with query definitions and dependency tracking
- **Materialized views** with queries, indexes, and `with_data` state
- **Sequences** (standalone, excluding identity-backed sequences)
- **Functions** with language, body, args, volatility, parallel safety, cost, and rows
- **Partition metadata** including strategy, key columns, and child partitions

### What introspect filters out

pgdesign's own managed objects are excluded from introspection output so they do not appear as user schema. Three naming-pattern filters automatically remove migration tracking infrastructure (the `pgdesign_` prefix on tables, views, and materialized views) and state machine enforcement triggers (the `_pgdesign_sm_` prefix on functions):

- Tables, views, and materialized views with a `pgdesign_` prefix (migration tracking infrastructure)
- Functions with a `_pgdesign_sm_` prefix (state machine enforcement triggers) or named `pgdesign_deny_mutation` (append-only trigger function)
- Triggers backed by any of the above managed functions

When a user-defined object collides with the reserved `pgdesign_` naming pattern, introspect filters it out and emits an I201 warning diagnostic so the exclusion is visible.

## When to use introspect

The primary use case is **adopting pgdesign for an existing database**. Instead of hand-writing a pgdesign.toml from scratch to match your production schema, introspect reads the live database and generates the TOML representation automatically. This eliminates transcription errors and gives you a baseline that is guaranteed to reflect the actual database state.

Other situations where introspect is useful:

- **Auditing drift.** Run introspect against a database and diff the output against your pgdesign.toml to detect changes made outside the migration system (manual DDL, ad-hoc ALTER statements)
- **Bootstrapping migration chains.** After introspecting an existing database, use `migrate baseline` to adopt the database onto the migration chain without executing any SQL
- **Documenting a database.** Generate a TOML representation of a database for human review, even if you do not plan to manage it with pgdesign going forward

## Connection setup

Introspect requires a PostgreSQL connection URL, which can be passed via the `--db` flag or the `PGDESIGN_DB` environment variable. The connection URL follows standard PostgreSQL `libpq` format with support for SSL and search_path parameters.

```
pgdesign introspect --db "postgres://user:password@localhost:5432/mydb"
```

Or using the environment variable:

```
export PGDESIGN_DB="postgres://user:password@localhost:5432/mydb"
pgdesign introspect
```

The connection URL follows standard PostgreSQL `libpq` format. Query parameters like `sslmode=disable` or `search_path=myschema` are supported.

### Schema selection

By default, introspect reads the `public` schema. Use the `--schema` flag (repeatable) to introspect other schemas or multiple schemas at once. When multiple schemas are specified, all objects from all schemas appear in a single TOML output.

```
# Introspect only the "core" schema
pgdesign introspect --db "..." --schema core

# Introspect multiple schemas into a single TOML
pgdesign introspect --db "..." --schema public --schema billing --schema audit
```

When a single schema is specified, it becomes the `meta.schema` value in the output. When multiple schemas are specified, all objects from all schemas are merged into one TOML document.

### Writing output to a file

By default, introspect prints TOML to stdout. Use `--output` to write directly to a file, which is the typical workflow when adopting an existing database into pgdesign. When `--output` is used, introspect also prints an adoption note to stderr explaining that the file is a candidate schema source:

```
pgdesign introspect --db "..." --output pgdesign.toml
```

When `--output` is used, introspect prints an adoption note to stderr reminding you that the output is a candidate schema source (not a derived artifact) and that adopting it as your project source changes the project revision.

## Extension discovery with --extensions

The `--extensions` flag queries `pg_extension` for installed extensions (excluding `plpgsql`, which is always present) and prints their provided types, operator classes, and functions. This is separate from the main TOML output and is appended after it.

```
pgdesign introspect --db "..." --extensions
```

The extension output uses pgdesign's `[[extensions]]` array-of-tables format:

```toml
[[extensions]]
name = "pgcrypto"
functions = ["gen_random_uuid", "gen_salt", "crypt"]

[[extensions]]
name = "pgvector"
types = ["vector", "halfvec", "sparsevec"]
opclasses = ["vector_cosine_ops", "vector_l2_ops", "vector_ip_ops"]
```

This output can be merged into your `pgdesign.toml` under the `[[extensions]]` section. Extension declarations are required for pgdesign to recognize extension-provided types (like `vector` from pgvector) as valid base types -- without the declaration, columns using those types produce hard errors during schema validation.

## How introspected output maps to pgdesign.toml

The export produces a complete pgdesign TOML document covering 10 object categories. Each PostgreSQL catalog object maps to a specific TOML section with field-level fidelity. Here is how database objects map to TOML sections:

| Database object | TOML section | Notes |
|---|---|---|
| `[meta]` header | `[meta]` | `version = 1`, schema name if single schema |
| Enum types | `[types.NAME]` with `kind = "enum"` | Values array, optional comment |
| Composite types | `[types.NAME]` with `kind = "composite"` | Fields as `[types.NAME.fields]` sub-table |
| Domains | `[types.NAME]` with `kind = "scalar"` | `base_type`, `check`, `default`, `not_null` |
| Tables | `[tables.NAME]` | `comment`, `pk`, `enable_rls`, `force_rls` |
| Columns | `[tables.NAME.columns.COL]` | `type`, `nullable`, `default`, `identity`, `generated`, `array`, `collation`, `statistics` |
| Foreign keys | `[tables.NAME.fks.FK]` | `columns`, `ref_table`, `ref_columns`, `on_delete` |
| Indexes | `[tables.NAME.indexes.IDX]` | `columns` (with DESC suffix), `method`, `where`, `include`, `opclass`, `with` |
| Unique constraints | `[tables.NAME.unique.UQ]` | `columns` |
| Check constraints | `[tables.NAME.checks.CK]` | `expr` (stripped of `CHECK (...)` wrapper) |
| Exclusion constraints | `[tables.NAME.exclusions.EX]` | `columns`, `operators`, `method`, `where`, `deferrable` |
| Triggers | `[tables.NAME.triggers.TRG]` | `function`, `events`, `timing`, `for_each`, `when`, `constraint` |
| RLS policies | `[tables.NAME.policies.POL]` | `type`, `for`, `to`, `using`, `with_check` |
| Partitioning | via table attributes | `strategy`, `columns`, child partitions |
| Views | `[views.NAME]` | `query`, `comment`, `depends_on` |
| Materialized views | `[materialized_views.NAME]` | `query`, `comment`, `with_data`, `depends_on`, indexes |
| Sequences | `[sequences.NAME]` | `start`, `increment`, `min_value`, `max_value`, `cache`, `cycle`, `owned_by` |
| Functions | `[functions.NAME]` | `language`, `return_type` (as `returns`), `body`, `volatility`, `parallel`, `security_definer`, `cost`, `rows`, args as sub-tables |

### Column type handling

Introspect uses PostgreSQL's `format_type()` to read column types from the catalog and normalizes them through pgdesign's type system. The normalization ensures round-trip fidelity between introspected output and pgdesign's own build process, handling 5 special cases -- arrays, domains, enums, defaults, and identity columns:

- **Array columns**: `text[]` becomes `type = "text"` with `array = true`
- **Domain-backed columns**: a column whose type is a domain gets its `type` set to the domain's underlying base type and a `DomainName` reference to the domain (matching what the TOML build produces)
- **Enum columns**: columns whose type matches an introspected enum get `TypeKind = "enum"`
- **Defaults**: simple literals (`42`, `true`, `'hello'`) go into `default`; complex expressions (`gen_random_uuid()`, `now()`) go into `default_expr`
- **Identity columns**: `GENERATED ALWAYS AS IDENTITY` and `GENERATED BY DEFAULT AS IDENTITY` are captured in the `identity` field; the implicit `nextval()` default is suppressed

### What introspect cannot recover

Five categories of pgdesign concepts do not exist in `pg_catalog` and therefore cannot be round-tripped from a live database. These are pgdesign-specific abstractions that PostgreSQL has no catalog representation for. They must be added manually to the introspected TOML during the review-and-refine step:

- **State machine type kind**: PostgreSQL stores state machines as plain enums (`typtype = 'e'`). Introspect recovers them as enums; the `kind = "state_machine"` distinction must be re-added manually
- **`json_schema` references**: pgdesign lowers these to CHECK constraints. Introspect recovers the CHECK but not the originating JSON Schema file reference
- **RLS `error_code` and `error_message`**: these are pgdesign-only metadata not stored in `pg_catalog`
- **Semantic type names**: a column typed as the `email` semantic type appears as `type = "text"` with the domain's CHECK constraint. The semantic type name is not recoverable
- **`append_only` flag**: introspect detects the `pgdesign_deny_mutation` trigger and sets the flag, but only for tables managed by pgdesign's own append-only machinery

## Common workflow: introspect, review, diff, iterate

### 1. Introspect the live database

```
pgdesign introspect --db "postgres://..." --output schema-introspected.toml --extensions
```

This gives you a TOML file reflecting the current database state, plus extension declarations printed to stdout (copy these into the file's `[meta]` section or as `[[extensions]]` entries).

### 2. Review and refine

The introspected TOML is syntactically valid and reflects the live database state, but it may need refinement to add pgdesign-specific concepts that the catalog cannot recover. Most databases require at least 2-3 of the following changes before the TOML passes validation:

- Add table comments where they are missing (pgdesign requires comments on all tables)
- Add `[[extensions]]` declarations for any extensions discovered with `--extensions`
- Restore semantic type names if your schema uses pgdesign's type system
- Add `kind = "state_machine"` to enum types that are actually state machines, with their `transitions` and `initial` state
- Add `json_schema` references for JSONB columns that had them
- Add `error_code` and `error_message` to RLS policies if needed
- Review `on_delete` actions on foreign keys for correctness

### 3. Diff against the database

After refining, use `pgdesign diff --live` to compare your refined TOML against the running database and verify that your changes did not introduce drift. The goal is a clean diff with zero differences -- any reported change means the TOML does not yet match the live schema:

```
pgdesign diff --live --db "postgres://..."
```

This shows any differences between your TOML and the live schema. The goal is zero drift: the TOML should produce the same DDL as the existing database.

### 4. Iterate until clean

Fix differences found by diff, re-run, and repeat until the diff reports zero changes. Most databases converge within 2-4 iterations. The majority of adjustments involve expression normalization differences or default value formatting, not structural schema mismatches. Common adjustments include:

- Expression normalization differences (PostgreSQL's `pg_get_expr` returns a canonical form that may differ from what you wrote)
- Default value formatting (e.g., `'2024-01-01'::date` vs `'2024-01-01'`)
- Index column order or naming

### 5. Adopt onto the migration chain

Once the diff is clean (zero differences between your TOML and the live database), use `migrate baseline` to stamp the database's current state as the migration chain's starting revision. This creates a rollback-frozen boundary -- all future schema changes go through pgdesign's TOML-first workflow from this point forward:

```
pgdesign migrate baseline schema.toml --db "postgres://..."
```

From this point forward, schema changes go through pgdesign's TOML-first workflow: edit the TOML, generate migrations, apply them.

## Flags reference

| Flag | Type | Default | Description |
|---|---|---|---|
| `--db` | string | `PGDESIGN_DB` env | PostgreSQL connection URL |
| `--schema` | string (repeatable) | `public` | PostgreSQL schema name(s) to introspect |
| `--output` | string | stdout | Write TOML output to this file path |
| `--extensions` | bool | `false` | Discover installed extensions with their types, functions, and opclasses |
