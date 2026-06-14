# pgdesign

A PostgreSQL schema compiler. Declarative schema definitions in TOML, compiled to SQL DDL with strict enforcement of database design principles.

## Installation

### Go

```
go install github.com/smm-h/pgdesign/cmd/pgdesign@latest
```

### npm

```
npm install pgdesign
```

### pip

```
pip install pgdesign
```

## Commands

| Command | Description |
|---------|-------------|
| `generate` | Generate SQL from schema file(s) or directory |
| `validate` | Validate schema file(s) for errors and warnings |
| `audit` | Audit schema for normal form violations |
| `fmt` | Format schema file(s) or directory |
| `introspect` | Introspect a live PostgreSQL database |
| `diff` | Diff schema against a live database, another TOML, or a git ref |
| `seed` | Generate type-aware test data |
| `codegen` | Generate application code (Go, TS, Java, Kotlin, Python, Zig) |
| `build` | Generate all configured outputs from pgdesign.toml |
| `stats` | Database statistics and health analysis |
| `serve` | Start the pgdesign HTTP API server |
| `migrate plan` | Preview migration operations |
| `migrate generate` | Generate migration files from schema changes |
| `migrate apply` | Apply pending migrations |
| `migrate rollback` | Roll back the last migration |
| `migrate status` | Show migration status |
| `migrate squash` | Squash a range of migrations into one |
| `migrate test` | Test migrations against a staging database |

## Documentation

[pgdesign.smmh.dev](https://pgdesign.smmh.dev)
