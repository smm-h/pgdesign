# testdb: DSN hygiene, generator fixes, and sandbox-cluster self-adoption

Provenance: `[%%]`-marked decisions were adopted from recommendations; unmarked were
deliberate user rulings.

## 1 — Remove the implicit default DSN (now)

`internal/testdb/skip.go:14` hard-codes `postgres://localhost:5432/postgres?sslmode=disable`
as the fallback when `PGDESIGN_DB` is unset — an implicit connection target, against the
no-implicit-defaults rule. Fix `[%%]`: absent `PGDESIGN_DB` → **skip with an explicit message**
(never probe a default); present → use it; `PGDESIGN_REQUIRE_DB=1` semantics unchanged
(absent/unreachable → fatal). Red-green on the absent-env path.

## 3 — Self-adoption of the in-sandbox test cluster

The fleet test-sandbox runner (shipped via the release tooling's scaffold) will provision an
ephemeral Postgres inside the sandbox and export its DSN. pgdesign adopts with zero code — the
existing `PGDESIGN_DB` connection-env binding picks up the injected unix-socket DSN. Then:

- Delete the CI Postgres service container + docker-exec partman install
  (`.github/workflows/ci-go.yml` ~:19-42) in favor of runner-host packages (postgres +
  pg_partman + pgvector — pgvector is a current CI gap).
- Name the **package-fetch boundary** explicitly: the gradle/npm conformance lanes
  (`internal/test/testdb_conformance_test.go` ~:413-424, :541, :654) fetch from the network
  and can never run inside the sandbox's network namespace — they stay CI-provisioned (or
  skipped where the toolchain is absent), warm-cached where feasible. Not a DB problem; do not
  conflate.

