# Extension-provided types, index parameters, and views

A consumer project needs to define a PostgreSQL schema with pgvector embeddings, tsvector full-text search with UNION ALL views, and HNSW-tuned indexes. Several pgdesign features are missing or incomplete to fully express this schema.

## Blocking

### 1. Extension-provided types not valid as scalar base types

The `pgTypeAllowlist` in `internal/semtype/semtype.go` is a hard-coded set of PostgreSQL built-in types. Extension-provided types like `vector` (from pgvector) cannot be used as `base_type` in custom scalar definitions — they fail with E106 ("unknown base type").

The fix: make the allowlist extensible from the `[[extensions]]` registry in `pgdesign.toml`. When a user declares `types = ["vector", "halfvec", "sparsevec"]` on an extension, those names should be added to the set of valid base types for scalar definitions. This is more correct than manually adding extension types to the built-in allowlist, because:
- The set of extensions and their types is open-ended
- The user already declares extension types in `pgdesign.toml` — the information is there
- It preserves the validation guarantee: using an undeclared extension type is still a hard error

Affected files:
- `internal/semtype/semtype.go` — `pgTypeAllowlist` and `loadScalarType()` need to accept an expanded type set
- `internal/model/build.go` — `Build()` needs to pass extension-registered types into the semantic type resolver
- `internal/config/config.go` — already has `ExtensionConfig.Types`, just needs to flow through

Example TOML that should work after this change:
```toml
# pgdesign.toml
[[extensions]]
name = "pgvector"
types = ["vector", "halfvec", "sparsevec"]
opclasses = ["vector_cosine_ops", "vector_l2_ops", "vector_ip_ops"]
index_methods = ["hnsw", "ivfflat"]

# schema.toml
[types.embedding_384]
kind = "scalar"
base_type = "vector(384)"
comment = "384-dimensional embedding vector"
```

### 2. Index WITH parameters (storage parameters)

Index definitions have no way to specify PostgreSQL `WITH (...)` storage parameters. This matters for HNSW indexes (`m`, `ef_construction`), IVFFlat indexes (`lists`), and even standard B-tree indexes (`fillfactor`).

The fix: add a `with` field to index definitions.

Affected files:
- `internal/parse/types.go` — add `With map[string]string` to `RawIndex`
- `internal/parse/parse.go` — parse `with = { key = "value" }` in `parseIndex()`
- `internal/model/model.go` — add `With map[string]string` to `Index`
- `internal/model/build.go` — propagate `With` from raw to resolved
- `internal/sql/sql.go` — emit `WITH (key = value, ...)` in `CreateIndex()` after the column list, before the WHERE clause
- `internal/diff/diff.go` — detect changes to WITH parameters
- `internal/migrate/generate.go` — handle WITH parameter changes (requires DROP + CREATE INDEX)
- `internal/introspect/introspect.go` — read `pg_catalog.pg_index` / `pg_class.reloptions` to round-trip WITH params

Example TOML:
```toml
[tables.items.indexes.idx_items_embedding_hnsw]
columns = ["embedding"]
method = "hnsw"
opclass = "vector_cosine_ops"
with = { m = "16", ef_construction = "200" }
```

Expected SQL:
```sql
CREATE INDEX idx_items_embedding_hnsw ON app.items USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 200);
```

## Important

### 3. Views (CREATE VIEW)

pgdesign has no concept of views. There is no model type, parser section, SQL generator, differ, or introspector for views. This means any view — including generated tsvector UNION ALL views for unified full-text search, summary views, or API-facing projections — must be maintained outside pgdesign.

This is a substantial feature addition across the full pipeline:
- `internal/parse/types.go` — `RawView` struct (name, query, comment, check_option)
- `internal/parse/parse.go` — `parseViews()`, wire into `walk()`, handle `[views.*]` TOML section
- `internal/model/model.go` — `View` struct, `Views []View` on `Schema`
- `internal/model/build.go` — `resolveView()`, wire into `Build()`
- `internal/sql/sql.go` — `CreateView()` function
- `internal/generate/generate.go` — emit views after tables/indexes
- `internal/diff/diff.go` — detect view additions, removals, query changes
- `internal/migrate/generate.go` — `CREATE OR REPLACE VIEW` for changes, `DROP VIEW` for removals
- `internal/introspect/introspect.go` — read `pg_views` / `information_schema.views`
- `internal/introspect/export.go` — export views to TOML
- D2 diagram generator, JSON output, doc generator — include views

Example TOML:
```toml
[views.searchable_messages]
comment = "Unified full-text search across messages and commands"
query = """
SELECT id, 'message' AS kind, content, search_tsv
FROM messages
UNION ALL
SELECT id, 'command' AS kind, command_text, search_tsv
FROM commands
"""
```

### 4. Materialized views (CREATE MATERIALIZED VIEW)

Same scope as views but with additional concerns:
- `WITH [NO] DATA` option
- `REFRESH MATERIALIZED VIEW [CONCURRENTLY]` generation
- Indexes on materialized views (already supported syntactically if wired up)
- `pg_matviews` for introspection
- Diff logic: materialized views can't be altered in place — changes require DROP + CREATE + refresh
- `CONCURRENTLY` refresh requires a unique index

Example TOML:
```toml
[materialized_views.density_cache]
comment = "Pre-computed event density at multiple time granularities"
query = "SELECT ... FROM events GROUP BY ..."
with_data = true
refresh_concurrently = true   # requires a unique index

[materialized_views.density_cache.indexes.idx_density_pk]
columns = ["session_id", "bucket_ms", "bucket_start_ms"]
unique = true
```

## Nice to Have

### 5. pgvector as a built-in extension

pgvector is one of the most widely used PostgreSQL extensions but is not in the built-in registry at `internal/extregistry/builtins.go`. Users must declare it manually via `[[extensions]]` in `pgdesign.toml`. Adding it as a built-in (like pgcrypto, pg_trgm, btree_gin, postgis, hstore, ltree, citext) would reduce boilerplate and enable better validation out of the box.

Suggested registry entry:
```go
{
    Name:         "pgvector",
    Types:        []string{"vector", "halfvec", "sparsevec"},
    Opclasses:    []string{"vector_l2_ops", "vector_ip_ops", "vector_cosine_ops", "halfvec_l2_ops", "halfvec_ip_ops", "halfvec_cosine_ops", "sparsevec_l2_ops", "sparsevec_ip_ops", "sparsevec_cosine_ops"},
    Functions:    []string{"l2_distance", "inner_product", "cosine_distance", "l1_distance"},
    IndexMethods: []string{"hnsw", "ivfflat"},
}
```

### 6. Array column migration generation

The diff layer (`internal/diff/diff.go`) correctly detects `ArrayChanged` when a column's `array` flag changes, but `internal/migrate/generate.go` does not produce an `ALTER TABLE ... ALTER COLUMN ... TYPE text[]` (or vice versa) for this change. The migration is silently skipped.

### 7. Array column codegen

All 6 language generators in `internal/codegen/` ignore the `Array` flag on columns. Array columns should produce language-appropriate types: `list[str]` in Python, `[]string` in Go, `string[]` in TypeScript, `List<String>` in Java/Kotlin, `[]const u8` or similar in Zig.

### 8. Extension IndexMethods field is dead metadata

The `IndexMethods []string` field on `Extension` in `internal/extregistry/extregistry.go` is declared and populated in built-in registrations but never read by any validation, generation, or introspection code. Either wire it into validation (e.g., warn when using `method = "hnsw"` without declaring an extension that provides it) or remove the field.
