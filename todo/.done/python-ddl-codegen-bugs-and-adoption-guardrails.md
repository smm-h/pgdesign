# Python DDL codegen: two blocking bugs and two adoption guardrails

## Context

A consumer project is adopting `pgdesign codegen --lang python --mode ddl` as its single
source of generated, committed Python DDL modules (replacing hand-maintained `_schema.py`
files), with regeneration on TOML change and a CI freshness check. During evaluation
against pgdesign v0.20.0, two bugs and two feature gaps surfaced. The bugs block adoption;
the guardrails would make the adoption path safe for any consumer.

## Problem 1: codegen emits invalid SQL for enums (`CREATE TYPE IF NOT EXISTS`)

`codegen --lang python --mode ddl` emits `CREATE TYPE IF NOT EXISTS ... AS ENUM (...)`
in both the `sql` and `idempotent_sql` fields of the generated statements. PostgreSQL
does not support `IF NOT EXISTS` on `CREATE TYPE` — the statement is a syntax error on
every PostgreSQL version.

The faceted split mode's generated `schema_executor.py` happens to sidestep this because
it guards execution with catalog existence checks (`SELECT 1 FROM pg_type ...`) before
running each statement, so the invalid string is never executed when the type already
exists — but on a fresh database the raw statement still gets executed and fails. Any
consumer that executes the generated `sql`/`idempotent_sql` strings directly (e.g., a
test fixture applying `STATEMENTS` in order) hits the syntax error immediately.

### Solutions

1. **Emit plain `CREATE TYPE ... AS ENUM` in `sql`, and a `DO $$ ... EXCEPTION WHEN
   duplicate_object $$` block (or a catalog-guarded form) in `idempotent_sql`.**
   - Pros: both fields become truthful — `sql` is the canonical statement,
     `idempotent_sql` is actually idempotent and actually valid.
   - Cons: the `DO` block form is uglier to read in generated output.
2. **Emit plain `CREATE TYPE` in both fields and let the executor's catalog checks own
   idempotency.**
   - Pros: simplest emitter change.
   - Cons: `idempotent_sql` becomes a lie for enum statements unless the field is
     documented as "idempotent only under the generated executor"; consumers executing
     raw strings get non-idempotent behavior.

Option 1 is the correct fix: every emitted string should be executable as-is.

A regression test should exist that executes every emitted `sql` and `idempotent_sql`
string (including running `idempotent_sql` twice) against a real PostgreSQL, for a schema
that includes at least one enum. Per the red-green policy: write the failing test first.

## Problem 2: `--split-mode` catch-22 blocks `--lang python --mode query-layer` entirely

With v0.20.0:

- `pgdesign codegen --lang python --mode query-layer` (no `--split-mode`) fails with
  `invalid value '<nil>'`.
- `pgdesign codegen --lang python --mode query-layer --split-mode self-contained` fails
  with `--split-mode is only supported for Python DDL mode`.

There is no flag combination that reaches the query-layer mode: the flag is rejected when
present and defaulted to an invalid sentinel when absent. The query-layer feature is
shipped but unreachable from the CLI.

### Solutions

1. **Make `--split-mode` optional-and-absent for non-DDL modes: absence means "not
   applicable", presence with a non-DDL mode is a hard error (as today).**
   - Pros: matches the existing error message's intent; smallest change.
   - Cons: none apparent.
2. **Require `--split-mode` for all Python codegen modes and give query-layer a defined
   split behavior.**
   - Pros: uniform flag surface.
   - Cons: forces a meaning onto a mode that may not need splitting; more work.

Either way, a CLI-level test matrix over (`--lang`, `--mode`, `--split-mode` present/absent)
would have caught this — the sentinel `'<nil>'` leaking into flag validation suggests a
missing "unset" state in the flag model. Red-green: reproduce via a CLI invocation test
first.

## Problem 3 (guardrail): no freshness check for committed generated output

The adoption pattern "commit generated Python DDL, regenerate on TOML change" needs a CI
check that the committed output matches the current TOML — otherwise drift between TOML
and committed codegen output silently reappears (the exact problem codegen adoption is
supposed to kill). Consumers can hand-roll this (regenerate to a temp dir and diff), but
that requires byte-stable output as a documented guarantee and reimplements the same
logic in every consumer.

### Solutions

1. **`pgdesign codegen --check` (no file writes): regenerate in memory, diff against the
   files on disk, exit non-zero with a diff summary on mismatch.**
   - Pros: one flag, trivially wired into any CI; mirrors the established
     generate-then-verify pattern used elsewhere in the ecosystem (e.g., docs
     generators); makes byte-stability an explicit contract.
   - Cons: codegen output must be deterministic (stable ordering, no timestamps) — if it
     already is, this is cheap; if not, that determinism work is a prerequisite (and
     worth doing regardless, since committed generated files that churn on every run are
     unreviewable).
2. **Document byte-stability and leave the diff to consumers.**
   - Pros: zero pgdesign code.
   - Cons: every consumer reimplements the check; the determinism guarantee has no test
     backing it in pgdesign itself.

Option 1 is the correct solution.

## Problem 4 (guardrail): schemas can declare `ON DELETE CASCADE` into deny-mutation-trigger tables

pgdesign supports both per-FK `on_delete` actions and deny-mutation triggers (the
append-only immutability mechanism). A schema can currently declare
`on_delete = "CASCADE"` on an FK whose target-side delete would cascade INTO a table
protected by a deny-mutation trigger. That schema is internally contradictory: the
cascade can never succeed at runtime — the trigger rejects the cascaded DELETE, so the
original DELETE errors in a way the schema author clearly didn't intend. pgdesign
already warns about cascade blast radius during generation, but this specific
combination is not a blast-radius concern, it is a guaranteed runtime failure, and per
the house philosophy it should be a validation-time hard error, not a warning.

### Solutions

1. **Hard error at validation/generation time: any CASCADE (or SET NULL implying a write)
   path that reaches a deny-mutation table is rejected with a message naming the FK
   chain and the protected table.**
   - Pros: impossible-by-construction; catches the contradiction at design time instead
     of in production; consistent with hard-errors-not-warnings.
   - Cons: needs transitive cascade-path analysis (CASCade chains through intermediate
     tables), not just direct-FK inspection.
2. **Downgrade to a specific warning.**
   - Pros: cheaper.
   - Cons: warnings get ignored; the failure still lands at runtime.

Option 1 is the correct solution; the transitive analysis is the same graph walk the
existing blast-radius warning presumably already does.

## Affected areas (unverified paths — locate during implementation)

- Python DDL emitter (enum statement generation, `sql`/`idempotent_sql` fields)
- CLI flag model/validation for `codegen` (`--split-mode` unset state)
- Schema validation pass (cascade-path analysis against deny-mutation tables)
- New `--check` handling in the codegen command
- Test suite: emitted-SQL execution tests against real PostgreSQL, CLI flag matrix tests,
  determinism/freshness tests

## Effort estimate

- Problem 1 (enum DDL): small — emitter change plus execution-backed regression tests.
- Problem 2 (split-mode): small — flag-model fix plus CLI matrix tests.
- Problem 3 (--check): small-medium — mostly determinism verification plus a diff mode.
- Problem 4 (cascade guardrail): medium — transitive cascade analysis plus validation
  wiring and tests.

Problems 1 and 2 are independent quick fixes. Problem 3 is independent. Problem 4 is
independent. Nothing here blocks on anything else in this file; Problems 1 and 3
together are what unblock the consumer's codegen adoption.
