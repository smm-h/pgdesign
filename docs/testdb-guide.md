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

`pgdesign testdb init` generates one file per language. Each file is self-contained -- it implements the testdb protocol v1 contract:

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

The `[database].url` field in `pgdesign.toml` is baked into the generated wrappers as the default base URL (used when `PGDESIGN_DB` is not set).

The `[output]` section determines which SQL output provides the `.split.json` file. If you have multiple SQL outputs, use `--output <name>` during `testdb init` to specify which one.

```toml
[database]
url = "postgres://localhost:5432/myapp?sslmode=disable"

[output.ddl]
format = "sql"
path = "schema.sql"
```

## CLI Commands

The CLI commands are for manual use, scripting, and CI pipelines. The generated wrappers handle these operations automatically during tests.

### testdb setup

Create an ephemeral database and print its connection URL to stdout.

```
pgdesign testdb setup --db "postgres://localhost:5432/myapp" --ddl schema.sql
```

Output: `postgres://localhost:5432/myapp_test_1750000000_a3k9m2x1?sslmode=disable`

### testdb teardown

Drop an ephemeral database by its connection URL.

```
pgdesign testdb teardown --db "postgres://localhost:5432/myapp_test_1750000000_a3k9m2x1"
```

### testdb gc

Clean up orphaned ephemeral databases older than a given duration. Orphans can accumulate when tests crash without running teardown.

```
pgdesign testdb gc --db "postgres://localhost:5432/myapp" --older-than 2h
```

### testdb init

Generate test wrapper files for one or more languages.

```
pgdesign testdb init --language python --language go
pgdesign testdb init --language java --force          # overwrite existing
pgdesign testdb init --language ts --output ddl       # specify SQL output section
```

## CI Integration

The generated wrappers need only a running PostgreSQL server and the `PGDESIGN_DB` environment variable. In CI, provide a Postgres service container and set the variable.

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

Add a post-test step to clean up any orphaned databases from crashed runs:

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
