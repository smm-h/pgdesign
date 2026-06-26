# Generated test fixtures should guard against connecting to non-test databases

## Problem

The testdb teardown guard (v0.16.0) prevents DROP on databases without the `_test_` infix. But the generated Python/Go/TS test fixtures have no **connection guard** — they don't validate the database name before connecting and yielding a connection to test code.

If a consumer misconfigures the `PGDESIGN_DB` env var or the baked-in `BASE_URL` to point at a production database, the fixture connects to it, yields the connection, and test code runs DELETEs/TRUNCATEs against production data. The ephemeral naming convention makes this unlikely (the fixture creates its own DB with `_test_` in the name), but the `PGDESIGN_DB` override is a direct path to misconfiguration.

## The gap

- **Teardown guard**: refuses to DROP databases without `_test_` ✓
- **Connection guard**: does not exist ✗

A consumer's test fixture could connect to `myapp` (production), run `DELETE FROM users`, and the teardown guard wouldn't fire because the fixture didn't call `Drop()` on the production DB — it called `Drop()` on the ephemeral DB, which succeeds fine.

## Proposed fix

In all 6 language templates (`internal/testdb/templates/*.tmpl`), add a guard in the `pgdesign_db` / `pgdesign_db_url` fixtures that validates the ephemeral database name contains `_test_` before yielding:

Python example:
```python
assert "_test_" in db_name, (
    f"refusing to connect to database '{db_name}': "
    f"name does not contain '_test_'. This guard prevents "
    f"test fixtures from accidentally targeting a production database."
)
```

This runs after the ephemeral DB is created but before the connection is yielded to test code. Since the fixture itself generates the name via `_generate_name()` (which always includes `_test_`), this assertion is structurally always true — it's a safety net for when `PGDESIGN_DB` or the base URL is misconfigured.
