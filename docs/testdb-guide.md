---
title: "Ephemeral Test Databases"
description: "How to use pgdesign's testdb feature to create isolated, ephemeral databases for testing — preventing tests from touching production data."
---

# Ephemeral Test Databases

pgdesign can create isolated, throwaway databases for your test suite. Each test run gets a fresh database built from your schema DDL. The database is dropped when the test finishes. Production credentials are never involved.

## The Problem

Tests that connect to shared or production databases are a ticking time bomb. The failure mode is not hypothetical: a test suite reads production config from the environment, connects to the production database, and runs `DELETE FROM` on every content table. No confirmation, no safety net. The data is gone.

Even short of total destruction, shared test databases cause flaky tests (parallel runs collide on the same rows), leak state between tests (one test's INSERT affects another's SELECT count), and create pressure to "clean up" with TRUNCATE statements that mask real bugs.

The fix is not more careful cleanup. The fix is isolation: every test run gets its own database, created from scratch, destroyed automatically.

## How It Works

1. `pgdesign build` compiles your TOML schema into `schema.sql` and a companion `schema.sql.split.json` file containing pre-split SQL statements.
2. The test wrapper (generated per language) reads `schema.sql.split.json`, connects to a PostgreSQL server, creates a randomly-named database (e.g., `myapp_test_1750000000_a3k9m2x1`), and applies all DDL statements.
3. Your tests run against the ephemeral database.
4. When the test completes (pass or fail), the wrapper drops the database.

The wrapper connects to the `postgres` maintenance database to issue `CREATE DATABASE` and `DROP DATABASE`. Your tests then connect to the ephemeral database. Production databases are never referenced.

## Quick Start

### 1. Generate the DDL

```
pgdesign build
```

This reads `pgdesign.toml`, compiles your schema, and writes (among other outputs) the SQL file and its `.split.json` companion. The `.split.json` file is what the test wrappers consume -- it contains individually executable SQL statements.

### 2. Generate test wrappers

```
pgdesign testdb init --language python --language go
```

This generates standalone wrapper files for each requested language. The wrappers implement the full lifecycle (create, apply DDL, teardown) and require no pgdesign binary at test runtime.

### 3. Use in tests

The following examples show how to use the generated test wrappers in Python and Go. Each wrapper provides a fixture or helper function that creates an ephemeral database, applies your schema DDL, provides a connection for your test to use, and automatically drops the database when the test completes. The database is isolated per test run, so parallel test execution works without conflicts, and crashed tests leave orphaned databases that the gc command cleans up.

**Python (pytest):**

```python
# The pgdesign_db fixture is defined in tests/pgdesign_testdb.py
# Import it or add tests/ to your conftest.py

def test_user_creation(pgdesign_db):
    pgdesign_db.execute(
        "INSERT INTO users (name, email) VALUES (%s, %s)",
        ("Alice", "alice@example.com"),
    )
    pgdesign_db.commit()

    row = pgdesign_db.execute("SELECT name FROM users").fetchone()
    assert row[0] == "Alice"
```

**Go:**

```go
import "yourproject/internal/testdb"

func TestUserCreation(t *testing.T) {
    db := testdb.SetupTestDB(t)
    // db.Conn is a *pgx.Conn, automatically cleaned up via t.Cleanup

    _, err := db.Conn.Exec(context.Background(),
        "INSERT INTO users (name, email) VALUES ($1, $2)",
        "Alice", "alice@example.com",
    )
    if err != nil {
        t.Fatal(err)
    }
}
```

## Generated Wrappers

`pgdesign testdb init` generates one self-contained file per requested language that implements the full testdb protocol v1 contract without requiring the pgdesign binary at test runtime. Each generated wrapper handles the complete lifecycle of creating an ephemeral database, applying DDL from the pre-split JSON file, providing connection details to test code, and dropping the database on completion. The only runtime dependency is the language's PostgreSQL driver.

1. **Create**: connect to the `postgres` maintenance database and `CREATE DATABASE` with a randomly-generated name.
2. **Apply DDL**: read `schema.sql.split.json`, connect to the new database, execute each statement in order.
3. **Teardown**: terminate any remaining connections and `DROP DATABASE`.

The wrappers are standalone. They read the `.split.json` file at test time but do not shell out to `pgdesign` or require it to be installed. The only runtime dependency is the language's PostgreSQL driver.

### Output paths

| Language | Output path |
|----------|-------------|
| Go | `internal/testdb/pgdesign_testdb.go` |
| Python | `tests/pgdesign_testdb.py` |
| TypeScript | `test/pgdesign-testdb.ts` |
| Java | `src/test/java/pgdesign/TestDB.java` |
| Kotlin | `src/test/kotlin/pgdesign/TestDB.kt` |
| Zig | `src/test/pgdesign_testdb.zig` |

Files are generated once and checked into your repository. Re-run `pgdesign testdb init --language <lang> --force` to regenerate after upgrading pgdesign.

### Database naming

Ephemeral databases are named `{base}_test_{timestamp}_{random}`, where `{base}` comes from the database name in your connection URL, `{timestamp}` is a Unix epoch, and `{random}` is 8 alphanumeric characters. The total length never exceeds PostgreSQL's 63-byte identifier limit. This naming convention allows the `gc` command to identify and clean up orphaned databases.

## Configuration

### PGDESIGN_DB environment variable

All generated wrappers check `PGDESIGN_DB` first. If set, it overrides the baked-in base URL. This is the primary mechanism for pointing tests at a different PostgreSQL server (e.g., CI service containers vs local dev).

