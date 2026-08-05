# pgdesign

PostgreSQL schema compiler. TOML schemas to SQL DDL with normal form auditing, migration generation, and schema visualization.

## Package dependency order

- `parse` parses TOML schemas (uses go-toml-edit for comment preservation)
- `model` builds the resolved intermediate representation (tables, views, materialized views); `Schema.Build()` resolves types and dependencies. The IR carries CANONICAL ORDERING: collections that are semantically ordered (columns, enum values, composite fields, function args, partition-key columns, FK column correspondence, index key columns) are slices; collections that are order-insensitive (checks, indexes, uniques, policies, triggers) are canonicalized so declaration order never affects identity
- `project` is the shared project-loading core: it reconciles a parsed schema + config `[[extensions]]` + user types + the vendored import surface + pg_version tiers into `(Schema, Registry, cfg)`. One loader for `build`/`codegen`/`revise`/`serve` — they can never disagree about what the project is
- `enc` is the canonical per-object encoder (Model-object -> canonical bytes); `decode` is its inverse (`decode∘enc = id` is a checked property). Every encoded form carries a CODEC-VERSION epoch; `id = hash(enc(x))`. Reflection-based field-coverage guards make encoder totality mechanically checked, not hoped
- `rev` is the whole-model canonical form and revision identity: `revision = hash` of a SORTED, KIND-QUALIFIED manifest (kind, schema, name, and for functions the argument signature). The `Revision` type is opaque and model-class-tagged (a type-carrying model and an introspected model without type info are different classes; comparing their revisions is a type error, not a runtime mismatch)
- `objstore` is the content-addressed object store: `id -> bytes`, puts idempotent, `get(put(x)) = x`. One package, multiple roots (`migrations/objects/`, `imports/<alias>/`)
- `chain` is the pure kernel of the migration algebra: revision manifests, the parent-linked edge graph (the free category on edges), and three-way typed invertibility (mechanically-invertible / declared-inverse / non-invertible). No Postgres, no CLI — property-tested
- `predicate` is the migration precondition IR: one structured definition with a Go executor (structured object/expected/found diagnostics) and a SQL renderer (DO-blocks for `generate --idempotent`), kept in lockstep by a CI conformance matrix
- `catalog` is the shared, scoped pg_catalog query layer used by both `introspect` and the predicate executor; version-conditional queries gate through `pgcap` (one version->capability truth) so a second divergent set of catalog queries cannot arise
- `livenorm` implements live round-trip normalization: it round-trips an expression through the TARGET database (throwaway temp object + `pg_get_*` deparse) so Postgres computes its own canonical form — resolving the catalog-dependent `≈_pg` residue that no pure normalizer can reach. Identity NEVER consumes round-trip output (no DB exists on the pure path)
- `pgcap` is the PostgreSQL version -> capability registry gating version-conditional behavior
- `modelgen` is a pure random generator of VALID Models (well-formed FK graphs, type closures, version-gated features) used as the input source for property-based kernel tests; `validate` is its validity oracle
- `imports` implements cross-repository schema imports: git pin resolution, surface vendoring via `objstore`, the lockfile, and column-level semantic drift detection via the normalizer
- `livestats` fetches live table statistics (row counts, scan ratios) for D2 heat/live annotations; callers pass the data in so `generate` stays DB-free
- `splitfmt` is a sealed, terminal file-format helper for carrying provenance-stamped multi-section output
- `validate` validates the model and detects anti-patterns; includes design intelligence checks (cascade analysis, constraint subsumption, dead columns, row size estimation, natural key surfacing); state machine reachability validation
- `generate` produces DDL and D2 diagram output; state machine trigger generation (BEFORE UPDATE) and D2 state diagrams. Emitters NEVER sort — canonical order is established in the IR (`model`), so output order is a property of the model, not of the emitter
- `audit` checks normal form compliance (1NF/2NF/3NF/BCNF) using functional dependencies; FD inference from PK/UNIQUE forces explicit declaration; Armstrong relation counterexamples in violation diagnostics
- `fd` provides functional dependency primitives (closure, minimal cover, candidate keys, BCNF decomposition, lossless-join verification, dependency-preservation checking, Armstrong relation generation)
- `discover` discovers functional dependencies from live data using the TANE algorithm
- `graph` provides generic `TopoSort[T]` used by model (table ordering), generate (view/matview/function ordering), and format
- `sqlexpr` parses and walks SQL expressions (recursive descent parser with 9 precedence levels, supports arithmetic, comparisons, boolean logic, casts, CASE, EXISTS subqueries); used by validate (E213) and codegen (expression-driven validators)
- `sqlparse` wraps wasilibs/go-pgquery (WASM-based PostgreSQL parser, no CGo) for proper SQL statement splitting and expression deparsing
- `sqlutil` provides shared adapter between sqlexpr and diagnostic for consistent parse-error-to-diagnostic conversion
- `codegen` generates type-safe application code (Go, TS, Java, Kotlin, Python, Zig) from the model; modes: validators, constants, types, enums, constraints, gorm, drizzle, sqlalchemy, jpa, graphql, ddl (Python), query-layer (Python); shared type mapping and enum generation across all 6 languages; state machine transition codegen for all languages; MultiFileGenerator interface for multi-file output
- `diff` compares two models or a model against a live database; state machine type changes detected and diffed. It also hosts the diff-time RENAME GATE: a plausible column/table rename (a drop+add whose definitions are content-equal except the name) is a HARD ERROR unless declared in `[renames]`
- `migrate` instantiates the `chain` algebra on disk and makes `apply` a real functor with a recoverable trace. Chain edges are one-file-per-edge under `migrations/chain/` (content-derived filenames); revision manifests under `migrations/revisions/`; superseded/rebased-away edges retire intact to `migrations/archive/`. `generate` is PURE (diffs the head manifest against the current model — never reads a database) and always emits large-table-safe forms. `apply` runs a deterministic path-finder from the database's chain position to the head, preconditions each op against the catalog, journals each op with its recorded inverse, and reconciles the result. `rollback` replays journaled inverses (never re-reads files). `squash` is composition — a consolidation edge whose op-list is the concatenation of the superseded path (no optimizer, no folding). `upgrade` adopts a legacy database onto the chain; `rebase` resolves a two-head fork; `baseline` adopts a drifted database without running SQL
- `introspect` reads a live database via pg_catalog into a model (uses the shared `catalog` query layer; filters pgdesign-managed objects)
- `seed` generates type-aware test data for schema tables; supports all 42 PG types, Zipf/log-normal distributions, regex generation from CHECK patterns, FK cycle handling (two-pass NULL then UPDATE), COPY and batch INSERT formats, edge-case boundary values, CHECK/UNIQUE constraint awareness
- `serve` exposes the HTTP API and web UI; runs DB-FREE in project-schema mode (pool optional). The `/schema` endpoint returns THE canonical envelope (revision + FKGraph projection + diagnostics) — literally the same function as the `json` output, so it can never drift. Binds `127.0.0.1` by default; the bind-override flag's help text states the server has NO authentication
- `diagnostic` provides error/warning/hint reporting used across all packages
- `typeinfo` provides structured PostgreSQL type representation; Type struct (Base, DomainName, Params), Parse() normalizes raw type strings to short canonical forms via alias map, Reconstruct() rebuilds SQL type strings for DDL, DomainName populated by build.go not Parse(); used by semtype, model, validate, generate, codegen, diff, migrate, seed, sql, introspect
- `semtype` defines the semantic type system (builtins + user-defined enums + scalar types that produce CREATE DOMAIN + composite types + state machine types with states/transitions/initial); type extends via `extends` field with per-kind merge semantics; Source field on TypeDef ("builtin", "user", "extended"); builtin shadowing with sealed field enforcement
- `risk` classifies migration risk levels
- `sql` contains SQL formatting utilities
- `format` handles output formatting
- `extregistry` validates PostgreSQL extension references
- `config` handles project configuration loading from pgdesign.toml
- `workload` analyzes query patterns and recommends indexes; structural tier (schema-only: JSONB/array/tsvector GIN, BRIN for append-only, boolean selectivity, excessive indexes, duplicate detection) and live tier (pg_stat_statements extraction, N+1 detection via call ratio cross-reference, sequential scan analysis)

