# orxtra codegen wishlist: everything we need from pgdesign

orxtra is migrating from hand-maintained Python DDL (`_schema.py`) to pgdesign-generated code. The migration exposed gaps ranging from one-line fixes to ambitious features. This document covers all 22 items, grouped by effort, with rationale for each.

## Already works (just needs orxtra-side configuration)

### 1. [output] config with build command

**Status:** Fully implemented. orxtra just hasn't configured it yet.

**What:** `pgdesign.toml` supports `[output.<name>]` sections specifying format, path, lang, mode, split, idempotent. The `build` command generates all configured outputs and auto-commits via safegit.

**Why we need it:** orxtra currently runs `pgdesign codegen` imperatively with CLI flags. The `build` command is the declarative equivalent — declare outputs in config, run one command, files land in the right places. This is the foundation for staleness checking (item 2) and release pipeline integration (item 15).

### 2. check --tag build for staleness detection

**Status:** Fully implemented. Works out of the box once `[output]` is configured.

**What:** `pgdesign check --tag build` regenerates all configured outputs in memory, compares byte-for-byte against committed files, reports stale/missing files.

**Why we need it:** orxtra has a custom `scripts/check_schema_sync.py` that AST-parses TABLE_NAMES dicts to compare TOML against Python DDL. It's fragile, incomplete (only checks table-name parity, not full DDL), and must be maintained. `pgdesign check --tag build` replaces it with the schema compiler's own guarantee — the tool that generates the files checks their freshness. We delete `check_schema_sync.py` entirely.

### 3. execute(conn, sections=[...]) for filtered DDL

**Status:** Fully implemented in the generated `schema_executor.py`.

**What:** The generated executor's `execute()` function accepts `sections: Sequence[str] | None` to filter which DDL sections to apply. Valid kinds: schemas, extensions, types, tables, foreign_keys, unique_constraints, indexes, etc.

**Why we need it:** orxtra's test fixtures need to skip `CREATE EXTENSION pg_uuidv7` (not available in test PG) and install a stub function instead. By calling `execute(conn, sections=["types", "tables", "foreign_keys", ...])` — listing everything except extensions — the fixture skips the extension cleanly. This works today.

### 4. migrate plan/generate/apply for schema changes

**Status:** Fully implemented with 7 subcommands.

**What:** Full migration system: `plan` analyzes diff, `generate` produces SQL migration files, `apply` runs them, `rollback` reverses them, `status` shows state, `squash` consolidates, `test` shadow-tests. Handles `ALTER TYPE ADD VALUE` with PG version awareness (non-transactional on PG < 12, transactional on PG 12+).

**Why we need it:** orxtra adopting PG enum types (replacing TEXT columns) means enum value additions require `ALTER TYPE ADD VALUE`. Without migration tooling, every new task state or run status requires hand-written migration SQL. With `pgdesign migrate`, you edit the TOML, run `pgdesign migrate plan`, and the migration script is generated automatically.

### 5. diff --base for change detection

**Status:** Fully implemented with three modes (--live, --against, --base).

**What:** `pgdesign diff --base HEAD~1` compares the current schema against a git ref, detecting added/removed/modified tables, columns, indexes, enum values (with position awareness — added at end vs inserted in middle).

**Why we need it:** During code review, `pgdesign diff --base main` shows exactly what schema changes a PR introduces. For enum changes specifically, it distinguishes "value added at end" (safe, no reordering) from "value inserted in middle" (potentially unsafe, changes ordinal positions). This is more useful than raw TOML diffs.

### 6. --mode types --lang python for enum + row types

**Status:** Implemented. Produces 11 enum classes + 18 dataclass row types from orxtra's schema.

**What:** `pgdesign codegen --lang python --mode types` generates Python classes from the TOML schema: one `str, Enum` class per TOML enum type, one `@dataclass` per table with typed fields.

**Why we need it:** orxtra hand-maintains Python StrEnums (`TaskState`, `RunStatus`, etc.) that must match the TOML enum definitions. Two representations of the same thing that can drift. Generated enums eliminate drift by construction — the TOML is the single source of truth.

---

## Small fixes

### 7. StrEnum instead of str, Enum

**Status:** One-line fix in `internal/codegen/enum_gen.go:142`.

**What:** Change `class %s(str, Enum):` to `class %s(StrEnum):` and add `from enum import StrEnum` to the import block.

**Why we need it:** orxtra uses `StrEnum` (Python 3.11+) everywhere. The generated `str, Enum` pattern is functionally equivalent but not a drop-in replacement — orxtra's type checkers, pattern matching, and existing code all expect `StrEnum`. Without this fix, adopting generated enums requires either changing all of orxtra's enum usage or post-processing the generated output.

