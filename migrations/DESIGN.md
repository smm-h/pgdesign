# migrations/

Directory where generated migration files live. Committed to git.

## File naming

Semver: `0.1.0.toml`, `0.2.0.toml`, `0.3.0.toml`. Sorted by semver (not lexicographic) for execution order.

## Lifecycle

1. User edits schema TOML files
2. `pgdesign migrate generate --db <url>` diffs desired vs live, writes a new migration file here
3. User reviews the file (or CI reviews it)
4. `pgdesign migrate apply --db <url>` applies pending files in semver order

## This directory is NOT the schema source of truth

The TOML schema files are the source of truth. Migration files are derived artifacts (the diff between schema versions). They exist for: audit trail, rollback capability, DML operations, and reproducibility.

## Version numbering

The tool auto-assigns the next version based on the change magnitude:
- New tables or major structural changes: minor bump (0.1.0 -> 0.2.0)
- Column additions, index changes: patch bump (0.1.0 -> 0.1.1)
- User can override when generating

## State

Applied migrations tracked in `pgdesign_migrations` table in the target DB. This directory + that table = full migration history.