The dependency flow is: parse -> model -> validate/generate/audit/diff/codegen -> migrate, typeinfo -> semtype/model/validate/generate/codegen/diff/migrate/seed/sql/introspect, sqlexpr -> validate/codegen/seed, sqlutil -> validate/codegen, sqlparse -> migrate/generate/introspect (and homes normalization — the go-pgquery leaf), graph -> model/generate/format, discover -> audit/check, seed -> generate, introspect -> serve, workload -> check/serve. The kernel layer sits under migrate and imports: enc -> rev -> chain; objstore backs chain and imports; project reconciles parse+config+imports for build/codegen/revise/serve; predicate (+ catalog + pgcap + livenorm) backs apply's preconditions and reconcile; modelgen feeds the property-based kernel tests. Views depend on tables; materialized views depend on tables and views; functions depend on tables (auto-detected for SQL-language functions); triggers depend on functions.

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
- `build` command reads `[output]` config sections from pgdesign.toml and generates all configured outputs (SQL, D2, JSON, SVG, doc, GraphQL, codegen). Outside a git repo it hard-errors under the default `--auto-commit` (escape: `--no-auto-commit`).
- `revise` command is the one-command project revision (roadmap 6.1): PURE tier (build outputs + chain-mode migration + BLOCKING normal-form/structural checks, committed in two commits via one shared commit helper) then the NON-RETROACTIVE DB tier (live FD discovery, pg_stat workload, live import verification). A DB-unreachable run keeps the pure commits and exits non-zero naming the skipped tier.
- `check` command runs registered checks via strictcli's check framework; `--tag` selects a check (`validation`, `nf`, `coverage`, `design`, `structural`, `workload`, `build`, `revision`, `imports`), `--all` runs all.
- `stats` command analyzes live database health: table sizes, index usage, bloat, duplicate indexes.
- `seed` command generates type-aware test data respecting FK dependencies and semantic types.
- Codegen supports six languages: Go, TypeScript, Java, Kotlin, Python, Zig. Modes: `validators` (RLS policy checkers), `constants` (table/column name strings), `types` (native struct/class/interface definitions with enums), `enums` (standalone enum definitions), `constraints` (client-side validation from CHECK/NOT NULL/enum), `gorm` (Go GORM struct tags), `drizzle` (TS Drizzle schema), `sqlalchemy` (Python SQLAlchemy 2.0 models), `jpa` (Java JPA entity classes), `ddl` (Python DDL tuples + executor), `query-layer` (Python protocols + PgBackend + InMemoryBackend).
- Diff supports three modes: `--live` (against database), `--against` (against another TOML), `--base` (against git ref).
- `doc` output format generates human-readable schema documentation.
- Extension-provided types (e.g., `vector` from pgvector) become valid base types when declared via `[[extensions]]` in pgdesign.toml. Undeclared extension types remain hard errors.
- Index definitions support `with = { key = "value" }` for PostgreSQL storage parameters (e.g., HNSW `m`, `ef_construction`). E216 validates parameters against index method.
- Views are defined under `[views.*]` with `query`, optional `comment`, and optional `depends_on` for dependency ordering.
- Materialized views are defined under `[materialized_views.*]` with `query`, optional `comment`, `with_data`, and nested `[materialized_views.*.indexes.*]`.
- Scalar types with CHECK constraints produce `CREATE DOMAIN` in the database. Columns reference the domain name instead of the base PG type. Per-column CHECKs are replaced by the domain's own constraint.
- Composite types are defined under `[types.*]` with `kind = "composite"` and `[types.*.fields]` subsection. Generates `CREATE TYPE ... AS (...)`.
- Exclusion constraints are defined under `[tables.*.exclusions.*]` with `columns`, `operators`, `method`, `where`, `deferrable`, `initially_deferred`. Validates btree_gist extension requirement. Unique constraints also support `deferrable`/`initially_deferred`.
- Functions are defined under `[functions.*]` with `language`, `returns`, `body`/`file` (SQL body inline or file-referenced), `args`, `volatility`, `parallel`, `security_definer`, `cost`, `rows`, `depends_on`. DDL uses `CREATE OR REPLACE FUNCTION` with `$pgdesign$` dollar-quoting. Signature changes (arg types, return type) require DROP + CREATE (Dangerous).
- Triggers are defined under `[tables.*.triggers.*]` with `function`, `events`, `timing`, `for_each`, `when`, `constraint`, `deferrable`, `initially_deferred`, `referencing_old`, `referencing_new`. Supports CONSTRAINT TRIGGER and transition tables (PG 10+).
- Standalone sequences are defined under `[sequences.*]` with `start`, `increment`, `min_value`, `max_value`, `cache`, `cycle`, `owned_by`, `comment`. Identity-backed sequences are filtered during introspection.
- Per-column `collation` for COLLATE in DDL. Per-column index collation. Per-column `statistics` for SET STATISTICS.
- Multi-column partition keys via `columns = ["year", "month"]` (backward compat with single `column`). pg_partman validated for single-column only.
- Range and multirange types (int4range, tsrange, daterange, etc.) are valid base types.
- BCNF audit (W103) alongside existing 3NF (W102). BCNF decomposition with lossless-join and dependency-preservation verification. Armstrong relation counterexamples in violation diagnostics. Minimal cover visualization (I100) when declared FDs have redundancy.
- FD inference from PK/UNIQUE constraints (A100). Inferred FDs must be explicitly declared in `[[dependencies]]` — hard error if undeclared.
- FD source tracking: `Source` field on FuncDep ("declared", "discovered", "inferred").
- `graphql` output format generates GraphQL SDL with types, relations, enums, custom scalars (DateTime, JSON).
- Function dependency auto-detection for `LANGUAGE sql` functions via go-pgquery AST walking. PL/pgSQL requires explicit `depends_on`.
- PGVersion resolution order: live database (introspect) > `[database].pg_version` in pgdesign.toml > `[meta].version` in schema TOML > 0 (conservative defaults).
- Generated columns: PG 12-17 only support STORED; PG 18+ supports both STORED and VIRTUAL. When `stored` is omitted from TOML, defaults to true. E218 validates version compatibility. STORED-to-VIRTUAL transition is destructive (DROP + recreate).
- RLS policies support PERMISSIVE (default) and RESTRICTIVE modes, FORCE ROW LEVEL SECURITY on table owners. Policies are diffed, migrated (ADD/DROP POLICY), and introspected from live databases. PG 10+ version gate for RESTRICTIVE. W011 warns when a table has RLS enabled but no policies for common operations; W012 warns about operation gaps in policy coverage.
- FK graph is built once during `Schema.Build()` and stored persistently on the Schema struct (`FKGraph` with forward/reverse adjacency maps, fan-in/fan-out counts). Cascade walkers (DFS depth, BFS breadth, BFS chain) power validate, codegen, and workload analysis.
- Design intelligence checks (`check --tag design`): W013 cascade depth exceeds threshold, W014 cascade breadth exceeds threshold, W015 mixed ON DELETE actions in cascade chain, I001 natural key candidate surfacing, W016 PK subsumes UNIQUE, W017 redundant NOT NULL check, W018 domain CHECK duplicates column CHECK, W019 range subsumption, I002 dead column detection (schema-only heuristic), I003 row size exceeds TOAST threshold, W021 excessive row size, I004 column reordering optimization.
- Migration chain model: migrations are content-addressed EDGES between revisions, one file per edge under `migrations/chain/` (content-derived filename = edge-content hash prefix + slug; a display sequence is derived from topology, never stored as identity). Revision manifests live under `migrations/revisions/`, object/op payloads under `migrations/objects/` (the content-addressed store), retired originals under `migrations/archive/`, and the rebase revision-remap table at `migrations/remap.json`. Applied history is recorded in the DATABASE, never inferred from files: `pgdesign_chain_position` (current revision + in-progress edge), `pgdesign_migration_ops` (the journal: op identity + serialized down-op), and the `pgdesign_applied_migrations` VIEW (version, applied_at, description, checksum). `migrate generate` is pure (always large-table-safe: NOT VALID + VALIDATE, backfill-then-set-not-null, expand/contract). `apply` = path-finder + precondition + journal + reconcile. `rollback` replays journaled inverses. `squash` = concatenation into a consolidation edge (the old op-list optimizer — inverse-pair cancellation, type merging, CREATE TABLE folding — is DELETED). Pre-upgrade databases (legacy tracking table, no chain position) hard-error every migrate subcommand pointing at `migrate upgrade`.
- Three-way typed invertibility (`chain`): every op is MECHANICALLY-INVERTIBLE, DECLARED-INVERSE (incl. vacuous DML inverses — data is not restored), or NON-INVERTIBLE. A composite's inverse is the reversed composition of component inverses, defined only when every component has one. Never infer invertibility from a net manifest delta (DROP populated column then ADD has an empty net delta and destroys data).
- Rename gate (`[renames]` in the schema TOML): `tables = [{ from, to }]` and `columns = [{ table, from, to }]`. A drop+add that is content-equal except the name is a plausible rename; detected-but-undeclared = HARD ERROR, ambiguous (multiple candidates) = hard error listing all, declared = a mechanically-invertible `rename_table`/`rename_column` op. Stale or definition-changing `[renames]` entries are validation errors. It is a pure, committed, CI-safe directive — NEVER part of any schema's identity.
- Revision and stamps (provenance, L6): the fully-resolved project hashes to ONE full-project revision (`rev`); every derived artifact is stamped with the producing revision via `pkg/genkit` (one writer, one reader). Freshness is extensional equality. `check --tag revision` enforces it.
- Writer taxonomy (six classes, total): FULL regenerators (`build`, `revise`) always allowed; PARTIAL writers (only `codegen --output`) refuse when non-rewritten siblings differ; SOURCE-EDITING writers (`fmt`) change the revision and print a follow-up notice; SCAFFOLDING writers (`testdb init`, `introspect --output`) are outside the invariant; STAMPED-UNENFORCED (`seed` output); and the CHECKER-COVERED append-only stores (migrations/chain/objects). Any future file-writing generate mode must register as full-or-banned.
- Cross-repository imports (`[imports]` in pgdesign.toml): `alias = { git, ref, schema }`. `alias:table` is valid ONLY in an FK `ref_table` (aliases anywhere else = hard error). `import lock` vendors the referenced surface + transitive type closure into `imports/<alias>/` with a lockfile (URL, ref, resolved commit, surface hash); `import update` re-pins. `check --tag imports` re-derives and reports column-level SEMANTIC drift via the normalizer (equivalently-spelled defaults do not false-drift). Imported tables live in `Schema.ImportedTables` (a split slice) — every consumer iterating `Tables` is fail-closed correct by omission; the union is wired only at enumerated resolution sites. DDL/audit/codegen emit ZERO imported artifacts.
- One normalizer (`≈_syn`): differ, predicates, upgrade reconcile, and shadow test all compute equivalence through the SAME normalization primitive (homed in `internal/sqlparse`, the go-pgquery leaf). Catalog-independent foldings live INSIDE the normalizer and apply to BOTH sides; the catalog-DEPENDENT residue (cast materialization) is resolved on live paths only, by `livenorm`'s round-trip through the target DB. Never rewrite one side only, and never stage catalog-independent foldings in the live-side residue mechanism.
- Workload analysis (`check --tag workload`): structural index recommendations W022 (JSONB no GIN), W023 (array no GIN), W024 (tsvector no GIN), I005 (append-only BRIN candidate); live analysis via pg_stat_statements for N+1 detection W025 (call ratio >= 100x), sequential scan detection W026 (seq_scan > 10x idx_scan); I006 boolean index low selectivity, I007 excessive indexes (10+).
- Seed generation: `--seed` for deterministic output, `--format copy|insert` (COPY 5-10x faster, psql-only), `--clean` emits TRUNCATE CASCADE, `--mode edge-cases` generates boundary values, `--counts table=N` for per-table row counts. Distributions: Zipf (s=1.5) for FK references, log-normal for money. Regex generation from CHECK patterns via regexp/syntax AST walk. FK cycles use two-pass NULL-then-UPDATE (S001 if cycle column is NOT NULL). Batch INSERT (1000 rows/statement). CHECK hint extraction for range/length/regex constraints, UNIQUE enforcement with 100 retries.
- State machines: `[types.*]` with `kind = "state_machine"`, `states`, `transitions`, and `initial`. Produces CHECK constraints for valid states, trigger-enforced transitions (BEFORE UPDATE), D2 state diagrams, and codegen transition methods. Reachability validation ensures all states are reachable from the initial state.
- Type extends: `extends = "parent_type_name"` on `[types.*]` for type derivation. Per-kind semantics: scalar (field overlay, sealed Kind + BaseType.Base), enum (additive values with E117 no-new-values warning), composite (additive fields with E118 collision error), state machine (additive states/transitions with E119 state collision, overridable initial/enforce_trigger/comment). Sealed fields (Kind, BaseType.Base) cannot change via extends or builtin shadowing (E114). Circular extends detected via topo sort (E115). Unknown extends target (E116). Builtin shadowing emits I101 info. Source field on TypeDef: "builtin", "user", or "extended". Resolution order: topological sort ensures parents load before children.
- Type matching uses typeinfo.Type.Base (normalized short form from alias map), DDL reconstruction uses typeinfo.Reconstruct() which returns DomainName if set or rebuilds from Base+Params. Domain resolution is via DomainName field populated by build.go during Schema.Build(), never by typeinfo.Parse().
- Table groups: `[groups]` in schema TOML maps group names to table lists. Config-based group filtering for selective code generation.
- Python DDL codegen: `--mode ddl --lang python`. Pure data tuples for DDL definitions plus a decomposed section executor for applying DDL programmatically.
- Python query-layer codegen: `--mode query-layer --lang python`. Generates Protocol definitions (Backend, per-table Writer/Reader), Row dataclasses, PgBackend (asyncpg parameterized queries), InMemoryBackend (constraint registry enforcement). Context+delegate+forwarding architecture avoids MRO complexity. Dual-backend conformance tested at codegen level.
- Constraint codegen for Python/Java/Kotlin: Python uses `_constraints.py` with ConstraintKind enum, per-table constraint lists, and ConstraintEngine class. Java and Kotlin generate constraint validators; Kotlin uses extension functions.
- MultiFileGenerator interface for codegen modes that produce multiple output files (Python DDL, Python query-layer). Returns `map[string][]byte` of relative file paths to contents.
- Consumer regeneration: when bumping the pgdesign version, consumers must regenerate all codegen output (run `pgdesign codegen` or `pgdesign build`) in the same PR as the version bump. Generated code is version-coupled to the pgdesign binary that produced it; stale output from a previous version may have different type annotations, import patterns, or executor structure. Use `pgdesign codegen --check` in CI to catch stale output.

