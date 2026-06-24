---
title: "Validation Rules"
description: "Complete reference for all pgdesign validation rules including error codes, warning codes, normal form audit diagnostics, and coverage checks."
---

# Validation Rules

pgdesign's validator checks schemas for errors and warnings. Errors block DDL generation; warnings are advisory. Rules can be disabled individually via `pgdesign.toml`.

## Error rules

Errors indicate problems in the schema definition that must be fixed before DDL generation can proceed. Each error has a unique code starting with E and identifies a specific violation of pgdesign's schema rules. Common errors include missing type definitions, missing ON DELETE clauses on foreign keys, missing table comments, and usage of deprecated PostgreSQL types. Errors are always reported regardless of configuration and cannot be suppressed via the validate.disable setting.

### E200: Missing column type

A column has no PostgreSQL type after type resolution, which means the column references an undefined semantic type name that does not exist in either the built-in type registry or the user-defined types section of the schema. This is one of the most common errors when starting a new schema, typically caused by a typo in the type name or by referencing a type that has not yet been defined in the TOML file.

```toml
[tables.users.columns.name]
type = "nonexistent_type"  # E200: column missing type
```

### E201: FK missing ON DELETE

Every foreign key must explicitly declare an `on_delete` clause specifying what happens when the referenced row is deleted. PostgreSQL defaults to NO ACTION when on_delete is omitted, but this implicit default is a common source of integrity issues because developers often forget to consider the deletion behavior when defining foreign keys. pgdesign requires the explicit declaration to force a conscious decision about cascading, restricting, or nullifying on each foreign key relationship.

```toml
[tables.posts.fks.fk_posts_author]
columns = ["author_id"]
ref_table = "users"
ref_columns = ["id"]
# E201: missing on_delete
```

**Fix:** Add `on_delete = "CASCADE"`, `"RESTRICT"`, `"SET NULL"`, or `"NO ACTION"`.

### E202: Table missing comment

Every table must have a `comment` field that describes the table's purpose in the schema. pgdesign generates COMMENT ON TABLE statements in the DDL from these descriptions, making them visible in PostgreSQL's pg_catalog and in database tools like pgAdmin. Requiring comments forces documentation at the schema level, ensuring that every table has at least a brief description of what data it holds and why it exists in the system.

```toml
[tables.users]
# E202: table missing comment

[tables.users.columns.id]
type = "id"
```

**Fix:** Add `comment = "Description of this table"`.

### E203: Table missing primary key

Every table must have a primary key to uniquely identify rows and enable efficient lookups, joins, and foreign key references. Tables that include a column using the `id` or `auto_id` semantic type get a primary key inferred automatically on that column, so an explicit `pk` declaration is only needed when using a different column or a composite primary key. Tables without any PK declaration and without an id-typed column produce this error.

```toml
[tables.logs]
comment = "Application logs"

[tables.logs.columns.message]
type = "short_text"
# E203: no pk defined and no id column
```

**Fix:** Add `pk = ["column"]` to the table definition.

### E204: FK references non-existent target

A foreign key references a table or column that does not exist in the schema. This can happen when a table name is misspelled in the `ref_table` field, when the referenced column name does not match the target table's actual column names, or when the referenced table has been removed from the schema without updating the foreign keys that point to it. The validator checks both the table existence and the column existence within that table.

```toml
[tables.posts.fks.fk_posts_category]
columns = ["category_id"]
ref_table = "categories"  # E204 if categories table not defined
ref_columns = ["id"]
on_delete = "RESTRICT"
```

### E206: Duplicate index

An index's columns are an exact duplicate of another index on the same table, meaning both indexes cover the same columns in the same order with the same method. Duplicate indexes waste disk space and slow down every write operation because PostgreSQL must maintain both. This differs from W007 (redundant index) which detects leading-prefix overlaps rather than exact duplicates.

### E207: varchar usage

