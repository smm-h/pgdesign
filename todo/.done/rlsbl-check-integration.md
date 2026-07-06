# rlsbl check integration (item 15 from orxtra-codegen-deferred.md)

> Split from `orxtra-codegen-deferred.md`: item 15 is now implemented via
> rlsbl's external check provider mechanism (Phase 10). The remaining items
> (17-22) remain active in `todo/orxtra-codegen-deferred-remaining.md`.

## 15. rlsbl check integration

**Status:** Implemented.

**What:** pgdesign registers as an rlsbl check provider. When `rlsbl check --tag quality` runs (or a new `--tag schema` tag), it invokes `pgdesign check --tag build` automatically if a `pgdesign.toml` exists.

**Why we need it:** orxtra uses rlsbl for release orchestration. rlsbl already runs tests, lint, selfdoc check, and changelog validation during releases. Schema staleness should be checked at the same gate. Currently it requires a separate CI job and a separate pre-checks hook invocation. With rlsbl integration, schema freshness is just another rlsbl check — no extra wiring.

**The hole it fills:** Eliminates the need to separately wire pgdesign checks into CI and release hooks. The release pipeline becomes: `rlsbl release run` → runs all checks including schema staleness → no stale DDL can ship.

**Implementation:** Rather than pgdesign auto-registering itself, rlsbl's external check provider mechanism (`.rlsbl/config.json` `external_checks`) allows consumer projects to declare `pgdesign check --tag build` as a preflight check. pgdesign's internal check ordering (build depends on validation) is handled within the single invocation by strictcli's check framework. Documentation in `docs/rlsbl-integration.md`. Test in `cmd/pgdesign/freshness_test.go` (`TestCheckBuild_FreshPassStaleFailLifecycle`).