**The hole it fills:** Makes `--mode types --lang python` output directly usable by orxtra without manual editing. Currently the output must be hand-modified to replace `str, Enum` with `StrEnum` in 11 classes.

### 8. Split output idempotent variants

**Status:** Missing. Split files (`tables_trace.py`, `tables_dispatch.py`, etc.) contain raw SQL only. The non-split executor (`schema_executor.py`) has both `sql` and `idempotent_sql` in each `DDLOp`, but split mode produces 4-tuples with only the raw SQL.

**What:** Include `IF NOT EXISTS` / `CREATE OR REPLACE` variants in split output, either as a second element in each STATEMENTS tuple or as a parallel `IDEMPOTENT_STATEMENTS` list.

**Why we need it:** Per-owner self-contained files (item 11) need idempotent shared statements so that `CREATE TYPE run_status AS ENUM (...)` in `tables_trace.py` doesn't conflict with the same statement in `tables_dispatch.py`. Without idempotent variants, only one file can create the type — the other must assume it already exists. With idempotent variants (`CREATE TYPE IF NOT EXISTS` or `DO $$ IF NOT EXISTS (SELECT ...) $$`), both files can safely include the type and be executed in any order.

**The hole it fills:** Unblocks item 11 (self-contained per-owner output) and makes split output usable for independent module schemas that share types.

---

## Medium features

### 9. exclude_sections on execute()

**Status:** Not implemented. Only inclusion filtering exists (`sections` parameter).

**What:** Add `exclude_sections: Sequence[str] | None = None` parameter to the generated `execute()` function. When set, skip sections matching any of the excluded kinds.

**Why we need it:** Item 3 (section filtering) works by listing everything you want. For the common case of "everything except extensions," this means enumerating 8+ section kinds and updating the list whenever pgdesign adds a new kind. `exclude_sections=["extensions"]` is intention-revealing and forward-compatible.

**The hole it fills:** Makes the test fixture's extension-skipping pattern robust against pgdesign adding new section kinds in future releases.

### 10. extension_stubs on execute()

**Status:** Not implemented.

**What:** Add `extension_stubs: dict[str, str] | None = None` parameter to `execute()`. When an extension name appears in the dict, skip `CREATE EXTENSION <name>` and execute the stub SQL instead.

**Why we need it:** orxtra's test PG doesn't have `pg_uuidv7`. The fixture installs a stub function (`uuid_generate_v7() → gen_random_uuid()`). Currently the fixture must understand pgdesign's STATEMENTS tuple format to find and replace the extension entry. With `extension_stubs`, the fixture calls `execute(conn, extension_stubs={"pg_uuidv7": "CREATE OR REPLACE FUNCTION uuid_generate_v7() RETURNS uuid AS $$ SELECT gen_random_uuid(); $$ LANGUAGE sql;"})` — one line, no tuple parsing, no coupling to pgdesign internals.

**The hole it fills:** Decouples test fixtures from pgdesign's internal data structures. The test fixture expresses intent ("use this stub for this extension") rather than implementation ("find the tuple where the second element is 'extension' and replace the first element").

### 11. Per-owner self-contained split output

**Status:** Not implemented. `--split-by-file` produces separate files per source TOML, but shared dependencies (types, extensions) go into their own files (`types.py`, `extensions.py`) that all table files must import.

**What:** Each `tables_<source>.py` includes all the types, extensions, and functions it needs as a preamble, using idempotent DDL (IF NOT EXISTS). The file is fully self-contained — it can create its own tables from scratch without any other file.

**Why we need it:** orxtra has strict module ownership. trace owns its DDL, dispatch owns its DDL. A shared `types.py` forces either a new package or cross-module imports. Self-contained per-owner output eliminates the shared dependency entirely. Each module commits its own generated DDL file, imports nothing from the other module.

**The hole it fills:** Resolves the "where do shared generated files live?" question (D1) by making the question irrelevant — there are no shared files.

### 12. Standalone enum-only output mode

**Status:** Not implemented. `--mode types` generates both enums AND row dataclasses. No way to get just enums.

**What:** A way to generate only the enum classes, without the row dataclasses. Either a separate `--mode enums` or a `--exclude-tables` flag on `--mode types`.

**Why we need it:** orxtra wants to replace its hand-maintained StrEnums with generated ones. It does NOT want generated row dataclasses — it has its own Pydantic models with custom validation, frozen=True, strict=True, extra='forbid'. The generated dataclasses are plain `@dataclass` with no validation. Generating both and ignoring the dataclasses works but produces dead code in the output file and confuses contributors ("why are these dataclasses here if we don't use them?").

