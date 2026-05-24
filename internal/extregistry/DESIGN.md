# internal/extregistry

PostgreSQL extension capability registry. Maps extension names to what they provide.

## Purpose

When a schema uses an opclass like `gin_trgm_ops` or a type like `geometry`, pgdesign needs to know which extension provides it so validate/ can check that the extension is declared in [meta].extensions.

## Data structure

```go
type Extension struct {
    Name       string
    Types      []string   // e.g., "geometry", "tsvector" (for postgis, pg_trgm)
    Opclasses  []string   // e.g., "gin_trgm_ops", "jsonb_path_ops"
    Functions  []string   // e.g., "gen_random_uuid", "ts_rank"
    IndexMethods []string // e.g., "gist" (when provided by extension)
}
```

## Registry

Shipped with pgdesign as a hardcoded Go map. Covers common extensions:

| Extension | Provides |
|-----------|----------|
| pgcrypto | gen_random_uuid (pre-PG13), crypt, digest functions |
| pg_trgm | gin_trgm_ops, gist_trgm_ops, similarity(), word_similarity() |
| btree_gin | GIN opclasses for scalar types (int, text, timestamp, etc.) |
| btree_gist | GiST opclasses for scalar types + exclusion constraint support |
| postgis | geometry, geography types, spatial index opclasses |
| hstore | hstore type, gin/gist opclasses |
| pg_partman | partman.create_parent(), run_maintenance_proc() |
| pg_cron | cron.schedule(), cron.unschedule() |
| pg_stat_statements | pg_stat_statements view |
| uuid-ossp | uuid_generate_v4() (legacy, replaced by gen_random_uuid in PG13) |
| citext | citext type (case-insensitive text) |
| ltree | ltree type, ltree operators |
| intarray | intarray operators, GIN opclass for int[] |

## User extension

Users can add custom extensions via pgdesign.toml:

```toml
[[extensions]]
name = "my_custom_ext"
opclasses = ["my_ops"]
types = ["my_type"]
```

## Lookup

`Registry.RequiredExtension(opclass string) (string, bool)` -- Given an opclass name, returns which extension provides it.
`Registry.RequiredExtensionForType(typeName string) (string, bool)` -- Given a type name, returns which extension provides it.

## Validate integration

validate/ calls extregistry to check:
1. If index uses opclass X, is the extension providing X declared in [meta].extensions?
2. If column uses type X (postgis geometry, hstore, etc.), is the extension declared?

Diagnostic E214 emitted if extension is missing.
