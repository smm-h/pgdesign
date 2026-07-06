---
title: "rlsbl Release Integration"
description: "How to wire pgdesign's build freshness check into rlsbl's release pipeline via the external check provider mechanism."
---

# rlsbl Release Integration

pgdesign's build freshness check can run as part of rlsbl's release pipeline. This ensures that no release ships with stale schema-derived output: if the TOML schema changed but `pgdesign build` was not re-run, the release is blocked.

## How It Works

rlsbl supports external check providers: subprocess commands declared in `.rlsbl/config.json` that run alongside rlsbl's built-in checks (changelog validation, test suite, lint). pgdesign's `check --tag build` command fits this model exactly: it exits 0 when all build outputs are fresh, and non-zero when any output is missing, stale, or orphaned.

When `rlsbl check` or `rlsbl release run` reaches the preflight phase, it invokes the external check command. pgdesign's check framework handles its own internal concerns (dependency ordering between checks, schema parsing, freshness comparison) within that single invocation. rlsbl sees only the exit code and diagnostic output.

## Configuration

Add an `external_checks` entry to the consumer project's `.rlsbl/config.json`:

```json
{
  "external_checks": [
    {
      "name": "pgdesign-build-freshness",
      "command": "pgdesign check --tag build",
      "tag": "preflight"
    }
  ]
}
```

Fields:

- `name`: Unique identifier for the check. Appears in rlsbl's check output.
- `command`: The shell command to execute. `pgdesign check --tag build` runs all checks tagged `build` (currently: the build freshness check, which depends on validation internally).
- `tag`: The rlsbl check tag that triggers this check. `preflight` runs during `rlsbl release run`.

Optional fields:

- `depends_on`: List of other check names that must pass first. Not needed here because pgdesign handles its own internal ordering.
- `cwd`: Working directory for the command. Defaults to the project root. Set this if the pgdesign.toml is in a subdirectory.

## What the Check Verifies

`pgdesign check --tag build` compares every configured output against what `pgdesign build` would produce:

- **Missing files**: An output file that should exist but does not.
- **Stale files**: An output file whose content differs from what the current schema would generate (byte-exact comparison).
- **Orphan files**: Files inside an owned output directory that the current configuration does not produce (left behind after renames or split-mode changes).

Any of these conditions causes a non-zero exit, which rlsbl treats as a hard failure. The release is blocked until `pgdesign build` is run and the results committed.

## Internal Check Ordering

pgdesign's checks have internal dependencies declared in `checks.toml`. The `build` check depends on `validation` -- if the schema has validation errors, the build check skips rather than producing misleading freshness results. This dependency is resolved by strictcli's check framework within the `pgdesign check --tag build` invocation. rlsbl does not need to know about these internal dependencies; the single command handles everything.

## Monorepo Projects

For monorepo projects where pgdesign.toml is in a subdirectory, use the `cwd` field:

```json
{
  "external_checks": [
    {
      "name": "pgdesign-build-freshness",
      "command": "pgdesign check --tag build",
      "tag": "preflight",
      "cwd": "packages/database"
    }
  ]
}
```

## Alternative: Pre-checks Hook

Projects not yet using rlsbl's external check mechanism can use the pre-checks hook instead. Add to `.rlsbl/hooks/pre-checks.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
pgdesign check --tag build
```

The external check provider is preferred because it integrates with rlsbl's check reporting (tag filtering, JSON output, dependency ordering) rather than being opaque script execution.
