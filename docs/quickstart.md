---
title: "Quickstart"
description: "Get started with pgdesign in five minutes: install, define a schema, generate SQL, and validate."
---

# Quickstart

pgdesign is a PostgreSQL schema compiler. You define your database schema in TOML, and pgdesign compiles it to SQL DDL with strict enforcement of database design principles.

## Installation

### Go

```
go install github.com/smm-h/pgdesign/cmd/pgdesign@latest
```

### npm

```
npm install pgdesign
```

### pip

```
pip install pgdesign
```

## Creating your first schema

Create a file called `schema.toml`:

```toml
[meta]
version = 16
schema = "public"

[tables.users]
comment = "User accounts"

[tables.users.columns.id]
type = "id"

[tables.users.columns.email]
type = "email"

[tables.users.columns.display_name]
type = "short_text"

[tables.users.columns.created_at]
type = "timestamp"

[tables.posts]
comment = "User-authored posts"

[tables.posts.columns.id]
type = "id"

[tables.posts.columns.author_id]
type = "ref"

[tables.posts.columns.title]
type = "short_text"

[tables.posts.columns.body]
type = "short_text"

[tables.posts.columns.published]
type = "flag"

[tables.posts.columns.created_at]
type = "timestamp"

[tables.posts.fks.fk_posts_author]
columns = ["author_id"]
ref_table = "users"
ref_columns = ["id"]
on_delete = "CASCADE"

[tables.posts.indexes.idx_posts_author_id]
columns = ["author_id"]
```

Key design decisions enforced by pgdesign:

- Every table requires a `comment`.
- Columns use semantic types (`id`, `email`, `timestamp`) instead of raw PG types.
- Every FK must declare `on_delete`.
- FK columns should have an index.

## Generating SQL

```
pgdesign generate schema.toml
```

This produces the full DDL: `CREATE TABLE` statements, constraints, indexes, and `COMMENT ON` statements, all in dependency order.

Add `--idempotent` for `IF NOT EXISTS` guards:

```
pgdesign generate --idempotent schema.toml
```

Other output formats:

```
pgdesign generate --format json schema.toml
pgdesign generate --format d2 schema.toml
pgdesign generate --format svg schema.toml
```

## Validating a schema

```
pgdesign check --tag validation
```

The validator checks for errors (missing types, FK targets that don't exist, naming violations) and warnings (god tables, orphan tables, missing timestamps). Exit code is 1 if any errors are found.

Disable specific rules in `pgdesign.toml`:

```toml
[validate]
disable = ["W002", "W005"]
```

## Formatting a schema

```
pgdesign fmt schema.toml
```

Formats the TOML file with consistent ordering: tables in dependency order, columns ordered by PK then FK then alphabetical.

Check mode (exit 1 if not formatted, useful in CI):

```
pgdesign fmt --check schema.toml
```

Options for ordering:

```
pgdesign fmt --table-order=alphabetical --column-order=fk_last schema.toml
```

## Auditing for normal form violations

```
pgdesign check --tag nf
```

The NF check examines 1NF, 2NF, and 3NF violations using declared functional dependencies. For tables without dependencies declared, the check is skipped. When a database URL is configured (in `pgdesign.toml` or `PGDESIGN_DB`), pgdesign discovers functional dependencies automatically.

Use `--strict-nf` on the `generate` command to block DDL output when NF violations exist:

```
pgdesign generate --strict-nf schema.toml
```

## Next steps

- [Format Reference](format-reference.html) -- full TOML schema syntax
- [Semantic Types](semantic-types.html) -- built-in and custom types
- [Validation Rules](validation-rules.html) -- all error and warning codes
- [Migration Guide](migration-guide.html) -- generating and applying migrations
