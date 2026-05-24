# internal/serve

HTTP server for schema visualization, migration timeline, and database statistics.

## Command

`pgdesign serve --db <url> --port 8080`

Serves both a JSON API (for external tooling) and an embedded web UI.

## API endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| /api/schema | GET | Current resolved IR as JSON |
| /api/schema/d2 | GET | D2 diagram source for ER visualization |
| /api/schema/svg | GET | Rendered SVG of ER diagram (via D2 library) |
| /api/diff | POST | Diff desired schema (TOML body) against live DB |
| /api/migrations | GET | Migration history (from pgdesign_migrations table) |
| /api/migrations/:version | GET | Single migration details (ops, applied_at) |
| /api/audit | GET | NF audit findings for current live schema |
| /api/validate | POST | Validate a TOML schema (body) |
| /api/stats | GET | Database statistics (table sizes, row counts, index usage) |
| /api/stats/:table | GET | Per-table statistics (columns, bloat, index usage) |
| /api/extensions | GET | Installed extensions |

## Statistics queries

| Stat | Source |
|------|--------|
| Table row count | pg_stat_user_tables.n_live_tup |
| Table size (bytes) | pg_total_relation_size() |
| Index size | pg_relation_size() on each index |
| Index usage | pg_stat_user_indexes.idx_scan |
| Dead tuples (bloat indicator) | pg_stat_user_tables.n_dead_tup |
| Sequential scans | pg_stat_user_tables.seq_scan |
| Cache hit ratio | pg_stat_database.blks_hit / (blks_hit + blks_read) |
| Unused indexes | indexes with idx_scan = 0 |
| Duplicate indexes | indexes whose column set is a subset of another |

## Web UI (deferred -- frontend framework TBD)

Embedded via `//go:embed web/dist/*`. Pages planned:
- Schema viewer: interactive ER diagram (D2-rendered SVG with click-to-inspect)
- Migration timeline: visual history of all applied migrations
- Validation dashboard: current schema health (errors, warnings)
- Audit report: NF findings with decomposition suggestions
- Stats dashboard: table sizes, index usage, bloat estimates

Dev mode (`-tags dev`): serves from filesystem for hot-reload during frontend development.

## Connection

Uses pgxpool (connection pool) for concurrent API requests. Pool size configurable via pgdesign.toml or command flags.

## D2 rendering

ER diagram generation:
1. Build D2 graph programmatically via d2oracle API
2. Each table = a shape with columns listed (name: type)
3. FK relationships = labeled edges (label = ON DELETE action)
4. Color coding: green = clean, yellow = warnings, red = errors
5. Render to SVG via d2svg.Render()

Diagram is regenerated on each /api/schema/svg request (or cached with invalidation on schema change).
