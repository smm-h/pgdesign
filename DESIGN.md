# pgdesign

Production-grade PostgreSQL schema compiler with strict enforcement, normal form auditing, declarative migrations, and schema visualization.

## Architecture

```
pgdesign/
  cmd/pgdesign/          CLI entry point (strictcli)
  internal/
    diagnostic/          Shared error/warning types with stable codes
    semtype/             Semantic type system (builtins + user + enum)
    parse/               TOML parser via go-toml-edit AST walk
    model/               Resolved IR + Build() pipeline (resolve -> order -> enrich)
    fd/                  Functional dependency algorithms (closure, minimal cover, candidate keys)
    format/              Canonical TOML formatter (pgdesign fmt)
    validate/            Strict validation rules + anti-pattern detection
    sql/                 Shared SQL builder (quoting, statements, naming)
    generate/            DDL generator (IR -> SQL/D2/JSON)
    audit/               Normal form auditor (1NF, 2NF, 3NF + Bernstein decomposition)
    discover/            TANE algorithm for FD discovery from live data
    risk/                Shared risk classification for schema operations
    extregistry/         PG extension capability registry
    introspect/          Live DB -> IR via pg_catalog
    diff/                Two-IR comparison with risk classification
    migrate/             Migration generation, apply, rollback, safety linting
    serve/               HTTP server + web UI (JSON API, D2 diagrams, stats)
  migrations/            Generated migration files (committed to git)
  testdata/              Golden-file test cases
  docs/                  Generated documentation (selfdoc)
  npm/                   npm binary wrapper
  pypi/                  PyPI binary wrapper
```

## Core data flow

```
TOML files -> parse/ -> model.Build() -> validate/ -> generate/ (DDL)
                                      -> audit/    (NF findings)
                                      -> diff/     (vs introspected live DB)
                                                      -> migrate/ (generate ops)

Live DB -> introspect/ -> model.Schema -> diff/ (compare to desired)
                                       -> serve/ (JSON API + visualization)
```

## Key design decisions

- TOML schema files are the source of truth (declarative)
- Migrations are always generated as committed files with mandatory down ops
- All columns NOT NULL by default (nullable opt-in)
- FK requires on_delete, table requires comment, auto-index on FKs
- Canonical formatting via `pgdesign fmt` (configurable ordering, eliminates git conflicts)
- Safety linting: dangerous ops flagged with lock types, risk levels, safe alternatives
- Non-transactional DDL (CONCURRENTLY, ALTER TYPE ADD VALUE) auto-detected and executed outside transactions
- Advisory lock prevents concurrent migration execution
- Cycle-safe DDL: CREATE TABLE without FK, then ALTER TABLE ADD CONSTRAINT
- Extension registry validates opclass/type references against declared extensions
- D2 library for native SVG diagram rendering (no external binary)
- pgx/v5 for PostgreSQL connectivity

## Project config (pgdesign.toml)

```toml
[project]
schemas = ["auth.toml", "game.toml"]
migrations_dir = "migrations"

[database]
url = ""  # set via --db flag or environment variable
pg_version = 18  # target PostgreSQL major version (controls conditional syntax)

[format]
file_granularity = "per_schema"
table_order = "dependency"
column_order = "pk_fk_alpha"

[validate]
disable = []
naming_pattern = "snake_case"
max_columns = 30

[migrate]
lock_timeout = "5s"
auto_concurrent_threshold = 10000
expand_contract_threshold = 10000000

[[extensions]]
# User-defined extension capabilities (supplements built-in registry)
```

## Dependencies

- github.com/smm-h/go-toml-edit (TOML parsing, comment-preserving editing)
- github.com/smm-h/strictcli/go/strictcli (CLI framework)
- github.com/jackc/pgx/v5 (PostgreSQL driver)
- oss.terrastruct.com/d2 (diagram rendering)

## Distribution

- Go binary (primary): `go install github.com/smm-h/pgdesign@latest`
- npm wrapper: `npm install pgdesign` (downloads Go binary)
- PyPI wrapper: `pip install pgdesign` (downloads Go binary)
- Release management: rlsbl + goreleaser
