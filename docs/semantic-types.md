---
title: "Semantic Types"
description: "Reference for pgdesign's semantic type system: built-in types, custom types, and type composition rules."
---

# Semantic Types

pgdesign uses a semantic type system instead of raw PostgreSQL types. Each semantic type maps to a PG type and carries default constraints (NOT NULL, default values, check expressions). This ensures consistency across your schema and prevents common mistakes like nullable IDs or timestamps without time zones.

## Why semantic types?

Raw PG types leave too many decisions to each column definition. Semantic types encode your project's conventions once:

- `id` always means UUID, NOT NULL, with `gen_random_uuid()` default
- `money` always means bigint (cents), NOT NULL, default 0
- `email` always means text, NOT NULL, with a format check constraint

When you write `type = "email"`, every email column in your schema gets the same PG type, nullability, and check constraint automatically.

## Built-in types

| Type | PG Type | NOT NULL | Default | Check | Notes |
|------|---------|----------|---------|-------|-------|
| `id` | `uuid` | yes | `gen_random_uuid()` | -- | Primary key type |
| `ref` | `uuid` | yes | -- | -- | Foreign key type |
| `timestamp` | `timestamptz` | yes | `now()` | -- | Creation/event timestamps |
| `timestamp_optional` | `timestamptz` | no | -- | -- | Nullable timestamps (deleted_at, completed_at) |
| `money` | `bigint` | yes | `0` | -- | Monetary amounts in minor units (cents) |
| `slug` | `text` | yes | -- | `VALUE ~ '^[a-z0-9-]+$'` | URL-safe identifiers |
| `email` | `text` | yes | -- | `VALUE ~ '^[^@]+@[^@]+\.[^@]+$'` | Email addresses |
| `short_text` | `text` | yes | -- | `LENGTH(VALUE) <= 255` | Short text fields |
| `json` | `jsonb` | yes | `'{}'::jsonb` | -- | JSON objects |
| `json_array` | `jsonb` | yes | `'[]'::jsonb` | -- | JSON arrays |
| `counter` | `bigint` | yes | `0` | -- | Incrementing counters |
| `flag` | `boolean` | yes | `false` | -- | Boolean flags |
| `auto_id` | `bigint` | yes | -- | -- | Identity column (GENERATED ALWAYS AS IDENTITY) |

## Defining custom types

### Scalar types

Scalar types wrap a PostgreSQL base type with constraints.

```toml
[types.currency_amount]
kind = "scalar"
base_type = "numeric"
check = "VALUE >= 0"
comment = "Non-negative monetary amount"

[types.phone]
kind = "scalar"
base_type = "text"
check = "VALUE ~ '^\\+[0-9]{7,15}$'"
comment = "E.164 phone number"

[types.percentage]
kind = "scalar"
base_type = "integer"
default = "0"
check = "VALUE >= 0 AND VALUE <= 100"

[types.nullable_text]
kind = "scalar"
base_type = "text"
not_null = false
comment = "Text column that allows NULL"
```

The `kind` field defaults to `"scalar"` when omitted.

**Requirements:**
- `base_type` must be a valid PostgreSQL type from the allowlist
- `check` expressions must contain the `VALUE` placeholder (replaced with the column name in generated DDL)
- `base_type` cannot reference another user-defined type (no type chaining)

### Enum types

Enum types create a PostgreSQL `CREATE TYPE ... AS ENUM`.

```toml
[types.status]
kind = "enum"
values = ["active", "inactive", "suspended"]

[types.priority]
kind = "enum"
values = ["low", "medium", "high", "critical"]
default = "medium"
```

Enum types are NOT NULL by default. Override with `not_null = false`.

### Array types

Any column or custom type can be made into an array by setting `array = true`. The base type stays clean (e.g., `text`), and pgdesign appends `[]` in the generated DDL.

```toml
[tables.posts.columns.tags]
type = "text"
array = true
# Result: text[] NOT NULL

[tables.events.columns.attendees]
type = "ref"
array = true
default = "{}"
# Result: uuid[] NOT NULL DEFAULT '{}'
```

Array defaults use raw values: `default = "{}"` produces `DEFAULT '{}'` in the generated DDL.

## Type composition rules

When a column uses a semantic type, the type provides base values and the column can override them.

### Override precedence

1. Column `nullable` overrides type `not_null` (setting `nullable = true` makes the column nullable even if the type says `not_null = true`)
2. Column `default` overrides type `default` (and clears any `default_expr`)
3. Column `default_expr` overrides type `default_expr` (and clears any `default`)

### Examples

Using a type as-is:

```toml
[tables.users.columns.id]
type = "id"
# Result: uuid NOT NULL DEFAULT gen_random_uuid()
```

Overriding nullability:

```toml
[tables.users.columns.deleted_at]
type = "timestamp"
nullable = true
# Result: timestamptz (nullable, no default)
# The type's default now() is preserved, but nullable overrides NOT NULL.
```

Overriding the default:

```toml
[tables.orders.columns.status]
type = "short_text"
default = "pending"
# Result: text NOT NULL DEFAULT 'pending' CHECK(LENGTH(status) <= 255)
```

## Type validation errors

| Code | Error |
|------|-------|
| E100 | User-defined type has empty name |
| E101 | Enum type must have at least one value |
| E102 | Scalar type must have a base PG type |
| E103 | Composite types are not yet supported |
| E104 | Unknown type kind |
| E105 | Duplicate type name with different definition |
| E106 | Unknown base type (not in allowlist) |
| E107 | Base type references another user type (circular reference) |
| E108 | Check expression missing VALUE placeholder |
| E109 | Enum default value not in declared values |
| E110 | Default value contains embedded SQL quotes |

## Default values

The `default` field holds raw values. pgdesign handles SQL quoting automatically when generating DDL. Do not embed SQL quotes in default values.

```toml
# Correct: raw value, pgdesign adds SQL quotes in generated DDL
default = "created"

# Wrong: embedded SQL quotes (triggers E109)
default = "'created'"
```

For SQL expressions (function calls, casts, etc.), use `default_expr` instead:

```toml
# Correct: SQL expression via default_expr
default_expr = "now()"
default_expr = "'{}'::jsonb"
```

For example, given an enum with `values = ["created", "running", "done"]`, set the default as `default = "created"` -- the generated DDL will produce `DEFAULT 'created'` with proper quoting.

For array columns, use raw array literal syntax for defaults:

```toml
[tables.posts.columns.tags]
type = "text"
array = true
default = "{}"
# Result: text[] NOT NULL DEFAULT '{}'
```
