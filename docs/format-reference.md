---
title: "Format Reference"
description: "Complete reference for the pgdesign TOML schema format covering meta, types, tables, columns, constraints, indexes, views, materialized views, and project configuration."
---

# Format Reference

pgdesign schemas are written in TOML. A schema file defines metadata, custom types, and table definitions.

## [meta]

The `[meta]` section declares schema-level settings that apply to the entire schema file. This includes the target PostgreSQL major version for version-aware DDL generation, the PostgreSQL schema name for qualified identifiers, and the list of required PostgreSQL extensions that provide custom types, operator classes, and index methods used elsewhere in the schema.

```toml
[meta]
version = 16
schema = "public"
extensions = ["pgcrypto", "pg_trgm"]
```

| Key | Type | Description |
|-----|------|-------------|
| `version` | integer | PostgreSQL major version (used for PG-version-aware DDL generation) |
| `schema` | string | PostgreSQL schema name (e.g., `"public"`, `"auth"`) |
| `extensions` | array of strings | PostgreSQL extensions the schema depends on |

## [types.*]

User-defined semantic types extend the built-in type system with project-specific domain concepts. Types defined here can be referenced by any column in the schema and produce the corresponding PostgreSQL DDL: enum types become CREATE TYPE, scalar types with CHECK constraints become CREATE DOMAIN, composite types become CREATE TYPE AS, and state machines produce CHECK constraints with trigger-enforced transitions.

### Enum types

```toml
[types.status]
kind = "enum"
values = ["active", "inactive", "suspended"]
```

| Key | Type | Description |
|-----|------|-------------|
| `kind` | string | Must be `"enum"` |
| `values` | array of strings | Enum values (at least one required) |
| `not_null` | boolean | Override NOT NULL (default: true) |
| `default` | string | Raw default value (pgdesign handles SQL quoting) |
| `comment` | string | Type description |

### Scalar types

```toml
[types.currency_amount]
kind = "scalar"
base_type = "numeric"
check = "VALUE >= 0"
comment = "Non-negative monetary amount in minor units"
```

| Key | Type | Description |
|-----|------|-------------|
| `kind` | string | `"scalar"` (or omitted -- scalar is the default) |
| `base_type` | string | PostgreSQL base type (required for scalars) |
| `not_null` | boolean | Override NOT NULL (default: true) |
| `default` | string | Raw default value (pgdesign handles SQL quoting) |
| `default_expr` | string | SQL expression default, written as-is into DDL (e.g., `"now()"`) |
| `check` | string | Check expression using `VALUE` placeholder |
| `unique` | boolean | Whether columns of this type get a UNIQUE constraint |
| `comment` | string | Type description |

Allowed base types: `bigint`, `boolean`, `bytea`, `char`, `citext`, `date`, `float4`, `float8`, `inet`, `integer`, `interval`, `json`, `jsonb`, `macaddr`, `numeric`, `oid`, `real`, `serial`, `bigserial`, `smallint`, `smallserial`, `text`, `time`, `timetz`, `timestamp`, `timestamptz`, `tsquery`, `tsvector`, `uuid`, `varchar`, `xml`.

Extension-provided types are also valid as base types when declared via `[[extensions]]` in pgdesign.toml. For example, declaring `types = ["vector", "halfvec"]` on a pgvector extension makes `vector(384)` a valid base type for scalar definitions.

## [tables.*]

Each table is defined under `[tables.<table_name>]` with a required comment describing its purpose, a primary key specification, and nested sections for columns, foreign keys, indexes, unique constraints, check constraints, RLS policies, and partitioning. Tables are emitted in dependency order in the generated DDL, with circular foreign key references handled via deferred ALTER TABLE ADD CONSTRAINT statements.

```toml
[tables.users]
comment = "User accounts"
pk = ["id"]

[tables.users.columns.id]
type = "id"

[tables.users.columns.email]
type = "email"

[tables.users.columns.created_at]
type = "timestamp"
```

### Table-level properties

| Key | Type | Description |
|-----|------|-------------|
| `comment` | string | Table description (required -- E202 if missing) |
| `pk` | array of strings | Primary key columns (auto-inferred if a column uses `id` or `auto_id` type) |
| `enable_rls` | boolean | Enable row-level security on the table |
| `append_only` | boolean | Generates a BEFORE UPDATE OR DELETE trigger that prevents mutations. Tables with `append_only` should not have mutable-default columns (W010) |

## Column properties

Columns are defined under `[tables.<table>.columns.<column>]` and require a semantic type reference. All columns are NOT NULL by default; use `nullable = true` to opt in to nullability. Columns inherit defaults, NOT NULL behavior, and CHECK constraints from their semantic type, but can override any of these at the column level. Generated columns, array columns, and JSONB shape validation are also supported through column-level attributes.

