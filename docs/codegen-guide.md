---
title: "Code Generation Guide"
description: "How pgdesign generates type-safe application code from TOML schemas across six languages and eleven modes, with CI freshness checking."
---

# Code Generation Guide

pgdesign generates type-safe application code from your schema definitions. Instead of manually writing struct definitions, ORM models, enum types, or validation logic that mirrors your database schema, you run `pgdesign codegen` and get generated source files that stay in sync with your TOML schema.

Source: `internal/codegen/`, `cmd/pgdesign/handlers_codegen.go`, `cmd/pgdesign/codegen_registry.go`

## Supported Languages and Modes

pgdesign supports 6 target languages (Go, TypeScript, Python, Java, Kotlin, Zig) and 11 generation modes. Not every mode is available for every language -- the table below shows which of the 66 possible combinations are supported, ranging from universal modes like validators and constants to language-specific ORM integrations.

| Mode | Go | TypeScript | Python | Java | Kotlin | Zig | Description |
|------|:--:|:----------:|:------:|:----:|:------:|:---:|-------------|
| `validators` | x | x | x | x | x | x | RLS policy pre-check functions |
| `constants` | x | x | x | x | x | x | Table/column name string constants |
| `types` | x | x | x | x | x | x | Native struct/class/interface definitions |
| `enums` | x | x | x | x | x | x | Standalone enum type definitions |
| `constraints` | x | x | x | x | x | x | Client-side validation from CHECK/NOT NULL/enum |
| `gorm` | x | | | | | | Go GORM struct tags with relationships |
| `drizzle` | | x | | | | | Drizzle ORM table/relation schema |
| `sqlalchemy` | | | x | | | | SQLAlchemy 2.0 declarative models |
| `jpa` | | | | x | | | JPA entity classes with annotations |
| `ddl` | | | x | | | | DDL as Python data tuples + executor |
| `query-layer` | | | x | | | | Protocol-based async query layer |

## Basic Usage

The `pgdesign codegen` command reads your TOML schema, resolves types and dependencies, and emits generated source code. Output goes to stdout by default, or to a file with `--output`. You specify the target language with `--lang` and the generation mode with `--mode`. Generate to stdout:

```bash
pgdesign codegen --lang go --mode types schema/
pgdesign codegen --lang python --mode constants schema/
pgdesign codegen --lang ts --mode drizzle schema.toml
```

Generate to a file:

```bash
pgdesign codegen --lang go --mode types --output gen/schema/types.go schema/
pgdesign codegen --lang python --mode sqlalchemy --output gen/models.py schema/
```

Check freshness without writing (for CI):

```bash
pgdesign codegen --lang go --mode types --output gen/schema/types.go --check schema/
```

## Mode Reference

### validators

Generates functions that pre-check RLS (Row Level Security) policy conditions before hitting the database. Only policies with an `error_code` and a recognized expression pattern produce validators. Two patterns are supported:

- **Ownership**: `column = current_setting('app.user_id')::uuid` -- generates a function that compares a column value against a context parameter.
- **Exists-lookup**: `EXISTS (SELECT 1 FROM settings WHERE ...)` -- generates a function that checks a flag in a related table.

Policies without an `error_code` are silently skipped. Policies whose expressions do not match any pattern produce a C001 diagnostic warning.

### constants

Generates string constants for every table name, column name, and column list in the schema. Useful for building queries without string literals. State machine transition maps are included as constants.

Each language uses its idiomatic naming convention:

| Language | Table constant | Column constant |
|----------|---------------|-----------------|
| Go | `TableUsers` (PascalCase) | `UsersColEmail` |
| Python | `TABLE_USERS` (UPPER_SNAKE) | `USERS_COL_EMAIL` |
| TypeScript | `TABLE_USERS` | `USERS_COL_EMAIL` |
| Java | `TABLE_USERS` | `USERS_COL_EMAIL` |
| Kotlin | `TABLE_USERS` | `USERS_COL_EMAIL` |
| Zig | `table_users` (snake_case) | `users_col_email` |

### types

Generates native struct/class/interface definitions with one type per table. Columns map to fields with the appropriate native type for the language. Enum types defined in the schema are emitted as branded enums (not raw strings). State machine transition maps are included.

Type mapping examples:

| PostgreSQL | Go | TypeScript | Python | Java | Kotlin | Zig |
|------------|-----|-----------|--------|------|--------|-----|
| `integer` | `int32` | `number` | `int` | `int` | `Int` | `i32` |
| `text` | `string` | `string` | `str` | `String` | `String` | `[]const u8` |
| `uuid` | `uuid.UUID` | `string` | `UUID` | `UUID` | `UUID` | `[16]u8` |
| `timestamptz` | `time.Time` | `Date` | `datetime` | `Instant` | `Instant` | `i64` |
| `jsonb` | `json.RawMessage` | `Record<string, unknown>` | `dict[str, Any]` | `JsonNode` | `JsonNode` | `[]const u8` |
| `boolean` | `bool` | `boolean` | `bool` | `boolean` | `Boolean` | `bool` |
| `numeric` | `string` | `string` | `Decimal` | `BigDecimal` | `BigDecimal` | `[]const u8` |
| `bytea` | `[]byte` | `Uint8Array` | `bytes` | `byte[]` | `ByteArray` | `[]const u8` |

Nullable columns are wrapped in the language's nullable pattern (`*T` in Go, `T | null` in TS, `Optional[T]` in Python, `T?` in Kotlin, `?T` in Zig). Array columns use the language's array/list type.

The `money` semantic type maps to integer cents (`int64` in Go, `int` in Python, etc.) to avoid floating-point rounding.

### enums

Generates standalone enum type definitions without the struct/class definitions that the `types` mode includes. This mode is useful when you need enum types in a separate file or package, or when your application only uses the enum values without the full table struct definitions.

Go enums are branded string types with `String()`, `IsValid()`, `MarshalJSON()`/`UnmarshalJSON()`, and `Scan()`/`Value()` methods for database interop. Python enums extend `StrEnum`. TypeScript enums use string literal union types. Java and Kotlin use standard enum classes. Zig enums are tagged unions.

### constraints

Generates client-side validation code derived from CHECK constraints, NOT NULL columns, and enum-typed columns. This mode lets you enforce the same rules in your application layer that the database enforces at the SQL level, catching invalid data before it reaches the database. The constraint generator parses CHECK expressions and classifies them into 4 patterns:

- **Range**: `column >= 0 AND column <= 100` -- generates a bounds check
- **Comparison**: `column >= 18` -- generates a comparison check
- **Length**: `LENGTH(column) <= 255` -- generates a string length check
- **LIKE/ILIKE pattern**: `column LIKE '%@%'` -- generates a pattern match

NOT NULL constraints (excluding PK, identity, and generated columns) produce required-field checks. Enum-typed columns produce valid-value checks. Domain-backed columns use the domain's CHECK constraint with the underlying base type.

### gorm (Go only)

Generates Go structs with GORM struct tags for the GORM ORM. Includes `gorm:"column:name;type:type"` tags, primary key marking, foreign key relationship fields (`BelongsTo` and `HasMany`), and unique/index annotations derived from the schema. Uses the FK graph to derive relationship fields automatically.

### drizzle (TypeScript only)

Generates Drizzle ORM schema definitions with `pgTable()` calls, column builders, index declarations, and `relations()` blocks. Imports are tracked and only the used Drizzle functions appear in the generated import block.

### sqlalchemy (Python only)

Generates SQLAlchemy 2.0 declarative models using `mapped_column()` definitions. Includes column type mappings, nullable annotations, primary key marking, and `relationship()` fields from FK metadata. Enum-typed columns map to `sa.Enum(BrandedEnumClass)` with the generated `StrEnum` as both the column type and Python type.

### jpa (Java only)

Generates JPA entity classes with `@Entity`, `@Table`, `@Id`, `@Column`, `@ManyToOne`, `@OneToMany`, and `@JoinColumn` annotations. Each table produces one entity class with fields derived from columns and relationships derived from FK metadata. Enum columns map to `@Enumerated(EnumType.STRING)` with generated Java enum classes.

### ddl (Python only)

Generates DDL statements as Python `DDLStmt` namedtuples with seven fields: `sql`, `idempotent_sql`, `kind`, `name`, `table`, `phase`, `transactional`. Includes a decomposed section executor for applying DDL programmatically. This is a multi-file generator.

The `--split-mode` flag controls file layout:

| Split mode | Output |
|-----------|--------|
| (default) | `schema_ddl.py` + `schema_executor.py` |
| `faceted` | One file per object kind (extensions, types, tables per source, post-tables) |
| `self-contained` | A single importable module |

### query-layer (Python only)

Generates a complete async query layer with Protocol definitions, Row dataclasses, a PgBackend (asyncpg), and an InMemoryBackend (for testing). This is the most comprehensive mode -- see the [Python Query Layer](query-layer.md) guide for full details.

## Configuring Codegen in pgdesign.toml

Instead of running `pgdesign codegen` manually for each language and mode combination, you can declare all codegen outputs in `pgdesign.toml`. The `build` command then reads these `[output]` sections and generates all configured outputs in one pass. Codegen outputs use `format = "codegen"` with `lang` and `mode` fields.