`varchar` or `character varying` is used as a base type instead of `text`. In PostgreSQL, `varchar(N)` and `text` with a CHECK constraint have identical performance characteristics, but `text` with an explicit CHECK is more flexible because the length limit can be changed by modifying the CHECK constraint without a table rewrite. pgdesign enforces this convention by requiring `text` with `CHECK(LENGTH(col) <= N)` for length-limited string columns, or the `short_text` built-in type which provides this pattern automatically.

```toml
# Don't do this -- use short_text or text with check instead
[tables.users.columns.name]
type = "scalar"  # with base_type = "varchar(255)"
```

**Fix:** Use `text` with a `CHECK(LENGTH(col) <= N)` constraint, or the `short_text` built-in type.

### E208: timestamp without time zone

`timestamp` without time zone is used instead of `timestamptz` (timestamp with time zone). PostgreSQL's `timestamp` type stores a date and time without any timezone information, which creates ambiguity about what moment in time the value represents. Applications running in different timezones will interpret the same stored value differently, leading to subtle data corruption. pgdesign requires `timestamptz` for all timestamp columns, which stores an absolute moment and converts to the session timezone on display.

**Fix:** Use the `timestamp` or `timestamp_optional` semantic types, or `timestamptz` as a raw base type.

### E209: serial usage

`serial` or `bigserial` is used as a column type. These are legacy PostgreSQL pseudo-types that create an implicit sequence and set the column default, but they have drawbacks compared to the modern GENERATED ALWAYS AS IDENTITY syntax. Serial columns do not prevent manual value insertion, which can cause sequence conflicts. pgdesign requires the `auto_id` semantic type or the `id` type instead.

**Fix:** Use the `auto_id` semantic type (which uses `GENERATED ALWAYS AS IDENTITY`) or the `id` type (UUID).

### E210: float for money

A `float`, `real`, or `double precision` type is used on a column with a money-related name (price, cost, amount, balance, total, fee). Floating-point types cause rounding errors with monetary values.

**Fix:** Use the `money` semantic type (bigint in minor units) or `numeric(precision, scale)`.

### E211: Naming convention violation

Table, column, or index names do not match the `snake_case` pattern defined as `^[a-z][a-z0-9]*(_[a-z0-9]+)*$`, which requires lowercase letters, digits, and underscores with no leading digits or consecutive underscores. Consistent snake_case naming is enforced because PostgreSQL automatically lowercases unquoted identifiers, so mixed-case names require quoting everywhere they are used. The naming convention is configurable via the `naming_pattern` setting in pgdesign.toml for projects with different naming standards.

### E212: FK columns missing index

Foreign key columns have no covering index, which means that JOIN operations using these columns and cascaded DELETE operations triggered by ON DELETE CASCADE must perform full table scans to find matching rows. On large tables, this can cause significant performance degradation and long-running queries that hold locks. pgdesign requires an index on FK columns to ensure that lookups are always index-backed, following PostgreSQL best practices for referential integrity performance.

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

A generated column's expression references another generated column in the same table. PostgreSQL does not allow this because it creates a dependency chain between generated columns that cannot be resolved during tuple storage. The expression for a generated column can only reference non-generated columns in the same table, ensuring that the computation is always based on concrete stored values rather than derived values that may themselves be in the process of being computed.

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

An index uses an operator class like `gin_trgm_ops` or `vector_cosine_ops` that requires a PostgreSQL extension not listed in the schema's extension declarations. Operator classes are provided by extensions and must be available in the database before the index can be created. Without the extension declaration, pgdesign cannot verify that the operator class exists and the generated DDL would fail during application. The validator maintains a registry of known extensions and their provided operator classes.

**Fix:** Add the extension to `extensions = ["pg_trgm"]` in the `[meta]` section.

### E109: Enum default is not a declared value

Enum defaults must match one of the values declared in the enum's values list. pgdesign validates the default value against the declared values at schema compile time, catching typos and invalid defaults before they reach the database. Use raw values like `"created"` rather than SQL-quoted literals like `"'created'"` because pgdesign handles SQL quoting automatically during DDL generation. Invalid defaults would cause INSERT failures at runtime, so catching them early prevents data insertion errors in production.

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

