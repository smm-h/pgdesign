# migrate path: ADD COLUMN lacks IF NOT EXISTS (generate path already fixed)

## Context

v0.24.4 added per-column `ADD COLUMN IF NOT EXISTS` guards to `generate --idempotent`
(internal/generate/generate.go, section 4b), closing the column-drift gap for the
single-file idempotent workflow. The **migrate** system was not touched: `opAddColumn`
in `internal/migrate/sql_gen.go` (~line 295) still emits plain
`ALTER TABLE ... ADD COLUMN` without a guard.

## Problem

The migrate path's idempotency relies solely on the `pgdesign_migrations` tracking table
+ advisory lock. That protects against re-running a *recorded* migration, but not against
the column already existing for any other reason: a database that was ever touched by the
idempotent single-file workflow (or by a hand ALTER, or by a partially-applied migration
that failed after the ADD COLUMN) will make the migration abort on
`duplicate column` — and since the migration never records as applied, it aborts on every
retry. The two workflows (generate --idempotent and migrate) are not safely mixable on
the same database, which is undocumented.

## Proposed solutions

1. **Emit `ADD COLUMN IF NOT EXISTS` in `opAddColumn` (recommended).** Matches the
   generate path's v0.24.4 behavior; PostgreSQL supports it from 9.6, and the migrate
   system already knows the pg_version. Idempotency becomes intrinsic to the statement,
   not just the tracking table.
   - Pros: one-line class fix; makes the two workflows mixable; retries after partial
     failure self-heal.
   - Cons: an unexpected pre-existing column with a DIFFERENT definition is silently
     tolerated rather than surfacing as a duplicate error — mitigate by keeping the
     tracking-table record as the source of truth and (optionally) verifying the column
     definition matches after the guard.
2. **Document the incompatibility** (migrate must never run on a database that used the
   idempotent single-file workflow) and leave the SQL as-is.
   - Pros: zero code.
   - Cons: leaves a real abort-loop failure mode; documentation-only guardrails don't
     hold against agents.

## Affected files

- `internal/migrate/sql_gen.go` (`opAddColumn`, ~line 295).
- Its tests (the migrate sql_gen test file; golden/string assertions on emitted SQL).
- Red-green: a test asserting the emitted ADD COLUMN carries the guard; plus (if the
  migrate tests have a DB-backed harness) an apply-twice/pre-existing-column case.

## Effort

Small. The emission change is one line plus tests; the definition-verification option in
solution 1 is the only open design point.