## Testing

- Standard `testing.T`, no external frameworks or assertion libraries.
- Test fixtures live in `testdata/` subdirectories within each package.
- Run tests: `go test ./... -race -short -timeout=10m`
- Lint: `go vet ./...`

## CLI (strictcli)

- Commands registered via `app.Command(name, desc, handler, strictcli.WithArgs(...), strictcli.WithFlags(...))`
- Handler signature: `func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome`
- Global flags: `--project-config` (explicit pgdesign.toml override — a missing/unusable override is a hard error, never a silent fall back)
- RESERVED QUARTET: `--dry-run`, `--approve-consequential`, `--quiet` and `--verbose` are owned by strictcli, available on every command, and read off the Context (`ctx.DryRun()`, `ctx.Quiet()`, ...). Declaring any of them as a flag at any level is a registration-time hard error.
- EFFECT CLASSIFICATION: every command declares `strictcli.WithEffect(strictcli.EffectReadOnly)` or `...EffectMutating`; registration hard-errors without it. Commands whose act is worth interrupting someone for additionally declare `strictcli.WithConsequential()`, and the framework prompts for exactly those: `migrate apply`, `migrate rollback`, `migrate upgrade`, `migrate baseline`, `testdb teardown`, `testdb gc`. `cmd/pgdesign/classification_test.go` pins the whole table (total over the live command set, like `writersRegistry`) with the reasoning for every row.
- CONNECTION ENV: `PGDESIGN_DB` is the single connection env for every database-backed command and check, registered via `strictcli.WithConnectionEnv`. Every `--db`/`--live` flag binds to it via `ConnectionURLFlag` — an unbound DB-URL flag is a registration-time error. Under `--hermetic`, DB work resolves as absent and skips visibly instead of connecting.
- Commands: `revise`, `generate`, `check`, `fmt`, `introspect`, `diff`, `seed`, `serve`, `codegen`, `build`, `stats`
- Command groups: `migrate` (`plan`, `generate`, `apply`, `rollback`, `status`, `squash`, `rebase`, `test`, `baseline`, `upgrade`), `import` (`lock`, `update`), `testdb` (`setup`, `teardown`, `gc`, `init`)
- Checks (via strictcli's check framework): errors — `validation`, `build`, `revision`, `imports`; warnings — `nf`, `coverage`, `design`, `structural`, `workload`
- `introspect --extensions` discovers extension types, functions, and opclasses from a live database
- Removed flags (content-derived identity / pure generation): `migrate generate --version` (identity is content-derived), `migrate plan`/`generate --db` for generation (generation is pure). `--strict-nf` now also blocks BCNF (W103).

## Dependencies

- `go-toml-edit`: TOML parsing with comment preservation
- `strictcli`: CLI framework
- `pgx/v5`: PostgreSQL driver
- `d2`: diagram rendering (native Go library, no external binary)
- `go-pgquery`: WASM-based PostgreSQL parser (no CGo); used for SQL statement splitting, index definition parsing, and function dependency extraction

## Build

No Makefile or build scripts. Direct Go commands only:

- `go build ./cmd/pgdesign`
- `go test ./...`
- `go vet ./...`