Default values must be raw values without embedded SQL quotes because pgdesign handles SQL quoting automatically during DDL generation. Writing `default = "'created'"` with embedded single quotes produces double-quoted output like `DEFAULT '''created'''` in the generated DDL, which is almost certainly not the intended result. This validation applies to all type kinds including enums, scalars, and arrays, catching a common mistake that would otherwise produce subtle bugs where the default value includes literal quote characters.

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

A row-level security policy uses an expression type that is incompatible with its declared operation. PostgreSQL enforces specific rules about which expression types are valid for each policy operation: INSERT policies should use `with_check` because there are no existing rows to evaluate USING against, while SELECT and DELETE policies cannot use `with_check` because they only read existing rows. This validation catches configuration errors that would cause policy creation to fail at the database level.
- INSERT policies should use `with_check`, not `using`
- SELECT and DELETE policies cannot use `with_check`
- UPDATE and ALL can use both

### E216: Index WITH parameter not valid for method

An index uses a `with` storage parameter that is not valid for the specified index method, which would cause the CREATE INDEX statement to fail at the database level. Each PostgreSQL index method supports a specific set of storage parameters that control its internal behavior. For example, btree indexes support fillfactor and deduplicate_items, while HNSW indexes from pgvector support m and ef_construction. Using a parameter from the wrong method is always an error.

- **btree**: `fillfactor`, `deduplicate_items`
- **hash**: `fillfactor`
- **gin**: `fastupdate`, `gin_pending_list_limit`
- **gist**: `fillfactor`, `buffering`
- **brin**: `pages_per_range`, `autosummarize`
- **hnsw** (pgvector): `m`, `ef_construction`
- **ivfflat** (pgvector): `lists`

```toml
[tables.items.indexes.idx_items_embedding]
columns = ["embedding"]
method = "hnsw"
with = { fillfactor = "90" }  # E216: fillfactor is not valid for hnsw
```

**Fix:** Use only parameters valid for the index method. Consult the PostgreSQL or extension documentation for supported parameters.

### E217: Unknown index method

An index uses a method name that is not one of PostgreSQL's built-in methods (`btree`, `hash`, `gin`, `gist`, `brin`, `spgist`) and is not provided by any extension declared in the schema. This typically indicates a typo in the method name or the use of an extension-provided method without declaring the extension. The validator maintains a registry of known extension methods, so methods like `hnsw` and `ivfflat` are recognized when the pgvector extension is declared.

```toml
[tables.items.indexes.items_embedding_idx]
columns = ["embedding"]
method = "foo"
```

Fix: use a built-in method or declare the extension that provides the desired method via
`[[extensions]]`.

### E219: Index method requires undeclared extension

An index uses an extension-provided index method like `hnsw` or `ivfflat` without the providing extension being declared in the schema via `[[extensions]]`. Unlike E217 which catches completely unknown methods, E219 specifically identifies methods that the validator recognizes as belonging to a known extension but that extension has not been declared. This distinction provides a more helpful error message that tells the developer exactly which extension to declare rather than just reporting an unknown method name.

```toml
[tables.items.indexes.items_embedding_idx]
columns = ["embedding"]
method = "hnsw"
```

Fix: declare the extension:

```toml
[[extensions]]
name = "pgvector"
```

## Warning rules

Warnings highlight potential design issues in the schema that may indicate anti-patterns, performance problems, or modeling errors, but they do not block DDL generation. Each warning has a unique code starting with W and can be individually disabled via the `validate.disable` setting in pgdesign.toml when the flagged pattern is intentional. Warnings can also be suppressed on specific tables or columns using the `[suppress]` section with a mandatory reason string explaining why the suppression is justified.

### W001: God table

A table has more columns than the configured maximum threshold, which defaults to 30 columns. Tables with many columns often indicate that the table is trying to represent multiple concepts in a single relation, which violates the single responsibility principle and can lead to wide rows that exceed the TOAST threshold, NULL-heavy columns that waste storage, and complex queries that touch many columns unnecessarily. The threshold is configurable via the `max_columns` setting in the validate section of pgdesign.toml.