```toml
[project]
schemas = ["schema/"]

[output.go_types]
format = "codegen"
lang = "go"
mode = "types"
path = "gen/schema/types.go"

[output.go_constants]
format = "codegen"
lang = "go"
mode = "constants"
path = "gen/schema/constants.go"

[output.go_constraints]
format = "codegen"
lang = "go"
mode = "constraints"
path = "gen/schema/constraints.go"

[output.ts_drizzle]
format = "codegen"
lang = "ts"
mode = "drizzle"
path = "gen/drizzle/schema.ts"

[output.py_sqlalchemy]
format = "codegen"
lang = "python"
mode = "sqlalchemy"
path = "gen/models.py"

[output.py_query_layer]
format = "codegen"
lang = "python"
mode = "query-layer"
path = "gen/query_layer/"
```

Then run:

```bash
pgdesign build
```

This generates all outputs, stamps each file with a provenance header and full-project revision, and auto-commits the result.

### Output configuration fields

| Field | Required | Description |
|-------|----------|-------------|
| `format` | yes | Must be `"codegen"` for code generation outputs |
| `path` | yes | Output file path (or directory for multi-file modes), relative to project root |
| `lang` | yes | Target language: `go`, `ts`, `python`, `java`, `kotlin`, `zig` |
| `mode` | yes | Generation mode (see table above) |
| `groups` | no | Restrict to tables in these schema groups |
| `source` | no | Restrict to tables from these source file basenames |
| `split_mode` | no | For Python DDL only: `"faceted"` or `"self-contained"` |
| `backends` | no | For query-layer only: `["pg"]`, `["memory"]`, or both (default) |

### Filtering with groups and source

You can restrict which tables appear in the generated output using two filtering mechanisms: `groups` filters by schema-defined table groups declared in `[groups]`, while `source` filters by source file basenames for multi-file schemas. Both filters accept arrays and can be combined.

```toml
# Schema defines groups
# [groups]
# core = ["users", "roles"]
# orders = ["orders", "order_items"]

[output.core_types]
format = "codegen"
lang = "go"
mode = "types"
path = "gen/core/types.go"
groups = ["core"]

[output.orders_types]
format = "codegen"
lang = "go"
mode = "types"
path = "gen/orders/types.go"
groups = ["orders"]
```

### Go codegen co-location rules

Go codegen outputs emit `package schema` code. Because Go requires unique type definitions within a package, two co-location rules prevent compilation errors when configuring multiple codegen outputs in the same project:

1. **At most one struct provider per directory.** The `types` and `gorm` modes both define row struct types. Putting both in the same directory produces duplicate type definitions. Configure them in separate directories.

2. **`constraints` must co-locate with its provider.** The `constraints` mode references struct types and branded enum types by bare name (no import path). It must be in the same directory as the `types` or `gorm` output that defines those types.

## Output Structure

### Single-file modes

Most of the 11 codegen modes produce a single output file containing all generated definitions. When using `--output`, the file is written to that path. When using `build`, the file is written to the configured `path`. Single-file modes include validators, constants, types, enums, constraints, gorm, drizzle, sqlalchemy, and graphql.

Every generated file begins with a provenance header:

```
// Code generated by pgdesign. DO NOT EDIT.
// pgdesign-revision: abc123def456...
```

The comment prefix matches the target language (`//` for Go/TS/Java/Kotlin/Zig, `#` for Python).

### Multi-file modes

Three modes -- `ddl`, `query-layer`, and `jpa` -- produce multiple files via the MultiFileGenerator interface. The `--output` path (or `path` in config) specifies a directory, and the generator writes files inside it. Each mode has its own directory structure.

For `query-layer`, the output directory contains:

| File | Contents |
|------|----------|
| `__init__.py` | Barrel file re-exporting public types |
| `protocols.py` | Context types, Row dataclasses, Backend Protocol |
| `_constraints.py` | Constraint registry for InMemoryBackend |
| `_<table>_pg.py` | Per-table PgBackend delegate |
| `_<table>_mem.py` | Per-table InMemoryBackend delegate |
| `pg_backend.py` | Composite PgBackend |
| `memory_backend.py` | Composite InMemoryBackend |

### Owned directories and orphan detection

Multi-file generators own their output directory. Every file inside the directory must either be a planned output or on the ignore list (`__pycache__/` and `*.pyc`). Unexpected files are **orphans** -- a hard error that blocks writing.

Orphans appear when output configuration changes (e.g., renaming a schema source file, switching `split_mode`). The old files remain on disk and must be manually deleted before regenerating.

## Using --check in CI

The `--check` flag verifies that generated code on disk matches what would be generated from the current schema, without writing anything. This prevents stale generated code from reaching production -- a common issue when schema changes are committed without regenerating codegen outputs. It requires `--output` (the path to verify against).

```bash
pgdesign codegen --lang go --mode types --output gen/schema/types.go --check schema/
```