```bash
# Local development
export PGDESIGN_DB="postgres://localhost:5432/myapp?sslmode=disable"

# CI (GitHub Actions)
PGDESIGN_DB="postgres://postgres:postgres@localhost:5432/myapp?sslmode=disable"
```

### pgdesign.toml

The `[database].url` field in `pgdesign.toml` is baked into the generated wrappers as the default base URL, used when the `PGDESIGN_DB` environment variable is not set. This means wrappers work out of the box for local development where the database URL is stable, while CI environments can override the URL via the environment variable to point at service containers or dedicated test servers without modifying the generated wrapper code.

The `[output]` section determines which SQL output provides the `.split.json` file. If you have multiple SQL outputs, use `--output <name>` during `testdb init` to specify which one.

```toml
[database]
url = "postgres://localhost:5432/myapp?sslmode=disable"

[output.ddl]
format = "sql"
path = "schema.sql"
```

## CLI Commands

The CLI commands provide manual access to the testdb lifecycle operations for scripting, debugging, and CI pipeline integration. While the generated wrappers handle these operations automatically during normal test execution, the CLI commands are useful for creating databases for manual inspection, cleaning up orphaned databases after CI failures, and integrating with build systems that need explicit control over the database lifecycle.

### testdb setup

Create an ephemeral database on the specified PostgreSQL server and print its connection URL to stdout. The command connects to the maintenance database, creates a new database with a unique timestamped name, applies the DDL from the specified SQL file, and returns the connection URL for the new database. This is useful for scripting scenarios where you need to create a database outside of the generated test wrappers.

```
pgdesign testdb setup --db "postgres://localhost:5432/myapp" --ddl schema.sql
```

Output: `postgres://localhost:5432/myapp_test_1750000000_a3k9m2x1?sslmode=disable`

### testdb teardown

Drop an ephemeral database by its connection URL, terminating any remaining active connections before issuing the DROP DATABASE command. This ensures clean removal even when test code crashed without properly closing its database connections. The command validates that the target database name matches the pgdesign test naming pattern to prevent accidental deletion of non-test databases.

```
pgdesign testdb teardown --db "postgres://localhost:5432/myapp_test_1750000000_a3k9m2x1"
```

### testdb gc

Clean up orphaned ephemeral databases older than a specified duration by scanning the PostgreSQL server for databases matching the pgdesign test naming pattern and dropping those that exceed the age threshold. Orphaned databases accumulate when test processes crash, are killed, or fail to run their teardown logic. The gc command extracts the creation timestamp from each database name and compares it against the `--older-than` duration to determine which databases to drop.

```
pgdesign testdb gc --db "postgres://localhost:5432/myapp" --older-than 2h
```

### testdb init

Generate test wrapper files for one or more target programming languages. Each wrapper is a self-contained file that implements the full testdb lifecycle using the language's standard PostgreSQL driver and test framework conventions. Use `--force` to regenerate existing wrappers after a pgdesign upgrade, and `--output` to specify which SQL output section provides the DDL when your pgdesign.toml defines multiple SQL outputs.

```
pgdesign testdb init --language python --language go
pgdesign testdb init --language java --force          # overwrite existing
pgdesign testdb init --language ts --output ddl       # specify SQL output section
```

## CI Integration

The generated wrappers need only a running PostgreSQL server and the `PGDESIGN_DB` environment variable pointing to it. In CI environments, provide a PostgreSQL service container via your CI provider's service configuration and set the environment variable to the container's connection URL. The wrappers handle all database lifecycle operations automatically, including creation, DDL application, and cleanup, so your CI pipeline only needs to start Postgres and set the URL.

### GitHub Actions

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_PASSWORD: postgres
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U postgres"
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
    env:
      PGDESIGN_DB: "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
    steps:
      - uses: actions/checkout@v4
      - name: Run tests
        run: pytest  # or: go test ./...
```

The wrappers handle everything else: creating the ephemeral database, applying DDL, running your tests against it, and dropping it on completion.

### Garbage collection in CI

Add a post-test step with `if: always()` to clean up any orphaned ephemeral databases from test runs that crashed or were cancelled. This step runs regardless of whether the test step passed or failed, ensuring that orphaned databases do not accumulate across CI runs. The `--older-than` flag should be set shorter than your CI cache duration to prevent stale databases from consuming server resources.

```yaml
      - name: Cleanup orphaned test databases
        if: always()
        run: pgdesign testdb gc --db "$PGDESIGN_DB" --older-than 30m
```

## Supported Languages

| Language | Driver | Test framework | Output path |
|----------|--------|----------------|-------------|
| Go | pgx/v5 | `testing.T` with `t.Cleanup` | `internal/testdb/pgdesign_testdb.go` |
| Python | psycopg 3 | pytest fixture (`session` scope) | `tests/pgdesign_testdb.py` |
| TypeScript | pg (node-postgres) | Framework-agnostic (`setupTestDB` / `teardown`) | `test/pgdesign-testdb.ts` |
| Java | JDBC (DriverManager) | JUnit 5 extension (`BeforeAll` / `AfterAll`) | `src/test/java/pgdesign/TestDB.java` |
| Kotlin | JDBC (DriverManager) | JUnit 5 extension (`BeforeAll` / `AfterAll`) | `src/test/kotlin/pgdesign/TestDB.kt` |
| Zig | pg (zig-pg) | `std.testing` with manual `setup` / `teardown` | `src/test/pgdesign_testdb.zig` |
