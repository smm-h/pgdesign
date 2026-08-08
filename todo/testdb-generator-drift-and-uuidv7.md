# testdb: DSN hygiene, generator fixes, and sandbox-cluster self-adoption

Provenance: `[%%]`-marked decisions were adopted from recommendations; unmarked were
deliberate user rulings.

## 2 — Generator drift + post-DDL hook

Two functional divergences exist between the testdb templates and a consumer's checked-in
generated Python wrapper (hand-patched): the generator emits an ABSOLUTE `DDL_PATH` (breaks
CI; must be repo-relative) and the DDL apply lacks autocommit + duplicate-object tolerance.
Fix the templates FIRST — any regeneration sweep before that re-introduces both bugs.

Also add a **post-DDL hook** to the templates + Manager `[%%]`: consumers need extra setup
beyond the single `.sqlsplit` (ordered migration SQL files, seed scripts, database-level GUC
defaults). Without it, migrating them onto the generated wrappers loses setup steps.

## 4 — Native uuidv7 note

PostgreSQL 18 ships native `uuidv7()` in core. A consumer currently stubs the `pg_uuidv7`
extension in tests; the preferred path `[%%]` is eliminating that extension via the native
function (verify the consumer's production PG is ≥18 first). pgdesign side: confirm plain
function defaults render/migrate cleanly (near-certain; verify with a golden).
