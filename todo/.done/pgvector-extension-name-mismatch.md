# pgvector extension name mismatch

## Problem

The built-in extension registry (`internal/extregistry/builtins.go`, around line 106) registers pgvector with `Name: "pgvector"`, but PostgreSQL's actual extension name is `vector`. This causes generated DDL to emit `CREATE EXTENSION pgvector`, which fails at runtime. The correct DDL is `CREATE EXTENSION vector`.

The same mismatch propagates to user-facing TOML: users write `extensions = ["pgvector"]` in the `[meta]` block, but PG expects `vector`.

## Fix options

1. **Rename the built-in to `"vector"`** -- correct but a breaking change for existing `pgdesign.toml` files that reference `"pgvector"`.
2. **Add an alias mapping** so both `"pgvector"` and `"vector"` resolve to the same built-in, with `"vector"` used in generated DDL. Non-breaking.
3. **Document that extension names must match the PostgreSQL `CREATE EXTENSION` name** -- minimal fix, shifts burden to users.

Option 2 is the most correct solution: it fixes the DDL, preserves backward compatibility for existing TOML files, and sets a precedent for other extensions where the common name differs from the PG extension name (e.g., PostGIS is `postgis`, which happens to match, but others may not).

## Affected files

- `internal/extregistry/builtins.go` -- the `Name` field in the pgvector entry
- Any DDL generation code that emits `CREATE EXTENSION <name>`
- TOML parsing that resolves extension names against the registry