**The hole it fills:** Generates exactly what orxtra needs (enum types) without baggage it doesn't need (row types). Clean generated output that can be committed as-is.

---

## Large features

### 13. ON DELETE RESTRICT in codegen

**Status:** Unclear — needs verification. The TOML supports `on_delete = "RESTRICT"` but it's untested whether the Python DDL codegen emits it correctly.

**What:** Verify that `on_delete = "RESTRICT"` in TOML produces `ON DELETE RESTRICT` in both SQL DDL and Python DDL codegen output.

**Why we need it:** orxtra's append-only event store should never cascade deletions. RESTRICT (not NO ACTION, not CASCADE) makes the no-delete intent explicit and non-deferrable. The TOML currently says CASCADE on all FKs — we want to change it to RESTRICT. If the codegen doesn't handle RESTRICT, the migration produces wrong DDL.

**The hole it fills:** Ensures the TOML can express the exact FK behavior orxtra needs, and the codegen emits it faithfully.

### 14. Per-owner output routing via [output] config

**Status:** Partially implemented. `build` writes multi-file output to a directory. But there's no way to route different source files' outputs to different directories.

**What:** An `[output]` config that maps source TOML files to output paths. E.g.:
```toml
[output.trace_ddl]
format = "codegen"
lang = "python"
mode = "ddl"
path = "trace/src/orxtra/trace/_generated_schema.py"
source = "trace.toml"

[output.dispatch_ddl]
format = "codegen"
lang = "python"
mode = "ddl"
path = "dispatch/src/orxtra/dispatch/_generated_schema.py"
source = "dispatch.toml"
```

One `pgdesign build` invocation generates both files into their respective module directories.

**Why we need it:** orxtra's modules own their own source directories. Without output routing, the generated files land in a single directory and must be manually copied (or a script does it). With routing, `pgdesign build` is the single command that generates AND places all DDL — no post-processing, no scripts, no manual steps.

**The hole it fills:** Makes `pgdesign build` the complete solution for the "generate + place + commit" workflow. Currently you run `codegen`, manually copy files, then commit. With routing, `build` does all three.

### 15. rlsbl check integration

**Status:** Not implemented.

**What:** pgdesign registers as an rlsbl check provider. When `rlsbl check --tag quality` runs (or a new `--tag schema` tag), it invokes `pgdesign check --tag build` automatically if a `pgdesign.toml` exists.

**Why we need it:** orxtra uses rlsbl for release orchestration. rlsbl already runs tests, lint, selfdoc check, and changelog validation during releases. Schema staleness should be checked at the same gate. Currently it requires a separate CI job and a separate pre-checks hook invocation. With rlsbl integration, schema freshness is just another rlsbl check — no extra wiring.

**The hole it fills:** Eliminates the need to separately wire pgdesign checks into CI and release hooks. The release pipeline becomes: `rlsbl release run` → runs all checks including schema staleness → no stale DDL can ship.

### 16. Enum-typed columns in generated row dataclasses

**Status:** Not implemented. Generated row dataclasses type enum columns as `str`, not as the generated enum class.

**What:** When a column references an enum type (e.g., `status public.run_status`), the generated row dataclass should type the field as `RunStatus`, not `str`.

**Why we need it:** If orxtra ever adopts the generated row types (as DTOs, not as domain models), having `status: str` defeats the purpose of generating enums in the same file. The enum exists right there in the output — the dataclass should reference it. This is about internal consistency of the generated code.

**The hole it fills:** Makes the generated types file internally consistent — the enum classes and the row types that reference them are properly linked, enabling type-safe code without manual annotation.

---

## Outlandish but correct

### 17. Cross-reference validation against consumer code

**Status:** Not implemented. Would be a novel feature.

**What:** A pgdesign check that scans Python files importing from the generated enum module and validates that every enum value referenced in the consumer code exists in the TOML definition. E.g., if orxtra's `_transitions.py` references `TaskState.SUSPENDED` but the TOML enum `task_state` has no `suspended` value, pgdesign reports an error.

**Why we need it:** The single-source-of-truth guarantee (TOML → generated Python) prevents drift in the generated file but not in the consumers. A developer could add `SUSPENDED` to the Python transition table without adding it to the TOML, and the system would crash at runtime when the PG enum rejects the value. This check catches the bug at build time.

**The hole it fills:** Extends the single-source-of-truth guarantee from "the generated file is correct" to "the code that uses the generated file is also correct." Closes the gap between schema-level and application-level consistency.

