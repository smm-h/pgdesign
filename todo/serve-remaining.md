# Remaining HTTP API server items

Split from serve-gaps.md after implemented items were completed.

## Missing features

- D2 diagram color coding by schema health (green/yellow/red)
- SVG caching with invalidation on schema change (currently regenerated every request)
- Connection pool size configuration (relies on pgx defaults)

## Web UI

Architecture and technology decisions deferred. No embed directives, no web/ directory.

## Effort

Medium for SVG caching. Small for connection pool configuration. Large for the web UI.