```toml
[tables.products.columns.price]
type = "money"
default = "0"
```

| Key | Type | Description |
|-----|------|-------------|
| `type` | string | Semantic type name (built-in or user-defined, required) |
| `nullable` | boolean | Override the type's NOT NULL default |
| `default` | string | Raw default value -- pgdesign handles SQL quoting (overrides type default) |
| `default_expr` | string | SQL expression default, written as-is into DDL (overrides type default_expr) |
| `generated` | string | SQL expression for a generated column |
| `stored` | boolean | Whether the generated column is stored (default: false) |
| `array` | boolean | Marks the column as a PostgreSQL array type. DDL appends `[]` to the base type (e.g., `array = true` on a `text` column produces `text[]`) |
| `json_schema` | string | Path to a JSON Schema file (relative to the schema file). Generates CHECK constraints for top-level property validation (e.g., `json_schema = "schemas/address.json"`) |
| `comment` | string | Column description |

When both the type and the column define a default, the column-level value wins. Setting `nullable = true` on a column overrides the type's `not_null = true`.

### Generated columns

```toml
[tables.orders.columns.total_with_tax]
type = "money"
generated = "subtotal + tax"
stored = true
```

Generated columns cannot reference other generated columns (E213).

## Foreign keys

Foreign keys are defined under `[tables.<table>.fks.<fk_name>]` and require an explicit `on_delete` clause specifying CASCADE, RESTRICT, SET NULL, or NO ACTION. pgdesign enforces this requirement via E201 because implicit ON DELETE NO ACTION is a common source of integrity issues. Foreign key columns should have a covering index for join performance, enforced by E212.

```toml
[tables.posts.fks.fk_posts_author]
columns = ["author_id"]
ref_table = "users"
ref_columns = ["id"]
on_delete = "CASCADE"
```

| Key | Type | Description |
|-----|------|-------------|
| `columns` | array of strings | Local columns |
| `ref_table` | string | Referenced table name |
| `ref_columns` | array of strings | Referenced columns |
| `on_delete` | string | Required: `"CASCADE"`, `"RESTRICT"`, `"SET NULL"`, or `"NO ACTION"` |

Every FK must declare `on_delete` (E201). FK columns should have a covering index (E212).

## Indexes

Indexes are defined under `[tables.<table>.indexes.<index_name>]` and support btree, hash, gin, gist, brin, and extension-provided methods like hnsw and ivfflat. Each index specifies its columns, optional operator class, partial predicate, covering columns, uniqueness, and storage parameters. Duplicate indexes are detected by E206, and redundant indexes that are prefixes of other indexes are flagged by W007.

```toml
[tables.users.indexes.idx_users_email]
columns = ["email"]

[tables.events.indexes.idx_events_created_at]
columns = ["created_at"]
method = "brin"

[tables.docs.indexes.idx_docs_search]
columns = ["content"]
method = "gin"
opclass = "gin_trgm_ops"

[tables.users.indexes.idx_users_active_email]
columns = ["email"]
where = "deleted_at IS NULL"
unique = true

[tables.orders.indexes.idx_orders_covering]
columns = ["customer_id"]
include = ["status", "total"]
```

| Key | Type | Description |
|-----|------|-------------|
| `columns` | array of strings | Indexed columns |
| `method` | string | Index method: `btree` (default), `hash`, `gin`, `gist`, `brin` |
| `opclass` | string or map | Operator class (string applies to all columns; map for per-column) |
| `where` | string | Partial index predicate |
| `include` | array of strings | Covering index columns (INCLUDE clause) |
| `unique` | boolean | Create a unique index |
| `with` | map | Storage parameters as key-value pairs (e.g., `with = { m = "16", ef_construction = "200" }`) |

Per-column opclass map:

```toml
[tables.docs.indexes.idx_docs_multi]
columns = ["title", "body"]
method = "gin"
opclass = { title = "gin_trgm_ops", body = "gin_trgm_ops" }
```

Using an opclass that requires an undeclared extension triggers E214.

```toml
[tables.items.indexes.idx_items_embedding_hnsw]
columns = ["embedding"]
method = "hnsw"
opclass = "vector_cosine_ops"
with = { m = "16", ef_construction = "200" }
```

Valid WITH parameters depend on the index method. E216 is raised when a parameter is not valid for the specified method. Built-in methods (btree, hash, gin, gist, brin) and extension methods (hnsw, ivfflat) each have their own set of valid parameters.

## Unique constraints

```toml
[tables.users.uniques.uq_users_email]
columns = ["email"]
```

| Key | Type | Description |
|-----|------|-------------|
| `columns` | array of strings | Columns in the unique constraint |

