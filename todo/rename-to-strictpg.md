# Rename pgdesign to strictpg

## Background

The project is currently called "pgdesign" but the name doesn't match what it actually is. The tool is a PostgreSQL schema compiler with strict enforcement -- NOT NULL by default, mandatory comments, hard errors instead of warnings, no escape hatches, mandatory on_delete, NF auditing. "Design" implies interactive, creative, exploratory. The actual UX is writing declarative TOML and getting compiler errors.

## The naming debate

Ten independent agents studied the codebase and voted:

- **strictpg (4 votes):** The tool's defining trait is enforcement. 28+ validation rules are hard errors, not suggestions. Bans varchar, timestamp-without-tz, serial, float-for-money. "pgdesign" oversells interactivity and undersells the strictness that differentiates this tool. Brand coherence with strictcli.
- **pgdesign (6 votes):** "Strict" describes one quality; the tool also does codegen (6 languages), migrations, introspection, seeding, diffing, visualization, web API. Naming after enforcement undersells breadth. The `pg[noun]` prefix matches the PostgreSQL ecosystem convention. "strictpg" is vague -- strict what?

Counterarguments to the pgdesign majority:
- "strictpg is vague" is weak -- pgdesign is equally vague ("design what?"). Both require reading docs.
- "Strict" carries more signal than "design" -- it communicates personality and differentiation immediately.
- strictcli is a full CLI framework with many features, yet nobody thinks it's "just a linter" because of the name.
- The GCC/"strictc" analogy (naming GCC after -Wall -Werror) doesn't hold -- GCC's strictness is optional and off by default. pgdesign's strictness is mandatory and core.

## The split idea

Both names are good -- for different things. The right answer is to use both by splitting:

- **strictpg (90%):** The CLI tool and compiler. Everything that exists today. TOML in, artifacts out.
- **pgdesign (10%):** The future interactive/visual layer. Web UI, schema explorer, drift dashboard.

### The seam: live database interaction

strictpg is a pure compiler -- TOML in, artifacts out, no database required (except for introspect/diff/stats/serve which connect to a live DB). pgdesign is the interactive layer that connects to a real database and lets you explore, monitor, diff, and eventually visually design.

### What becomes pgdesign eventually

| Component | Why it's "design" |
|---|---|
| serve/ (HTTP API) | Already exposes structured JSON for a future UI -- schema introspection, diff, audit, stats, SVG |
| stats command | Live database health monitoring -- inherently interactive, repeated, contextual |
| diff --live | Schema drift detection against a live DB -- a "drift dashboard" |
| introspect/ | Reads a live DB into the model -- "explore your existing database" |
| seed --apply | Populating a live DB -- could benefit from preview, customization UI |
| Future web UI | The visualization-and-web-ui.md todo describes this: pan/zoom diagrams, click-to-filter, interactive exploration |

Note: the diff and introspect packages already produce structured, serializable data (SchemaDiff with [old, new] pairs, risk classifications, JSON tags) that a UI could consume with zero refactoring.

### What stays in strictpg

Everything else: parse, model, validate, audit, fd, discover, generate (DDL, D2, doc, JSON, SVG), codegen, migrate, sqlexpr, sqlparse, sqlutil, diagnostic, semtype, risk, extregistry, config, format, seed (to file/stdout), diff (TOML-to-TOML, git ref).

### Important caveat

The split is a future consideration. The immediate rename is pgdesign -> strictpg for the whole project. The pgdesign name is reserved for the future interactive layer, but that doesn't need to happen now.

## Rename scope

### Go module and imports (80+ files)

- go.mod: `module github.com/smm-h/pgdesign` -> `github.com/smm-h/strictpg`
- Every .go file under cmd/ and internal/ has import paths rooted at `github.com/smm-h/pgdesign/internal/...`
- 23 unique import paths across 80 files

### Binary and CLI

- Directory: cmd/pgdesign/ -> cmd/strictpg/
- CLI app name: `strictcli.NewApp("pgdesign", ...)` -> `strictcli.NewApp("strictpg", ...)`
- .goreleaser.yml: `main: ./cmd/pgdesign` -> `main: ./cmd/strictpg`
- .gitignore: `/pgdesign` -> `/strictpg`
- Compiled binary: pgdesign -> strictpg

### Config file

- pgdesign.toml -> strictpg.toml
- All source code references to the config filename (~30 locations across config.go, cli.go, handlers_build.go, handlers_diff.go, handlers_fmt.go, pgversion.go, parse.go, validate.go, extregistry.go)
- Test files creating/testing pgdesign.toml (~25 locations in config_test.go, parse_test.go)