### 18. Atomic migration codegen (SQL + Python enum diff)

**Status:** Not implemented. `migrate generate` produces SQL migrations. Python enum regeneration is a separate `codegen` step.

**What:** When an enum value is added to the TOML, `pgdesign migrate generate` produces both the SQL migration (`ALTER TYPE ADD VALUE 'new_state'`) AND regenerates the Python StrEnum file in one step. The developer edits the TOML, runs one command, gets both the migration and the updated Python types.

**Why we need it:** Today, adding a new enum value requires: (1) edit TOML, (2) run `pgdesign migrate generate` for SQL, (3) run `pgdesign codegen --mode types` to regenerate Python, (4) run `pgdesign build` to place files. Four commands for one conceptual change. Atomic migration codegen reduces this to: (1) edit TOML, (2) run one command that does everything.

**The hole it fills:** Eliminates the risk of running the SQL migration without updating the Python types (or vice versa). The two artifacts that must change together always change together.

### 19. Import-time validation hook in generated enums

**Status:** Not implemented.

**What:** The generated Python enum file includes a `_register_consumer(name: str, values: frozenset[str])` function and a module-level registry. Consumer modules call `_register_consumer("transitions", frozenset(TRANSITIONS.keys()))` at import time. The generated file validates that every registered consumer's values are a subset of the enum's values. Mismatch raises `ImportError` immediately.

**Why we need it:** orxtra's task state machine has a transition table (`_transitions.py`) that maps `TaskState` values to sets of valid next states. If the transition table references a state that doesn't exist in the enum, the system fails at runtime (when a transition is attempted) rather than at import time. Import-time validation makes this a startup error — the system refuses to start with an inconsistent transition table.

**The hole it fills:** Moves consistency checking from "runtime crash on the first invalid transition attempt" to "process refuses to start." Fail-fast at the earliest possible moment.

### 20. Full round-trip from one build invocation

**Status:** Partially implemented. `build` generates DDL. But it doesn't generate types, migrations, or consumer validation in one invocation.

**What:** A single `pgdesign build` invocation that: generates DDL files (per-owner, placed in module directories), generates Python StrEnum files (placed in protocols), generates migration scripts (if schema changed from previous version), runs staleness checks, runs consumer validation. One command, everything derived from TOML, everything placed correctly.

**Why we need it:** The current workflow for a schema change is: edit TOML → codegen DDL → codegen types → migrate generate → build → check. Each step is a separate command with its own flags and output path. A single `build` command that reads the `[output]` config and produces everything eliminates the multi-step dance and the risk of forgetting a step.

**The hole it fills:** Makes pgdesign the complete schema management tool — from definition to code generation to migration to validation, all declarative, all from one command.

### 21. Test schema mode

**Status:** Not implemented.

**What:** `pgdesign codegen --test` (or an `[output]` config with `test = true`) that generates DDL with: extension stubs instead of CREATE EXTENSION, relaxed constraints (e.g., shorter VARCHAR limits), synthetic data fixtures (INSERT statements with representative test data for each table), and optionally TABLE_NAMES as a Python dict for backward compatibility.

**Why we need it:** orxtra's test fixtures manually handle extension stubbing, test data creation, and schema setup. Every test suite reimplements parts of this. A test-specific DDL output would centralize test infrastructure in the schema compiler — tests would call `execute_test_schema(conn)` and get a fully set up database with representative data, no per-test boilerplate.

**The hole it fills:** Eliminates test fixture code that reimplements schema setup. The schema compiler knows the schema better than any test fixture — it should generate the test infrastructure too.

### 22. Dependency-aware multi-repo codegen

**Status:** Not implemented. Would require topology awareness similar to selfdoc.

**What:** pgdesign reads a `topology` config (similar to selfdoc's cross-project linking) and generates DDL/types for schemas that span multiple repositories. If repo A defines enum types that repo B's tables reference, pgdesign resolves the cross-repo dependency and generates correct DDL for both.

**Why we need it:** orxtra is a single monorepo today, but the architecture is designed for the modules to be independently deployable. If trace and dispatch ever become separate repos (or if external consumers define their own tables referencing orxtra's enum types), pgdesign needs to resolve cross-repo schema dependencies. This is the schema equivalent of what selfdoc's topology does for documentation cross-linking.

**The hole it fills:** Extends pgdesign from "single-project schema compiler" to "multi-project schema compiler" — the same evolution selfdoc made with its topology and assembly features. Without it, splitting orxtra into separate repos would require manually maintaining shared type definitions in each repo.
