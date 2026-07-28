# Trim the legacy migration surface to the no-backward-compat minimum

## Context

The 0.25.x chain migration system kept the old semver-TOML code paths alive
as a transition surface for pre-upgrade projects. The saferm incident (the
legacy squash flow exec'd a personal dev-machine tool, failing CI and
blocking the 0.25.0/0.25.1 publishes) demonstrated that this retained
surface is (a) barely tested, (b) unused — no known consumer has committed
legacy migration TOMLs — and (c) a defect harbor. The house rule is no
backward compatibility for pre-stable projects; the retained file-mode
operations exceed the defensible minimum.

## Problem

The pre-upgrade DB guard already forces one-way adoption for anything
touching a database, but pure FILE operations on old-format projects still
work: legacy squash (squashFiles + ArchiveLegacyOriginals), legacy
generate's auto-semver mode (NextSemverVersion), and legacy plan's
live-diff mode. Each is an IsChainMode branch that keeps dead-format code
alive.

## Solution

Reduce to the minimum the one-time conversion needs:

- `migrate upgrade` KEEPS its ability to parse legacy semver TOMLs for the
  prefix fold (ParseMigrationFile and the fold path stay).
- Legacy squash, legacy generate (auto-semver), and legacy plan (live-diff
  mode) are DELETED outright; invoking them on a non-chain project becomes
  a hard error naming `migrate upgrade`.
- Kill `IsChainMode` branching wherever it becomes single-purpose; the
  chain is the only migration format.
- Retire the legacy-path tests with the code they cover (sanctioned test
  deletion: they test deleted behavior; the upgrade-fold tests stay).
- Sweep for any remaining legacy-only helpers left uncalled afterward
  (state.go's legacy writers, rollback.go's file-driven path where only
  pre-upgrade DBs used it — the guard makes them unreachable; delete per
  the dead-code policy).

## Affected

cmd/pgdesign/handlers_migrate.go (mode branches), internal/migrate/
{squash.go, apply.go, rollback.go, state.go, parse_migration.go (fold-only
surface remains), baseline.go}, their tests, docs/migration-guide.md.

## Effort

Medium-small: mostly deletion with careful test retirement; one
documentation pass.
