# Findings from oxtra schema (second real consumer)

oxtra is a Python library for autonomous multi-agent AI workflows. Its schema has 13 tables, 13 enum types, and exercises enum defaults, nullable FKs, partial indexes, jsonb columns, and append-only table patterns. These findings come from writing and validating `schema/oxtra.toml`.

## Bug: Enum defaults are triple-quoted in generated DDL

When an enum type declares `default = "'created'"`, the generated SQL outputs `DEFAULT '''created'''` instead of `DEFAULT 'created'`. The TOML value already contains the SQL single quotes; pgdesign wraps them in an additional layer. This affects every enum column with a default value. The generated DDL is invalid SQL -- PostgreSQL will interpret `'''created'''` as a string literal containing `'created'`, not the enum value `created`.

Reproduction: any enum type with a default, e.g.:

```toml
[types.run_status]
kind = "enum"
values = ["created", "running"]
default = "'created'"
```

Generated: `DEFAULT '''created'''`
Expected: `DEFAULT 'created'`

## Feature: Native PostgreSQL array types

pgdesign has no way to express `text[]`, `integer[]`, `uuid[]`, or other PostgreSQL array types. These are first-class PG types used for tags, search vectors, and multi-value columns where a junction table would be over-normalization.

In oxtra, `inbox_items.tags` and `lessons.relevance_tags` are naturally `text[]` columns. Without array support, the workaround is `json_array` (jsonb), which loses type safety (any JSON value, not just text) and triggers W004 ("jsonb array could be a separate table") -- exactly the wrong advice for a tags column.

Suggested: support `text[]` etc. as `base_type` in scalar type definitions, or as a built-in semantic type family.

## Feature: Per-column or per-table warning suppression

When a warning is intentional (e.g., W004 on a tags jsonb column, or W005 on a table that uses `last_updated` instead of `created_at`), there's no way to suppress it. The warnings appear on every validation run, training users to ignore them.

Suggested: either inline annotation (`suppress = ["W004"]` on a column or table) or a top-level `[suppress]` section mapping table.column to warning codes with a reason string.

## Feature: REVOKE statement support

Append-only tables (audit logs, transcripts, event streams) need `REVOKE UPDATE, DELETE ON table FROM role` to mechanically enforce immutability. This is a schema-level contract -- it defines what the table IS, not just what it contains -- but pgdesign can't express it.

Suggested: a `revoke` field on tables, e.g. `revoke = {operations = ["UPDATE", "DELETE"], from = "app_role"}`, generating the appropriate REVOKE statements in DDL.
