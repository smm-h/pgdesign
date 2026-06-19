# Test database scaffolding — prevent tests from destroying production data

## Problem

Every project that uses pgdesign + pytest faces the same risk: test fixtures connect to the production database and run destructive operations (DELETE, TRUNCATE) as part of test isolation. If the test config points at the production database — which is the path of least resistance — every pytest run wipes real data.

This happened in a consumer project: the test suite read the production config file, connected to the production database, and DELETE FROM'd every content table during test setup. A release hook ran pytest, which destroyed all data that had been ingested by a prior build. The 5GB of dead tuple bloat (before VACUUM) was the only evidence.

## Why this belongs in pgdesign

pgdesign owns the schema. It generates DDL. It scaffolds CI workflows and release hooks. It should also scaffold the test infrastructure that ensures production data is never touched by tests. Making each consumer solve this independently means most will get it wrong.

## Proposed solution

When `pgdesign scaffold` runs, it should generate test infrastructure alongside CI hooks:

### 1. A test database convention
The test database is always `<dbname>_test` (e.g., if the production database is `myapp`, tests use `myapp_test`). This convention is enforced, not optional.

### 2. A scaffolded test helper
Generate a `tests/pgdesign_fixtures.py` (or similar) that provides:
- A function to create the test database from the schema DDL (idempotent)
- A connection fixture that always points at `<dbname>_test`
- A guard that refuses to connect to a database without the `_test` suffix
- Teardown that drops or truncates the test database

### 3. A `pgdesign test` CLI command
- `pgdesign test setup` — creates `<dbname>_test`, applies DDL
- `pgdesign test teardown` — drops `<dbname>_test`
- `pgdesign test reset` — truncates all tables in the test database

### 4. CI workflow integration
The scaffolded CI workflow already creates a PG service container. It should also create the test database and apply the schema automatically. The test database name should match the convention.

## Design considerations

- The guard (refusing to connect without `_test` suffix) is the most important piece. Even if a consumer doesn't use the scaffolded fixtures, importing the guard into their conftest prevents accidental production connections.
- The test database should use the same schema as production (same DDL, same extensions, same triggers). `pgdesign test setup` should run `pgdesign generate --idempotent` against the test database.
- Consider whether the test helper should be Python-specific or language-agnostic. Since pgdesign supports codegen for 6 languages, a language-agnostic approach (CLI commands) plus language-specific fixture scaffolding (Python pytest, Go testing, etc.) would be most complete.
- The `_test` suffix convention should be documented and enforced by the CLI (hard error if someone tries to run `pgdesign test setup` against a database without the suffix).
