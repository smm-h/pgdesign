# Command-surface disposition (5.0)

Every existing `migrate` subcommand and flag with its post-phase-5 fate. Source
of truth for current registration: `cmd/pgdesign/handlers_migrate.go`. The
`migrate` group today: `plan`, `generate`, `apply`, `rollback`, `status`,
`squash`, `test`, `baseline`.

## Subcommands

| Subcommand | Fate | Notes |
|---|---|---|
| `plan` | **Retargeted (5.9).** Becomes the PURE preview of pending chain edges via the path-finder — no DB. Drift preview is `diff --live`'s job, not `plan`'s. |
| `generate` | **Retargeted (5.9).** `generate = diff(deserialize(head manifest via objstore), current model)` — pure, no DB, always large-table-safe. Emits/updates the edge artifact + revision manifest. |
| `apply` | **Rewritten (5.2/5.5/5.7).** Path-finder-driven; per-op precondition → execute → journal loop; chain_position advances per edge. |
| `rollback` | **Rewritten (5.6).** Journal-driven (files never consulted); reverse recorded down-ops. Retargeted to a REVISION (see `--to`). |
| `status` | **Retargeted (5.2).** Chain enumeration via the path-finder (applied edges from `pgdesign_applied_migrations`; pending from the path-finder) — replaces the semver-file scan. |
| `squash` | **Rewritten (5.3).** Emits a CONSOLIDATION EDGE (new edge, concatenated op-list); superseded originals retire to `archive/`. Never rewrites files. `optimizeDDLOps` and its tests retire as dead code. |
| `test` | **Retained (5.10).** Replays EDGES (not semver files). `--shadow` retained. |
| `baseline` | **Retargeted (5.10).** Synthesizes a revision manifest FROM INTROSPECTION as a genesis-parented edge, writes `chain_position`; the two semver guards re-expressed as reachability checks. |
| — | **Pre-upgrade guard (5.2).** Post-release, EVERY subcommand run against a PRE-UPGRADE database (old `pgdesign_migrations` present, no `chain_position`) HARD-ERRORS naming `migrate upgrade`. |
| `upgrade` | **New (5.2).** One-time, explicit adoption: writes chain files, migrates tracking rows, asserts the view reproduces the old applied set, drops the old table. |
| `rebase` | **New (5.10).** Re-parents a fork's tail; rebased-away edges retire to `archive/`; writes the remap. |

## Flags

| Flag (subcommand) | Fate |
|---|---|
| `--version` (`generate`) | **Removed (5.9).** Identity is content-derived; there is no semver version to assign. |
| `--version` (`baseline`) | **Retained but re-meaning (5.10).** A `version_label` for the baseline record, not a semver identity. |
| `--db` (`plan`) | **Dropped (5.9).** `plan` is pure; drift preview moves to `diff --live`. |
| `--db` (`generate`) | **Dropped (5.9).** Generation never reads the world (L5). |
| `--db` (`apply`, `rollback`, `status`, `test`, `baseline`, `squash`) | **Retained.** These are live-tier operations against the target/staging database. |
| `--dry-run` (`apply`) | **Retained (5.2).** Previews the path-finder's chosen edges and their SQL without executing. |
| `--to` (`rollback`) | **Retargeted (5.6).** A target REVISION (resolved via journal + remap), not a semver version. Refuses to cross the upgrade/baseline boundary. |
| `--from`/`--to` (`squash`) | **Retained, re-meaning (5.3).** Endpoints of the consolidation range, resolved as revisions/edges, not semver versions. |
| `--shadow` (`test`) | **Retained (5.10).** Shadow replay now replays edges. |
| `--timeout` (`test`) | **Retained.** Staging test-run abort timeout (distinct from the dead `apply --timeout`). |
| `--dir` (all) | **Retained.** The `migrations/` root (now containing `objects/`, `revisions/`, `chain/`, `archive/`). |
| `--description` (`baseline`) | **Retained.** Human description for the baseline record. |
| `--strict-nf` (per-command) | **Retained** where present (unchanged by phase 5). |
| `quiet` (global) | **Retained.** |

## Already done in 0.6 (noted for completeness)

| Flag | Status |
|---|---|
| `apply --timeout` | **DELETED in 0.6.** Was DEAD (registered, never read — the lock timeout comes from config). `apply` today registers only `--db`, `--dir`, `--dry-run`; the flag is already gone. |
| `baseline --adopt` | **DELETED in 0.6.** Was PHANTOM (named only in an error message, never registered). |
