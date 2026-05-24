# cmd/pgdesign

CLI entry point. Uses strictcli.

## Commands

- `pgdesign generate <file>` -- Parse, build IR, validate, generate DDL to stdout. Flags: `--idempotent`, `--format` (sql|d2|json), `--output <path>`.
- `pgdesign validate <file>` -- Parse, build IR, run validation rules. Print diagnostics. Exit 1 on errors. Flags: `--json`.
- `pgdesign audit <file>` -- Parse, build IR, run NF auditor. Print findings with decomposition suggestions. Flags: `--strict-nf`, `--json`.
- `pgdesign fmt <file|dir>` -- Normalize TOML schema files to canonical form. Flags: `--check` (exit 1 if not canonical, don't write).
- `pgdesign introspect --db <url> --schema <name>` -- Connect to live DB, extract schema to TOML. Flags: `--output <path>`.
- `pgdesign diff <file> --db <url>` -- Diff desired TOML vs live DB. Print changes with risk classification. Flags: `--json`.
- `pgdesign migrate generate <file> --db <url>` -- Diff and produce a migration file in migrations/ dir. Detects DML needs, generates placeholders.
- `pgdesign migrate apply --db <url>` -- Apply pending migrations from migrations/ dir. Acquires advisory lock. Per-migration atomicity. Auto-detects non-transactional ops.
- `pgdesign migrate rollback --db <url>` -- Rollback most recent migration via stored down ops. Blocks on irreversible.
- `pgdesign migrate status --db <url>` -- Show applied/pending migrations.
- `pgdesign serve --db <url> --port <port>` -- HTTP server with JSON API and web UI for schema visualization, migration timeline, stats.

## Global flags

- `--quiet` -- Suppress non-error output.
- `--db <url>` -- PostgreSQL connection URL (postgres://...).
- `--strict-nf` -- Make NF violations block DDL generation.

## Version

`version.go` at package main level. Set via ldflags by goreleaser. Fallback to debug.ReadBuildInfo.

## Entry point

`main.go` creates the strictcli App, registers all commands, calls `app.Run()`.
