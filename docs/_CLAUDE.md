# pgdesign

PostgreSQL schema compiler. TOML schemas to SQL DDL with normal form auditing, migration generation, and schema visualization.

## Package dependency order

- `parse` parses TOML schemas (uses go-toml-edit for comment preservation)
- `model` builds the resolved intermediate representation; `Schema.Build()` resolves types and dependencies
- `validate` validates the model and detects anti-patterns
- `generate` produces DDL and D2 diagram output
- `audit` checks normal form compliance (1NF/2NF/3NF) using functional dependencies
- `fd` provides functional dependency primitives (closure, minimal cover, candidate keys)
- `sqlexpr` parses and walks SQL expressions; used by validate (E213) and codegen (expression-driven validators)
- `codegen` generates type-safe application code (Python, Zig) from the model
- `diff` compares two models or a model against a live database
- `migrate` generates migrations with risk classification and safety linting
- `introspect` reads a live database via pg_catalog into a model
- `serve` exposes the HTTP API and web UI
- `diagnostic` provides error/warning/hint reporting used across all packages
- `semtype` defines the semantic type system (builtins + user-defined enums)
- `risk` classifies migration risk levels
- `sql` contains SQL formatting utilities
- `format` handles output formatting
- `extregistry` validates PostgreSQL extension references

The dependency flow is: parse -> model -> validate/generate/audit/diff/codegen -> migrate, sqlexpr -> validate/codegen, and introspect -> serve.

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

## Testing

- Standard `testing.T`, no external frameworks or assertion libraries.
- Test fixtures live in `testdata/` subdirectories within each package.
- Run tests: `go test ./... -race -short -timeout=10m`
- Lint: `go vet ./...`

## CLI (strictcli)

- Commands registered via `app.Command(name, desc, handler, strictcli.WithArgs(...), strictcli.WithFlags(...))`
- Handler signature: `func(kwargs map[string]interface{}) int` (returns exit code)
- Global flags: `quiet`, `db` (PostgreSQL connection string), `strict-nf`

## Dependencies

- `go-toml-edit`: TOML parsing with comment preservation
- `strictcli`: CLI framework
- `pgx/v5`: PostgreSQL driver
- `d2`: diagram rendering (native Go library, no external binary)

## Build

No Makefile or build scripts. Direct Go commands only:

- `go build ./cmd/pgdesign`
- `go test ./...`
- `go vet ./...`
