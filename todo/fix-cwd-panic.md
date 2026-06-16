# pgdesign panics when run from any directory without .strictcli/checks.toml

## Problem

Running any pgdesign command (including `pgdesign version` or `pgdesign help`) from a directory that does not contain `.strictcli/checks.toml` causes a panic:

```
panic: checks_path does not exist: .strictcli/checks.toml
```

This makes pgdesign unusable as a tool from other projects. For example, running `pgdesign version` from `~/Projects/orxt` panics immediately.

## Root cause

In `cmd/pgdesign/cli.go` (lines 19-21), `strictcli.WithChecks(".strictcli/checks.toml")` is passed unconditionally to `strictcli.NewApp`. The path is relative and resolved against CWD. In strictcli's `NewApp` (`strictcli.go`, line 704), a missing checks file is treated as a programmer error and triggers a panic — this is intentional strictcli behavior.

The panic happens during app construction, before any command is dispatched, so even `--help` and `version` are affected.

## Impact

- Cannot use pgdesign as a tool from other projects (e.g., orxt needs to run `pgdesign generate` against its schema)
- Cannot run `pgdesign version` or `pgdesign help` from arbitrary directories
- The only workaround is `cd ~/Projects/pgdesign && pgdesign generate /absolute/path/to/schema.toml`, which works for `generate` but is fragile

## Fix

Use `strictcli.WithChecksEmbed` with `//go:embed` instead of `WithChecks` with a relative path. This embeds the checks.toml content into the binary at compile time, eliminating the filesystem dependency entirely.

The `gamehome/router` project already uses this pattern successfully:

```go
//go:embed .strictcli/checks.toml
var checksToml []byte

func main() {
    app := strictcli.NewApp("pgdesign", Version, "PostgreSQL schema compiler",
        strictcli.WithChecksEmbed(checksToml),
    )
    // ...
}
```

This is a one-line change (plus the embed directive). No strictcli changes needed — `WithChecksEmbed` already exists.

## Affected files

- `cmd/pgdesign/cli.go` — change `WithChecks` to `WithChecksEmbed` with `//go:embed`

## Effort

Minimal — a 3-line change.
