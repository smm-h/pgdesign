# HTTP API server gaps

## Missing endpoint

- `GET /api/migrations/:version`: single-migration detail view (only the list endpoint exists)

## Missing statistics

- Cache hit ratio: no query against pg_stat_database for blks_hit/blks_read
- Unused index detection: idx_scan data is returned but no server-side analysis flags idx_scan=0
- Duplicate index detection: no column-subset analysis

## Missing features

- D2 diagram color coding by schema health (green/yellow/red)
- SVG caching with invalidation on schema change (currently regenerated every request)
- Connection pool size configuration (relies on pgx defaults)

## Web UI

Explicitly deferred in DESIGN.md (frontend framework TBD). No embed directives, no web/ directory.

## Effort

Small for the missing stats and endpoint. Medium for SVG caching. Large for the web UI.
