---
title: "Validation Rules"
description: "Complete reference for all pgdesign validation rules: error codes, warning codes, and NF audit diagnostics."
---

# Validation Rules

pgdesign's validator checks schemas for errors and warnings. Errors block DDL generation; warnings are advisory. Rules can be disabled individually via `pgdesign.toml`.

## Error rules

Errors indicate problems that must be fixed.

### E200: Missing column type

A column has no PG type after type resolution. This usually means the column references an undefined semantic type.

```toml
[tables.users.columns.name]
type = "nonexistent_type"  # E200: column missing type
```

### E201: FK missing ON DELETE

Every foreign key must declare an `on_delete` clause.

```toml
[tables.posts.fks.fk_posts_author]
columns = ["author_id"]
ref_table = "users"
ref_columns = ["id"]
# E201: missing on_delete
```

**Fix:** Add `on_delete = "CASCADE"`, `"RESTRICT"`, `"SET NULL"`, or `"NO ACTION"`.

### E202: Table missing comment

Every table must have a `comment` field.

```toml
[tables.users]
# E202: table missing comment

[tables.users.columns.id]
type = "id"
```

**Fix:** Add `comment = "Description of this table"`.

### E203: Table missing primary key

Every table must have a primary key. Tables with an `id` or `auto_id` typed column get a PK inferred automatically.

```toml
[tables.logs]
comment = "Application logs"

[tables.logs.columns.message]
type = "short_text"
# E203: no pk defined and no id column
```

**Fix:** Add `pk = ["column"]` to the table definition.

### E204: FK references non-existent target

A foreign key references a table or column that does not exist in the schema.

```toml
[tables.posts.fks.fk_posts_category]
columns = ["category_id"]
ref_table = "categories"  # E204 if categories table not defined
ref_columns = ["id"]
on_delete = "RESTRICT"
```

### E206: Duplicate index

An index's columns are an exact prefix of another index on the same table.

### E207: varchar usage

`varchar` or `character varying` is used instead of `text`. pgdesign enforces `text` with `CHECK` constraints for length limits.

```toml
# Don't do this -- use short_text or text with check instead
[tables.users.columns.name]
type = "scalar"  # with base_type = "varchar(255)"
```

**Fix:** Use `text` with a `CHECK(LENGTH(col) <= N)` constraint, or the `short_text` built-in type.

### E208: timestamp without time zone

`timestamp` (without time zone) is used instead of `timestamptz`. Always use `timestamptz` to avoid timezone ambiguity.

**Fix:** Use the `timestamp` or `timestamp_optional` semantic types, or `timestamptz` as a raw base type.

### E209: serial usage

`serial` or `bigserial` is used. These are legacy PostgreSQL types.

**Fix:** Use the `auto_id` semantic type (which uses `GENERATED ALWAYS AS IDENTITY`) or the `id` type (UUID).

### E210: float for money

A `float`, `real`, or `double precision` type is used on a column with a money-related name (price, cost, amount, balance, total, fee). Floating-point types cause rounding errors with monetary values.

**Fix:** Use the `money` semantic type (bigint in minor units) or `numeric(precision, scale)`.

### E211: Naming convention violation

Table, column, or index names do not match the `snake_case` pattern (`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`).

### E212: FK columns missing index

FK columns have no covering index. Without an index, joins and cascaded deletes perform full table scans.

```toml
[tables.posts.fks.fk_posts_author]
columns = ["author_id"]
ref_table = "users"
ref_columns = ["id"]
on_delete = "CASCADE"
# E212: add an index on (author_id)
```

**Fix:** Add an index on the FK columns.

### E213: Generated column references generated column

A generated column's expression references another generated column. PostgreSQL does not allow this.

```toml
[tables.orders.columns.subtotal]
type = "money"
generated = "quantity * unit_price"
stored = true

[tables.orders.columns.total]
type = "money"
generated = "subtotal + tax"  # E213: references generated column subtotal
stored = true
```

**Fix:** Only reference non-generated columns in generated expressions.

### E214: Opclass requires undeclared extension

An index uses an operator class (e.g., `gin_trgm_ops`) that requires a PostgreSQL extension not listed in `[meta].extensions`.

**Fix:** Add the extension to `extensions = ["pg_trgm"]` in the `[meta]` section.

### E109: Enum default is not a declared value

Enum defaults must match one of the values in the enum's values list. Use raw values (e.g., `"created"`) not SQL literals (`"'created'"`). See also E110.

```toml
# Wrong: "archived" is not in the values list
[types.status]
kind = "enum"
values = ["created", "running", "done"]
default = "archived"  # E109

# Correct: default matches a declared value
[types.status]
kind = "enum"
values = ["created", "running", "done"]
default = "created"
```

**Fix:** Change the default to one of the declared enum values.

### E110: Default value contains embedded SQL quotes

Default values should be raw values -- pgdesign handles SQL quoting. Write `default = "created"` not `default = "'created'"`. This applies to all types (enums, scalars, arrays).

```toml
# Wrong: embedded SQL quotes
[types.status]
kind = "enum"
values = ["created", "running", "done"]
default = "'created'"  # E110

# Correct: raw value
[types.status]
kind = "enum"
values = ["created", "running", "done"]
default = "created"
```

This also applies to column-level defaults:

```toml
# Wrong
default = "'pending'"  # E110

# Correct
default = "pending"
```

**Fix:** Remove the embedded single quotes. For SQL expressions, use `default_expr` instead of `default`.

### E215: RLS policy expression mismatch