## Check constraints

```toml
[tables.products.checks.chk_price_positive]
expr = "price >= 0"
```

| Key | Type | Description |
|-----|------|-------------|
| `expr` | string | SQL check expression |

## Row-level security policies

```toml
[tables.documents.policies.pol_owner_access]
for = "ALL"
to = "authenticated"
using = "owner_id = current_user_id()"
with_check = "owner_id = current_user_id()"
error_code = "access_denied"
error_message = "You can only access your own documents"
```

| Key | Type | Description |
|-----|------|-------------|
| `for` | string | Operation: `SELECT`, `INSERT`, `UPDATE`, `DELETE`, or `ALL` |
| `to` | string | Role the policy applies to |
| `using` | string | SQL expression for existing row visibility |
| `with_check` | string | SQL expression for new/modified row validation |
| `error_code` | string | Application error code (should be snake_case -- W009) |
| `error_message` | string | Human-readable error message |

INSERT policies should use `with_check`, not `using`. SELECT and DELETE policies cannot use `with_check` (E215).

## Partitioning

```toml
[tables.events.partitioning]
strategy = "range"
column = "created_at"

[[tables.events.partitioning.partitions]]
name = "events_2024_q1"
bound = "FROM ('2024-01-01') TO ('2024-04-01')"

[[tables.events.partitioning.partitions]]
name = "events_2024_q2"
bound = "FROM ('2024-04-01') TO ('2024-07-01')"
```

| Key | Type | Description |
|-----|------|-------------|
| `strategy` | string | Partition strategy: `range`, `list`, or `hash` |
| `column` | string | Partition key column |
| `partitions` | array of tables | Child partition definitions |

Each partition child:

| Key | Type | Description |
|-----|------|-------------|
| `name` | string | Child table name |
| `bound` | string | Bound expression |

## Functional dependencies

Functional dependencies are declared per-table and used by `pgdesign check --tag nf` for normal form analysis. Each dependency specifies a determinant (left-hand side columns) and dependent (right-hand side columns), allowing the audit engine to check 1NF through BCNF compliance. Dependencies inferred from primary keys and unique constraints must be explicitly declared via A100, and any redundancy in declared dependencies is surfaced as an informational diagnostic.

```toml
[[tables.enrollments.dependencies]]
determinant = ["student_id"]
dependent = ["student_name"]
```

| Key | Type | Description |
|-----|------|-------------|
| `determinant` | array of strings | Left-hand side columns |
| `dependent` | array of strings | Right-hand side columns |

## Maintenance

Partition lifecycle configuration controls automatic partition management for time-series and append-only tables. The maintenance section specifies how many future partitions to pre-create ahead of the current period, the retention duration after which old partitions are eligible for cleanup, and whether expired partitions should be preserved as detached tables rather than dropped entirely. These settings integrate with pg_partman when available.

```toml
[tables.events.maintenance]
premake = 3
retention = "90d"
retention_keep_table = true
```

| Key | Type | Description |
|-----|------|-------------|
| `premake` | integer | Number of future partitions to pre-create |
| `retention` | string | Retention period (e.g., `"90d"`, `"1y"`) |
| `retention_keep_table` | boolean | Keep expired partition tables instead of dropping |

## [views.*]

Views are defined under `[views.<view_name>]` with a required SQL SELECT query and optional comment and dependency declarations. Views are emitted after all tables in the generated DDL output, and the `depends_on` field controls ordering when views reference other views. pgdesign validates that referenced tables exist and generates CREATE OR REPLACE VIEW statements in the correct dependency order.

```toml
[views.active_users]
comment = "Users with active accounts"
query = """
SELECT id, email, created_at
FROM users
WHERE status = 'active'
"""
depends_on = ["users"]
```

| Key | Type | Description |
|-----|------|-------------|
| `query` | string | SQL SELECT statement (required) |
| `comment` | string | View description |
| `depends_on` | array of strings | Tables or views this view depends on (for ordering) |

Views are emitted after tables in DDL output. The `depends_on` field controls ordering when views reference other views.

## [materialized_views.*]

Materialized views are defined under `[materialized_views.<view_name>]` with a required SQL SELECT query, optional WITH DATA flag, and support for nested index definitions. Unlike regular views, materialized views store their query results on disk and must be explicitly refreshed. Indexes on materialized views are required for REFRESH MATERIALIZED VIEW CONCURRENTLY, which avoids locking the view during refresh operations.

```toml
[materialized_views.user_stats]
comment = "Pre-computed user statistics"
query = """
SELECT u.id, COUNT(p.id) AS post_count
FROM users u
LEFT JOIN posts p ON p.author_id = u.id
GROUP BY u.id
"""
with_data = true
depends_on = ["users", "posts"]

[materialized_views.user_stats.indexes.idx_user_stats_id]
columns = ["id"]
unique = true
```

