# orxtra codegen wishlist: deferred items (17-22)

> Split from `orxtra-codegen-deferred.md`: item 15 (rlsbl check integration)
> moved to `todo/.done/rlsbl-check-integration.md`. These items (17-22) remain
> deferred. Original text preserved.

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