Exit codes:
- **0**: All files are fresh (byte-identical to what would be generated)
- **1**: At least one file is missing, stale, or orphaned

The check reports per-file status to stderr:

```
[missing] gen/schema/types.go
[stale]   gen/schema/constants.go
[fresh]   gen/schema/constraints.go
[orphan]  gen/schema/old_file.go
3 file(s): 1 missing, 1 stale, 1 fresh; 1 orphan(s)
```

### CI integration with build

For projects using `pgdesign.toml` with `[output]` sections, use `pgdesign check --tag build` instead -- it verifies all configured outputs (not just codegen) are up to date in a single command, covering SQL, D2, JSON, doc, GraphQL, and codegen outputs.

```bash
# In CI:
pgdesign check --tag build
```

The `check --tag revision` check goes further: it verifies that every output file carries the correct full-project revision stamp, catching cases where a file was regenerated from a different schema version.

### Partial-write refusal

When using `codegen --output` to write a single output, pgdesign checks whether sibling outputs (other `[output]` entries in `pgdesign.toml`) are at the same revision. If they differ, writing is refused with a message to run `pgdesign build` instead. This prevents a mixed-revision tree where different generated files were produced from different schema versions.

## Provenance Stamps

Every generated artifact carries a provenance header stamped by `pkg/genkit`. The header contains the canonical banner line and, when generated through the `build` pipeline, a full-project revision derived from the schema's content-addressed identity. The revision is computed once over the entire unfiltered schema, so even group/source-filtered outputs carry the same full-project stamp.

The stamp enables:
- **Freshness checking**: `--check` and `check --tag build` compare planned output byte-for-byte against disk
- **Revision enforcement**: `check --tag revision` verifies the stamped revision matches the current schema
- **Toolchain recognition**: Go's `go generate` convention recognizes the `// Code generated ... DO NOT EDIT.` pattern

## Determinism

All generators produce deterministic output: given the same input schema, they produce byte-identical output every time. This is a requirement of the `Generator` and `MultiFileGenerator` contracts in `pkg/genkit`. Determinism enables freshness checking -- without it, `--check` would report false stale results on every run.

Tables are emitted in dependency order (`Schema.TableOrder()`). Maps are iterated in sorted key order. Enum values preserve their declaration order from TOML.

## Consumer Regeneration

When bumping the pgdesign version, consumers must regenerate all codegen output in the same commit as the version bump. Generated code is version-coupled to the pgdesign binary that produced it -- stale output from a previous version may have different type annotations, import patterns, or structure. Use `pgdesign codegen --check` or `pgdesign check --tag build` in CI to catch stale output.

## Examples

### Go: types + constants + constraints

```toml
[output.go_types]
format = "codegen"
lang = "go"
mode = "types"
path = "gen/schema/types.go"

[output.go_constants]
format = "codegen"
lang = "go"
mode = "constants"
path = "gen/schema/constants.go"

[output.go_constraints]
format = "codegen"
lang = "go"
mode = "constraints"
path = "gen/schema/constraints.go"
```

```bash
pgdesign build
# Generates gen/schema/types.go, gen/schema/constants.go, gen/schema/constraints.go
```

### Go: GORM models

```toml
[output.go_gorm]
format = "codegen"
lang = "go"
mode = "gorm"
path = "gen/schema/models.go"
```

### TypeScript: Drizzle ORM

```toml
[output.ts_drizzle]
format = "codegen"
lang = "ts"
mode = "drizzle"
path = "gen/drizzle/schema.ts"
```

### Python: SQLAlchemy models

```toml
[output.py_sqlalchemy]
format = "codegen"
lang = "python"
mode = "sqlalchemy"
path = "gen/models.py"
```

### Python: query layer with both backends

```toml
[output.py_query_layer]
format = "codegen"
lang = "python"
mode = "query-layer"
path = "gen/query_layer/"
```

### Java: JPA entities

```toml
[output.java_jpa]
format = "codegen"
lang = "java"
mode = "jpa"
path = "gen/entities/"
```

### Filtered output by group

```toml
[output.api_types]
format = "codegen"
lang = "ts"
mode = "types"
path = "gen/api/types.ts"
groups = ["api"]

[output.internal_types]
format = "codegen"
lang = "ts"
mode = "types"
path = "gen/internal/types.ts"
groups = ["internal"]
```

### CI pipeline

```yaml
# GitHub Actions
- name: Check generated code
  run: pgdesign check --tag build
```

Or for a standalone codegen check:

```yaml
- name: Check codegen freshness
  run: |
    pgdesign codegen --lang go --mode types --output gen/schema/types.go --check schema/
    pgdesign codegen --lang go --mode constants --output gen/schema/constants.go --check schema/
```
