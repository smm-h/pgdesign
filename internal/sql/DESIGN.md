# internal/sql

Shared SQL builder. Produces PostgreSQL DDL fragments. Used by generate/ and migrate/.

## Primitives

- `QuoteIdent(name string) string` -- PG identifier quoting. Quotes if: reserved word, contains special chars, uppercase, or starts with digit. Uses double quotes.
- `QualifiedName(schema, name string) string` -- schema.name with proper quoting.
- `LiteralValue(value any, pgType string) string` -- Auto-quotes: strings get single quotes, numbers bare, booleans bare, NULL for nil.
- `ExprValue(expr string) string` -- Emitted verbatim (no quoting). For DEFAULT expressions like now(), gen_random_uuid().
- `ConstraintName(table, kind, columns []string) string` -- Auto-generate name: pk_<table>, fk_<table>_<target>, idx_<table>_<col1>_<col2>, uq_<table>_<col>, ck_<table>_<name>.

## Statement builders

Each returns a complete SQL statement string.

- `CreateSchema(name string, idempotent bool) string`
- `CreateExtension(name string, idempotent bool) string`
- `CreateEnum(schema, name string, values []string, idempotent bool) string`
- `CreateTable(table *model.Table, idempotent bool) string` -- Full CREATE TABLE with: columns (type, NOT NULL, DEFAULT, GENERATED), inline PRIMARY KEY, PARTITION BY clause. Does NOT include FKs (those come via ALTER TABLE for cycle safety).
- `AlterTableAddFK(schema, table string, fk *model.FK) string`
- `AlterTableAddUnique(schema, table string, uq *model.UniqueConstraint) string`
- `AlterTableAddCheck(schema, table string, ck *model.CheckConstraint) string`
- `CreateIndex(schema string, index *model.Index, table string, idempotent bool) string` -- Handles: method, opclass per column, WHERE clause, INCLUDE columns, CONCURRENTLY flag.
- `CommentOn(objectType, qualifiedName, comment string) string`
- `AlterTableOwner(schema, table, owner string) string`

## Idempotent mode

When `idempotent = true`, statements use:
- `CREATE TABLE IF NOT EXISTS`
- `CREATE INDEX IF NOT EXISTS`
- `CREATE SCHEMA IF NOT EXISTS`
- `CREATE EXTENSION IF NOT EXISTS`
- `DO $$ ... END $$` wrapper for constraints that lack IF NOT EXISTS syntax

## Design notes

This package has no dependencies on model/ types -- it accepts primitives (strings, slices). This allows migrate/ to call the same builders without depending on model/. If model/ types are convenient, thin adapter functions convert model types to primitive arguments.

Actually: this package DOES accept model/ types for convenience. generate/ and migrate/ both depend on model/ anyway. The sql/ package is the single place where SQL text is constructed -- no other package builds SQL strings directly.
