# pgdesign

PostgreSQL schema compiler. TOML schemas to SQL DDL with normal form auditing, migration generation, and schema visualization.

## Package dependency order

- `parse` parses TOML schemas (uses go-toml-edit for comment preservation)
- `model` builds the resolved intermediate representation (tables, views, materialized views); `Schema.Build()` resolves types and dependencies
- `validate` validates the model and detects anti-patterns
- `generate` produces DDL and D2 diagram output
- `audit` checks normal form compliance (1NF/2NF/3NF) using functional dependencies
- `fd` provides functional dependency primitives (closure, minimal cover, candidate keys)
- `discover` discovers functional dependencies from live data using the TANE algorithm
- `sqlexpr` parses and walks SQL expressions (recursive descent parser with 9 precedence levels, supports arithmetic, comparisons, boolean logic, casts, CASE, EXISTS subqueries); used by validate (E213) and codegen (expression-driven validators)
- `sqlparse` wraps wasilibs/go-pgquery (WASM-based PostgreSQL parser, no CGo) for proper SQL statement splitting and expression deparsing
- `sqlutil` provides shared adapter between sqlexpr and diagnostic for consistent parse-error-to-diagnostic conversion
- `codegen` generates type-safe application code (Go, TS, Java, Kotlin, Python, Zig) from the model
- `diff` compares two models or a model against a live database
- `migrate` generates migrations with risk classification and safety linting
- `introspect` reads a live database via pg_catalog into a model
- `seed` generates type-aware test data for schema tables
- `serve` exposes the HTTP API and web UI
- `diagnostic` provides error/warning/hint reporting used across all packages
- `semtype` defines the semantic type system (builtins + user-defined enums)
- `risk` classifies migration risk levels
- `sql` contains SQL formatting utilities
- `format` handles output formatting
- `extregistry` validates PostgreSQL extension references
- `config` handles project configuration loading from pgdesign.toml

The dependency flow is: parse -> model -> validate/generate/audit/diff/codegen -> migrate, sqlexpr -> validate/codegen, sqlutil -> validate/codegen, sqlparse -> migrate/generate, discover -> audit/check, seed -> generate, introspect -> serve. Views depend on tables; materialized views depend on tables and views.

## Key conventions

- All columns are NOT NULL by default; nullable is opt-in.
- Foreign keys require an explicit `on_delete` clause.
- All tables require a comment.
- Use `diagnostic.Diagnostics` for errors and warnings, not Go errors. Check `.HasErrors()`, not `!= nil`.
- Tables are always provided in dependency order via `Schema.TableOrder()`.
- Cycle-safe DDL: circular FK references are created without the FK, then ALTERed to add constraints.
- Non-transactional DDL: `CONCURRENTLY` and `ALTER TYPE ADD VALUE` operations execute outside transactions.
- Advisory locks prevent concurrent migration execution.
- RLS policies are defined per-table with USING/WITH CHECK expressions, error codes, and error messages.
- Array columns use `array = true` modifier; DDL appends `[]` to the base type.
- Warning suppression via `[suppress]` in pgdesign.toml with mandatory reason strings.
- Append-only tables generate BEFORE UPDATE OR DELETE triggers via `append_only = true`.
- JSONB shape validation via `json_schema` column attribute referencing an external JSON Schema file.
- E109 validates enum defaults against declared values; E110 catches embedded SQL quotes in all defaults.
- `build` command reads `[output]` config sections from pgdesign.toml and generates all configured outputs (SQL, D2, JSON, SVG, doc, codegen).
- `check` command runs registered checks (validation, NF audit, coverage) via strictcli's check framework.
- `stats` command analyzes live database health: table sizes, index usage, bloat, duplicate indexes.
- `seed` command generates type-aware test data respecting FK dependencies and semantic types.
- Codegen supports six languages: Go, TypeScript, Java, Kotlin, Python, Zig. Three modes: validators, constants, types.
- Diff supports three modes: `--live` (against database), `--against` (against another TOML), `--base` (against git ref).
- `doc` output format generates human-readable schema documentation.
- Extension-provided types (e.g., `vector` from pgvector) become valid base types when declared via `[[extensions]]` in pgdesign.toml. Undeclared extension types remain hard errors.
- Index definitions support `with = { key = "value" }` for PostgreSQL storage parameters (e.g., HNSW `m`, `ef_construction`). E216 validates parameters against index method.
- Views are defined under `[views.*]` with `query`, optional `comment`, and optional `depends_on` for dependency ordering.
- Materialized views are defined under `[materialized_views.*]` with `query`, optional `comment`, `with_data`, and nested `[materialized_views.*.indexes.*]`.
- Codegen supports `--mode types` for generating native type definitions from the schema in all 6 languages, in addition to `validators` and `constants`.
- PGVersion resolution order: live database (introspect) > `[database].pg_version` in pgdesign.toml > `[meta].version` in schema TOML > 0 (conservative defaults).
- Generated columns: PG 12-17 only support STORED; PG 18+ supports both STORED and VIRTUAL. When `stored` is omitted from TOML, defaults to true. E218 validates version compatibility. STORED-to-VIRTUAL transition is destructive (DROP + recreate).

## Testing

- Standard `testing.T`, no external frameworks or assertion libraries.
- Test fixtures live in `testdata/` subdirectories within each package.
- Run tests: `go test ./... -race -short -timeout=10m`
- Lint: `go vet ./...`

## CLI (strictcli)

- Commands registered via `app.Command(name, desc, handler, strictcli.WithArgs(...), strictcli.WithFlags(...))`
- Handler signature: `func(kwargs map[string]interface{}) int` (returns exit code)
- Global flags: `quiet`
- `--db` and `--strict-nf` are per-command flags (on check, generate, introspect, diff, serve, stats, migrate subcommands).
- Commands: `generate`, `check`, `fmt`, `introspect`, `diff`, `seed`, `serve`, `codegen`, `build`, `stats`
- Command groups: `migrate` (`plan`, `generate`, `apply`, `rollback`, `status`, `squash`, `test`)
- `introspect --extensions` discovers extension types, functions, and opclasses from a live database

## Dependencies

- `go-toml-edit`: TOML parsing with comment preservation
- `strictcli`: CLI framework
- `pgx/v5`: PostgreSQL driver
- `d2`: diagram rendering (native Go library, no external binary)
- `go-pgquery`: WASM-based PostgreSQL parser (no CGo); used for SQL statement splitting

## Build

No Makefile or build scripts. Direct Go commands only:

- `go build ./cmd/pgdesign`
- `go test ./...`
- `go vet ./...`
