---
title: "Format Reference"
description: "Complete reference for the pgdesign TOML schema format: meta, types, tables, columns, constraints, and indexes."
---

# Format Reference

pgdesign schemas are written in TOML. A schema file defines metadata, custom types, and table definitions.

## [meta]

The `[meta]` section declares schema-level settings.

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

User-defined semantic types extend the built-in type system.

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

## [tables.*]

Each table is defined under `[tables.<table_name>]`.

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

Columns are defined under `[tables.<table>.columns.<column>]`.

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

Foreign keys are defined under `[tables.<table>.fks.<fk_name>]`.

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

Indexes are defined under `[tables.<table>.indexes.<index_name>]`.

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

Per-column opclass map:

```toml
[tables.docs.indexes.idx_docs_multi]
columns = ["title", "body"]
method = "gin"
opclass = { title = "gin_trgm_ops", body = "gin_trgm_ops" }
```

Using an opclass that requires an undeclared extension triggers E214.

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

Used by the `audit` command for normal form analysis.

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

Partition lifecycle configuration.

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

## Project configuration (pgdesign.toml)

Project-level settings live in `pgdesign.toml` (separate from schema files).

```toml
[project]
schemas = ["schemas/auth.toml", "schemas/app.toml"]
migrations_dir = "migrations"

[database]
pg_version = 16

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
name = "pg_trgm"
opclasses = ["gin_trgm_ops", "gist_trgm_ops"]
```

### [suppress]

Suppress specific diagnostics on individual tables or columns. Each key is `"table.CODE"` or `"table.column.CODE"`, and the value is a mandatory reason string explaining why the suppression is justified.

```toml
[suppress]
"products.metadata.W004" = "metadata is a free-form JSONB blob, not a normalizable array"
"audit_log.W002" = "standalone audit table with no FK relationships by design"
```

Use the `--show-suppressed` flag with `pgdesign validate` to include suppressed diagnostics in the output (marked as suppressed with their reason).