| Key | Type | Description |
|-----|------|-------------|
| `query` | string | SQL SELECT statement (required) |
| `comment` | string | View description |
| `with_data` | boolean | Populate data on creation (default: true) |
| `depends_on` | array of strings | Tables or views this view depends on (for ordering) |

Materialized views support nested index definitions using the same syntax as table indexes. Indexes on materialized views are required for `REFRESH MATERIALIZED VIEW CONCURRENTLY`.

## Project configuration (pgdesign.toml)

Project-level settings live in `pgdesign.toml`, which is separate from the TOML schema files that define tables and types. This configuration file controls which schema files to load, the migrations directory path, formatting preferences, validation rule overrides, migration behavior thresholds, extension declarations with their provided types and operator classes, database connection pool settings, and build output targets for generating SQL, diagrams, documentation, and application code.

```toml
[project]
schemas = ["schemas/auth.toml", "schemas/app.toml"]
migrations_dir = "migrations"

[database]
pg_version = 16
pool_max_conns = 25
pool_min_conns = 5

[format]
table_order = "dependency"
column_order = "pk_fk_alpha"

[validate]
disable = ["W002", "W005"]
naming_pattern = "snake_case"
max_columns = 30

[migrate]
lock_timeout = "5s"
expand_contract_threshold = 10000000

[[extensions]]
name = "pgvector"
types = ["vector", "halfvec", "sparsevec"]
opclasses = ["vector_cosine_ops", "vector_l2_ops", "vector_ip_ops"]
index_methods = ["hnsw", "ivfflat"]
```

| Key | Type | Description |
|-----|------|-------------|
| `name` | string | Extension name (required) |
| `types` | array of strings | Types provided by the extension (become valid base types for scalars) |
| `opclasses` | array of strings | Operator classes provided by the extension |
| `index_methods` | array of strings | Index methods provided by the extension (e.g., hnsw, ivfflat) |

### [database]

| Key | Type | Description |
|-----|------|-------------|
| `pg_version` | integer | PostgreSQL major version for version-aware DDL generation |
| `pool_max_conns` | integer | Maximum connections in the pgxpool. When absent, pgxpool uses its built-in defaults (max connections = number of CPUs) |
| `pool_min_conns` | integer | Minimum connections in the pgxpool. When absent, pgxpool uses its built-in default (min connections = 0) |

### [suppress]

Suppress specific diagnostics on individual tables or columns when the default rule does not apply to a particular case. Each key is `"table.CODE"` or `"table.column.CODE"`, and the value is a mandatory reason string explaining why the suppression is justified. The reason requirement prevents blanket suppression without documentation, ensuring that each exception has a recorded rationale that future maintainers can evaluate.

```toml
[suppress]
"products.metadata.W004" = "metadata is a free-form JSONB blob, not a normalizable array"
"audit_log.W002" = "standalone audit table with no FK relationships by design"
```

Suppressed diagnostics are excluded from check output. Suppression applies during `pgdesign check --tag validation`.

## [output.*]

Build output targets define what `pgdesign build` generates from the compiled schema. Each output is a named section under `[output]` specifying a format (sql, d2, json, svg, doc, or codegen), a file path, and format-specific options. For codegen outputs, the target language and generation mode must be specified. Multiple outputs can be configured to generate SQL DDL, D2 diagrams, JSON snapshots, documentation, and application-layer code from a single build command.

```toml
[output.ddl]
format = "sql"
path = "out/schema.sql"
idempotent = true
comments = true

[output.diagram]
format = "d2"
path = "out/schema.d2"

[output.docs]
format = "doc"
path = "out/schema.md"

[output.api_types]
format = "codegen"
path = "out/types.ts"
lang = "ts"
mode = "validators"

[output.snapshot]
format = "json"
path = "out/schema.json"
```

| Key | Type | Description |
|-----|------|-------------|
| `format` | string | Output format: `sql`, `d2`, `json`, `svg`, `doc`, or `codegen` |
| `path` | string | Output file path relative to project root (required) |
| `lang` | string | Target language for codegen: `go`, `ts`, `java`, `kotlin`, `python`, `zig` (required when format is `codegen`) |
| `mode` | string | Codegen mode: `validators`, `constants`, or `types` (required when format is `codegen`) |
| `idempotent` | boolean | For `sql` format: add `IF NOT EXISTS` guards |
| `comments` | boolean | For `sql` format: include `COMMENT ON` statements (default: true) |

Running `pgdesign build` generates all configured outputs. Use `--dry-run` to preview what would be generated without writing files.
