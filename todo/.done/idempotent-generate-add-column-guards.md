# `generate --idempotent` should emit ADD COLUMN guards (column-drift gap)

## Context

`pgdesign generate --idempotent` produces a single-file schema (`generated.sql`) that
consumers apply repeatedly (server startup, deploy pipelines). Idempotency is achieved
with `CREATE SCHEMA/EXTENSION/TABLE/INDEX ... IF NOT EXISTS`, DO-block catalog checks for
constraints/domains/enums, and `CREATE OR REPLACE` for functions/triggers.

## Problem

The idempotent output contains **no `ALTER TABLE ... ADD COLUMN` statements at all**. When
a column is added to a table's TOML *after* that table already exists in a target
database, re-applying `generated.sql` silently does nothing for it: `CREATE TABLE IF NOT
EXISTS` skips the existing table, and no other statement adds the column.

Observed consequences in real production use (a consumer project):

- Two columns added to a table's TOML post-initial-deploy never materialized in prod.
- Worse, a **later statement in the same generated file** (an FK-constraint DO-block
  referencing the new column) **aborted the entire apply**, so schema application failed
  wholesale on any database exhibiting the drift.
- A background job selecting the missing column crash-looped with `UndefinedColumnError`
  on every cycle, silently, for days.

The consumer had to work around it with a companion file: a script that parses
`generated.sql`'s CREATE TABLE blocks and emits `ALTER TABLE <t> ADD COLUMN IF NOT EXISTS
<coldef>;` for every column of every table, applied right after `generated.sql`. That
companion (and the obligation to regenerate it in lockstep) should not need to exist.

Note: pgdesign's separate `migrate generate/apply` system *does* emit
`ALTER TABLE ... ADD COLUMN` (see `internal/migrate/sql_gen.go` ~line 295), but without
`IF NOT EXISTS` — its idempotency comes from a `pgdesign_migrations` tracking table +
advisory lock. Consumers on the single-file idempotent workflow (no migrations dir, no
tracking table, no pgdesign binary in the runtime image) get no column-drift coverage at
all.

## Proposed solutions

1. **Emit per-column `ADD COLUMN IF NOT EXISTS` guards in `--idempotent` output
   (recommended).** After each `CREATE TABLE IF NOT EXISTS`, emit one guarded ALTER per
   column (PostgreSQL supports `ADD COLUMN IF NOT EXISTS` since 9.6). Ordering matters:
   guards must precede the FK-constraint DO-blocks so constraints referencing new columns
   succeed on drifted databases.
   - Pros: closes the drift class at the source; single-file schema becomes genuinely
     idempotent against column additions; consumers delete their workarounds.
   - Cons: output grows (one line per column); does not cover column *type changes* or
     *drops* (out of scope — document that).
2. **A new opt-in flag (e.g. `--idempotent-columns`).**
   - Pros: no output change for existing consumers.
   - Cons: violates correctness-by-default — the current default silently produces a file
     that claims idempotency but cannot reconcile the most common schema evolution
     (adding a column). Option proliferation for what should just be correct.
3. **Document the limitation only.**
   - Pros: zero code.
   - Cons: leaves a silent-degradation trap that already caused a multi-day production
     crash loop; every consumer must discover and re-implement the companion-file
     workaround.

Option 1 aligns with the house rule of correctness by default.

## Affected files

- `internal/generate/` — the idempotent emitter (where CREATE TABLE blocks are produced).
- Tests: red-green — create a table missing one column, apply the generated file, assert
  the column exists; re-apply, assert idempotent (no error). Include the FK-references-
  new-column ordering case (the real-world failure mode above).
- Docs: idempotent-mode documentation (state exactly what drift is and is not covered).

## Effort

Small–medium. The emitter change is mechanical (iterate the already-known column defs);
the care is statement ordering vs the constraint DO-blocks, plus the red-green tests.
Roughly half a day including tests and docs.