### Database identifiers

- Migration table: `pgdesign_migrations` -> `strictpg_migrations`
- Advisory lock name: `pgdesign_migrate` -> `strictpg_migrate`
- Trigger function: `pgdesign_deny_mutation()` -> `strictpg_deny_mutation()`
- These appear in state.go, apply.go, rollback.go, serve/handlers.go, sql/sql.go, migrate/sql_gen.go, migrate/generate.go

### Environment variables

- `PGDESIGN_DB` -> `STRICTPG_DB`
- `PGDESIGN_TEST_DB` -> `STRICTPG_TEST_DB`

### User-facing strings

- Generated code headers: "Generated by pgdesign -- do not edit" (in all 6 codegen languages, seed output, build output)
- CLI help text: "Format a pgdesign schema file", "Start the pgdesign HTTP API server"
- Regen commands in codegen output: "pgdesign codegen ..."
- Seed output header: "Seed data generated by pgdesign"

### Package distribution

- npm: package.json `"name": "pgdesign"` -> `"strictpg"`, bin mapping, install.js download URLs, bin/pgdesign script
- PyPI: pyproject.toml `name = "pgdesign"` -> `"strictpg"`, console script entry point, pypi/pgdesign/ directory -> pypi/strictpg/, __init__.py references
- GitHub repo: github.com/smm-h/pgdesign -> github.com/smm-h/strictpg

### Documentation (27+ files)

- docs/_README.md, docs/_CLAUDE.md (selfdoc templates -- edit these, not the generated root files)
- docs/quickstart.md (~20 references)
- docs/format-reference.md (~12 references)
- docs/validation-rules.md (~10 references)
- docs/migration-guide.md (~25 references -- CLI commands, pgdesign_migrations table, pgdesign_deny_mutation function)
- 12 CLI doc files (docs/cli-*.md)
- 8 internal doc files (docs/internal-*.md)
- docs/semantic-types.md

### Configuration files

- selfdoc.json: `"base_url": "https://pgdesign.smmh.dev"` and `"project": "pgdesign"`
- .strictcli/checks.toml: `app = "pgdesign"`
- .strictcli/schema.json: name and project_id references
- .rlsbl/hooks/pre-checks.sh: checks for pgdesign.toml, runs pgdesign build

### Test fixtures and testdata

- testdata/schemas/comprehensive.toml: comment mentioning "all pgdesign features"
- testdata/expected/comprehensive.sql: pgdesign_deny_mutation() in expected output
- Test database names: pgdesign_test, pgdesign_discover_test, pgdesign_probe_test
- Test table names in migrate tests: pgdesign_test_table, pgdesign_test_table2
- Codegen test assertions (~24 locations): header text containing "Generated by pgdesign"
- sql/sql_test.go, generate/generate_test.go, migrate_test.go: pgdesign_deny_mutation assertions
- seed/seed_test.go: seed header assertion

### Historical references (do not modify)

- .rlsbl/changes/*.jsonl and *.md: released changelog entries mentioning pgdesign (1.0.0.jsonl records "Renamed from pgspec to pgdesign")
- todo/.done/ files: completed todo references
- CHANGELOG.md: generated from JSONL, will regenerate automatically

### Go struct name

- checks.go: `type pgdesignCheckContext struct` -> `type strictpgCheckContext struct`

### DNS/hosting

- pgdesign.smmh.dev -> strictpg.smmh.dev (or new domain)

## Migration concerns for existing users

- Database migration table rename: existing users have `pgdesign_migrations` in their databases. Need a migration path or backward-compat detection.
- Advisory lock name change: hash changes, so concurrent migration protection resets. Low risk but worth noting.
- Trigger function rename: existing databases have `pgdesign_deny_mutation()`. Migrations using the old function name would break on rollback.
- Config file rename: pgdesign.toml -> strictpg.toml. Could detect old name and error with instructions.
- Environment variables: PGDESIGN_DB -> STRICTPG_DB. Users with existing shell configs need to update.
- npm/PyPI: new packages. Old packages should get a final release pointing users to the new name.

## Effort estimate

Large rename but mechanical. Most changes are find-and-replace on import paths, string literals, and filenames. The tricky parts are:
- Database identifier migration (pgdesign_migrations, pgdesign_deny_mutation) -- needs a real migration path
- npm/PyPI package transition -- new packages, deprecation notices on old ones
- GitHub repo rename -- go module path change, redirect handling
- DNS -- new subdomain for docs site
- selfdoc/rlsbl re-scaffolding after rename

Estimate: 2-3 sessions for the mechanical rename + testing, plus decisions on migration strategy for existing database identifiers.