**Suggestion:** Split into smaller, focused tables with foreign key relationships.

### W002: Orphan table

A table has no foreign key relationships at all, meaning it neither references nor is referenced by any other table in the schema. Orphan tables may indicate a missing relationship that should connect it to the rest of the data model, an unused table left over from a previous schema revision, or a legitimate standalone table like configuration storage. The warning is suppressible for tables that are intentionally disconnected from the relational graph.

### W003: Boolean state machine

A table has 3 or more boolean columns, which often indicates that the table is modeling a state machine using individual boolean flags instead of a single enum column. Boolean flag sets create invalid state combinations (such as `is_active = true` and `is_suspended = true` simultaneously) that are difficult to prevent with CHECK constraints. An enum column eliminates invalid states by construction because only declared values are allowed, and pgdesign's state machine type adds trigger-enforced transitions for additional safety.

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

A JSONB column with a plural name and an empty array default (`'[]'::jsonb`) may be storing a list of items that would be better modeled as a separate normalized table with a foreign key relationship. Embedding arrays in JSONB columns circumvents referential integrity, makes it impossible to enforce constraints on individual array elements, prevents efficient indexing of element values, and violates first normal form. The pattern is detected heuristically by combining the column name (plural form) with the array default.

**Suggestion:** Create a separate table with a foreign key instead of embedding a JSON array.

### W005: Missing created_at

A non-junction table with more than 2 columns lacks a `created_at` column. Most tables benefit from tracking when rows were created because this timestamp enables debugging data issues, auditing changes, implementing retention policies, and ordering records by creation time. Junction tables (typically with only 2 FK columns forming a composite PK) are exempt because they represent relationships rather than entities and their creation timing is less commonly needed.

### W006: char(n) usage

`char(n)` is used instead of `text`. In PostgreSQL, `char(n)` pads stored values with trailing spaces to the declared length, which wastes storage space, creates confusing comparison behavior where trailing spaces affect equality checks, and offers no performance benefit over `text`. The only reason to use `char(n)` in PostgreSQL is compatibility with SQL standard or legacy systems, and pgdesign recommends `text` with a CHECK constraint for fixed-length requirements instead.

### W007: Redundant index

An index's columns are a strict leading prefix of another index on the same table using the same index method, making the shorter index redundant. PostgreSQL can use a multi-column btree index to satisfy queries on any prefix of the column list. For example, index A on `(user_id)` is redundant when index B on `(user_id, created_at)` exists. Same-length column lists are not flagged.

### W008: Circular FK dependency

Tables have circular foreign key references (A references B, B references A). pgdesign handles this by creating tables without the FK first, then adding the FK via `ALTER TABLE`, but it may indicate a design issue.

### W009: Policy error_code not snake_case

An RLS policy's `error_code` field does not follow the snake_case naming convention required by pgdesign. Error codes in RLS policies are used by application code to identify specific access denial reasons, so consistent naming prevents errors when matching codes in error handlers. The snake_case pattern requires lowercase letters, digits, and underscores, matching the same naming convention enforced on tables, columns, and indexes throughout the schema.

### W010: Append-only table has mutable default column

Tables with `append_only = true` should not have columns with mutable defaults like `updated_at` timestamps with `now()` as the default expression. Append-only tables are immutable after INSERT because a BEFORE UPDATE OR DELETE trigger prevents all mutations, so columns designed to track when rows were last modified are contradictory and will never contain meaningful update timestamps. This warning identifies columns whose semantic purpose conflicts with the table's append-only constraint.

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

Normal form audit warnings are emitted by `pgdesign check --tag nf`, not by `pgdesign check --tag validation`, and they require functional dependencies to be explicitly declared on the table using the `[[dependencies]]` syntax. Without declared dependencies, the audit cannot determine whether a table violates normal forms because it has no information about which columns functionally determine which others. The audit checks 1NF through 3NF violations and suggests decompositions using Bernstein's synthesis algorithm when violations are found.

### W100: 1NF violation (repeating group)

