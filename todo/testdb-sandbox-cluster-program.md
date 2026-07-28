# testdb: DSN hygiene, generator fixes, and sandbox-cluster self-adoption

Provenance: `[%%]`-marked decisions were adopted from recommendations; unmarked were
deliberate user rulings.

## 1 — Remove the implicit default DSN (now)

`internal/testdb/skip.go:14` hard-codes `postgres://localhost:5432/postgres?sslmode=disable`
as the fallback when `PGDESIGN_DB` is unset — an implicit connection target, against the
no-implicit-defaults rule. Fix `[%%]`: absent `PGDESIGN_DB` → **skip with an explicit message**
(never probe a default); present → use it; `PGDESIGN_REQUIRE_DB=1` semantics unchanged
(absent/unreachable → fatal). Red-green on the absent-env path.

## 2 — Generator drift + post-DDL hook

Two functional divergences exist between the testdb templates and a consumer's checked-in
generated Python wrapper (hand-patched): the generator emits an ABSOLUTE `DDL_PATH` (breaks
CI; must be repo-relative) and the DDL apply lacks autocommit + duplicate-object tolerance.
Fix the templates FIRST — any regeneration sweep before that re-introduces both bugs.

Also add a **post-DDL hook** to the templates + Manager `[%%]`: consumers need extra setup
beyond the single `.sqlsplit` (ordered migration SQL files, seed scripts, database-level GUC
defaults). Without it, migrating them onto the generated wrappers loses setup steps.

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

## 4 — Native uuidv7 note

PostgreSQL 18 ships native `uuidv7()` in core. A consumer currently stubs the `pg_uuidv7`
extension in tests; the preferred path `[%%]` is eliminating that extension via the native
function (verify the consumer's production PG is ≥18 first). pgdesign side: confirm plain
function defaults render/migrate cleanly (near-certain; verify with a golden).
