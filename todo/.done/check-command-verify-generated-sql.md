# `pgdesign check` command to verify generated SQL is up-to-date

## Problem

When `generated.sql` is committed to the repo (for reference and CI), there is no built-in way to verify it matches the current TOML schema definitions. If someone edits a schema TOML file but forgets to regenerate, the committed SQL silently goes stale.

## Proposed solution

Add a `pgdesign check` command that:

1. Generates the DDL from the current TOML schema files (same logic as `pgdesign generate`)
2. Compares the output against the existing `generated.sql` file on disk
3. Exits 0 if they match, non-zero with a diff if they don't

This is analogous to `selfdoc check` verifying generated docs are up-to-date.

## Integration with release tooling

This command could be wired into rlsbl's pre-release flow alongside `selfdoc check`, ensuring that committed DDL is always in sync with the schema definitions before a release ships.

## Design notes

- The check should respect the same flags as `generate` (e.g., `--idempotent`) so the comparison is apples-to-apples
- Output path could be auto-detected from `pgdesign.toml` config or passed explicitly
- Exit code semantics: 0 = up-to-date, 1 = stale (with diff), 2 = error (missing files, etc.)
