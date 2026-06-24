# testdb teardown should refuse to drop databases without the _test_ infix

## Problem

`testdb teardown` and the internal `Drop()`/`DropByName()` functions accept any database name and issue `DROP DATABASE`. There is no validation that the target database matches the `_test_` naming convention enforced by `testdb setup`.

If a user (or an AI agent) passes a production database name to `testdb teardown`, it drops it silently. This is the exact class of bug that test database scaffolding was designed to prevent — the destructive operation has no guard against targeting the wrong database.

## The asymmetry

- **Creation** (`testdb setup`): always generates names with `_test_` infix. Convention is enforced.
- **Destruction** (`testdb teardown`): accepts any name. Convention is NOT enforced.

This means the safety guarantee is one-sided. A consumer who uses `testdb setup` correctly is protected. But a consumer who passes a raw database name to `teardown` (e.g., from a config file, an env var, or a script argument) has no safety net.

## Proposed fix

`Drop()` and `DropByName()` in `internal/testdb/testdb.go` should validate that the database name contains the `_test_` infix before issuing `DROP DATABASE`. If the name doesn't match, hard error with a message like:

> "refusing to drop database 'myapp': name does not contain '_test_'. Only test databases created by `pgdesign testdb setup` can be dropped. If this is intentional, drop it directly with `DROP DATABASE myapp`."

This follows the "hard errors, not warnings" and "no escape hatches" principles. The user can always use raw SQL to drop a non-test database — pgdesign just shouldn't make it easy to do accidentally.

The `gc` command should apply the same guard — only clean up databases matching the `{base}_test_*` pattern (it already does this for discovery, but the actual drop should also validate).

## Affected code

- `internal/testdb/testdb.go`: `Drop()` (~line 192), `DropByName()` (~line 240)
- `internal/testdb/testdb.go`: `GarbageCollect()` (~line 315) — verify the drop path validates
