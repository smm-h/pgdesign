# pgdesign.toml project config system

The root DESIGN.md describes a comprehensive pgdesign.toml config file that is entirely unimplemented. All configuration currently comes from CLI flags or hardcoded defaults.

## Described config sections

- `[project]`: schemas list, migrations_dir
- `[database]`: pg_version
- `[format]`: file_granularity, table_order, column_order
- `[validate]`: disable, naming_pattern, max_columns
- `[migrate]`: lock_timeout (hardcoded to 5s), auto_concurrent_threshold (10K rows), expand_contract_threshold (10M rows)
- `[[extensions]]`: user-defined extension config (LoadUserExtensions exists but is never called)

## What this enables

- Schema file discovery (currently CLI args only)
- Centralized validation rule disabling
- Configurable lock_timeout for migrations
- Auto-concurrent and expand-contract thresholds
- User extension definitions without code changes
- Format preferences per project

## Affected packages

- cmd/pgdesign (config loading, flag defaults)
- internal/validate (Config.Disabled already exists, just not loaded from file)
- internal/format (Config already exists, just not loaded from file)
- internal/migrate (lock_timeout hardcoded in apply.go and rollback.go)
- internal/extregistry (LoadUserExtensions exists, just never called)
- internal/parse (would need Directory() function for multi-file schemas)

## Effort

Medium-large. Config loading is straightforward, but wiring it into every command handler touches the whole CLI.