A row-level security policy uses the wrong expression type for its operation:
- INSERT policies should use `with_check`, not `using`
- SELECT and DELETE policies cannot use `with_check`
- UPDATE and ALL can use both

## Warning rules

Warnings highlight potential design issues but do not block DDL generation.

### W001: God table

A table has more columns than the configured maximum (default: 30). This suggests the table is doing too much and should be decomposed.

**Suggestion:** Split into smaller, focused tables with foreign key relationships.

### W002: Orphan table

A table has no FK relationships at all -- it neither references nor is referenced by any other table. This may indicate a missing relationship or an unused table.

### W003: Boolean state machine

A table has 3 or more boolean columns. Multiple boolean flags often indicate a state machine that would be better modeled as an enum column.

```toml
# W003: is_active, is_verified, is_suspended suggest a status enum
[tables.users.columns.is_active]
type = "flag"

[tables.users.columns.is_verified]
type = "flag"

[tables.users.columns.is_suspended]
type = "flag"
```

**Suggestion:** Replace with `type = "status"` using an enum type like `values = ["active", "verified", "suspended"]`.

### W004: JSON array could be a table

A jsonb column with a plural name and an empty array default (`'[]'::jsonb`) may be storing data that belongs in a normalized table.

**Suggestion:** Create a separate table with a foreign key instead of embedding a JSON array.

### W005: Missing created_at

A non-junction table (more than 2 columns) lacks a `created_at` column. Most tables benefit from tracking when rows were created.

### W006: char(n) usage

`char(n)` is used instead of `text`. In PostgreSQL, `char(n)` pads with spaces and offers no performance benefit over `text`.

### W007: Redundant index

An index's columns are a leading prefix of another index using the same method. The shorter index is redundant because the longer one handles the same queries.

### W008: Circular FK dependency

Tables have circular foreign key references (A references B, B references A). pgdesign handles this by creating tables without the FK first, then adding the FK via `ALTER TABLE`, but it may indicate a design issue.

### W009: Policy error_code not snake_case

An RLS policy's `error_code` field does not follow snake_case naming.

### W010: Append-only table has mutable default column

Tables with `append_only = true` should not have columns with mutable defaults (e.g., `updated_at` timestamp). Append-only tables are immutable after INSERT, so columns designed to track mutations are contradictory.

```toml
[tables.audit_log]
comment = "Immutable audit trail"
append_only = true

[tables.audit_log.columns.updated_at]
type = "timestamp"
default_expr = "now()"  # W010: mutable default on append-only table
```

**Suggestion:** Remove mutable-default columns from append-only tables, or remove `append_only = true` if the table needs to support updates.

## Normal form audit warnings

These are emitted by `pgdesign audit`, not `pgdesign validate`. They require functional dependencies to be declared on the table.

### W100: 1NF violation (repeating group)

A jsonb column with a plural name, list-like name, or `'[]'::jsonb` default may contain repeating groups, violating first normal form.

### W101: 2NF violation (partial dependency)

A non-prime attribute depends on a proper subset of a composite candidate key. This means the column belongs in a separate table keyed by that subset.

```toml
[tables.enrollments]
comment = "Student enrollments"
pk = ["student_id", "course_id"]

[tables.enrollments.columns.student_name]
type = "short_text"
# W101: student_name depends only on student_id, not the full PK

[[tables.enrollments.dependencies]]
determinant = ["student_id"]
dependent = ["student_name"]
```

### W102: 3NF violation (transitive dependency)

A non-prime attribute is determined by a non-superkey. This means there is a transitive dependency that should be extracted into a separate table.

When a 3NF violation is detected, pgdesign suggests a decomposition using Bernstein's synthesis algorithm.

## Disabling rules

Disable rules by code in `pgdesign.toml`:

```toml
[validate]
disable = ["W002", "W005", "W006"]
```

This skips the disabled rules during `pgdesign validate`. The codes apply to the validation rules (E2xx, W00x). Audit warnings (W1xx) are controlled separately via `pgdesign audit`.

## Codegen diagnostics

Diagnostics emitted during code generation, not during validation.

### C001: Unparseable policy expression

The codegen validator generator could not parse an RLS policy expression into a supported pattern. The policy is skipped during code generation. This typically means the policy uses SQL constructs that the codegen pattern matcher does not yet support.

**Fix:** Simplify the policy expression to use a supported pattern, or write the validator code manually.

## Coverage checks

Coverage checks analyze constraint completeness and schema coverage. They are run by `pgdesign check` (registered as the `coverage` check) and report diagnostic codes C100-C104.

### C100: Table without check constraints

A table with more than 2 columns and no check constraints may be missing domain validation. Tables with `append_only = true` are exempt (their mutation constraints come from triggers, not checks).

### C101: FK columns without covering index

Foreign key columns have no covering index. Without an index, cascaded deletes and joins perform full table scans. This overlaps with E212 from the validator but is checked independently in the coverage analysis.

### C102: Unused enum type

An enum type is defined in the schema but not referenced by any column. This may indicate dead code or a type that was defined but never wired up.

### C103: Orphan table

A table with more than 2 columns has no foreign key relationships at all -- it neither references nor is referenced by any other table. Similar to W002 from the validator but checked independently in coverage analysis.

### C104: Missing index for FK join pattern

Suggests composite indexes for common join-and-filter patterns. When a foreign key references a table that has filter-like columns (`status`, `type`, `kind`, `category`, or columns ending in `_at` or `_date`), a composite index on `(fk_columns, filter_column)` can improve join performance. This is an informational suggestion (Info severity), not a warning.
