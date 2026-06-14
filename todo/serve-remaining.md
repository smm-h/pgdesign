# Remaining HTTP API server items

Split from the previous serve-remaining.md after deferring D2 color coding.

## SVG caching

SVG caching with invalidation on schema change (currently regenerated every request). Ships with web UI phase.

## Connection pool configuration

Connection pool size configuration (relies on pgx defaults). Implemented in v0.7.x; move to .done/ at release.

## Web UI

Architecture and technology decisions deferred. No embed directives, no web/ directory. Primary active item.

## Effort

Medium for SVG caching. Small for connection pool configuration. Large for the web UI.