A JSONB column with a plural name, list-like name, or empty array default `'[]'::jsonb` may contain repeating groups, which violates first normal form. First normal form requires that every column contains atomic values rather than sets, lists, or nested structures. While PostgreSQL's JSONB type technically allows storing arrays and nested objects, using them to represent repeating groups prevents the database from enforcing constraints on individual elements and makes queries more complex.

### W101: 2NF violation (partial dependency)

A non-prime attribute depends on a proper subset of a composite candidate key rather than the full key, violating second normal form. The column's value is determined by only part of the primary key and should be extracted into a separate table keyed by that subset. For example, `student_name` depending only on `student_id` in a `(student_id, course_id)` composite key belongs in a separate students table.

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

A non-prime attribute is functionally determined by a column set that is not a superkey, indicating a transitive dependency that should be extracted into a separate table. In a transitive dependency, column A determines B, and B determines C, creating update anomalies because changing B's value inconsistently leads to contradictory C values. When detected, pgdesign suggests a decomposition using Bernstein's synthesis algorithm.

When a 3NF violation is detected, pgdesign suggests a decomposition using Bernstein's synthesis algorithm.

## Disabling rules

Individual validation rules can be disabled by their diagnostic code in `pgdesign.toml` when a rule does not apply to your project. Disabled rules are completely skipped during `pgdesign check --tag validation` and do not appear in the output. This is useful for projects with legitimate reasons to deviate from pgdesign's defaults, such as using varchar for compatibility with external systems or having intentionally orphaned tables for audit logging.

```toml
[validate]
disable = ["W002", "W005", "W006"]
```

This skips the disabled rules during `pgdesign check --tag validation`. The codes apply to the validation rules (E2xx, W00x). Audit warnings (W1xx) are emitted by `pgdesign check --tag nf`.

## Codegen diagnostics

Codegen diagnostics are emitted during application code generation rather than during schema validation. These diagnostics identify situations where the codegen engine encounters schema patterns it cannot fully translate into the target language, such as RLS policy expressions that use SQL constructs not supported by the codegen pattern matcher. Codegen diagnostics use the C0xx code range and are separate from the validation diagnostics that use E and W codes.

### C001: Unparseable policy expression

The codegen validator generator could not parse an RLS policy expression into a supported pattern. The policy is skipped during code generation. This typically means the policy uses SQL constructs that the codegen pattern matcher does not yet support.

**Fix:** Simplify the policy expression to use a supported pattern, or write the validator code manually.

## Coverage checks

Coverage checks analyze constraint completeness and overall schema quality by looking for patterns that suggest missing constraints, missing indexes, or unused type definitions. They are registered as the `coverage` check in strictcli's check framework and report diagnostic codes in the C100-C104 range. Coverage checks run independently of the main validation rules and provide a complementary view of schema health focused on completeness rather than correctness.

### C100: Table without check constraints

A table with more than 2 columns and no check constraints may be missing domain validation. Tables with `append_only = true` are exempt (their mutation constraints come from triggers, not checks).

### C101: FK columns without covering index

Foreign key columns have no covering index. Without an index, cascaded deletes and joins perform full table scans. This overlaps with E212 from the validator but is checked independently in the coverage analysis.

### C102: Unused enum type

An enum type is defined in the schema's `[types]` section but not referenced by any column in any table. This may indicate dead code from a schema refactoring, a type defined for future use but never wired up, or a naming mismatch where a column references a different type name. The coverage check surfaces these unused definitions so they can be connected to columns or removed.

### C103: Orphan table

A table with more than 2 columns has no foreign key relationships at all -- it neither references nor is referenced by any other table. Similar to W002 from the validator but checked independently in coverage analysis.

### C104: Missing index for FK join pattern

Suggests composite indexes for common join-and-filter patterns. When a foreign key references a table that has filter-like columns (`status`, `type`, `kind`, `category`, or columns ending in `_at` or `_date`), a composite index on `(fk_columns, filter_column)` can improve join performance. This is an informational suggestion (Info severity), not a warning.
