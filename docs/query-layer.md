---
title: "Python Query Layer"
description: "How pgdesign generates a type-safe Python query layer with protocols, dual backends, and a declarative constraint registry for testing."
---

# Python Query Layer

The `--mode query-layer --lang python` codegen mode generates a complete type-safe query layer with protocols, dual backends, and a constraint registry. The generated code is async-first and uses Python's Protocol typing for structural subtyping.

## Generated Files

| File | Contents |
|------|----------|
| `protocols.py` | Context types, Row dataclasses, per-table Writer/Reader Protocols, combined Backend Protocol |
| `_constraints.py` | ConstraintKind enum, Constraint dataclass, per-table constraint lists, ALL_CONSTRAINTS dict, ConstraintEngine class |
| `_<table>_pg.py` | Per-table PG delegate (asyncpg parameterized queries) |
| `_<table>_mem.py` | Per-table InMemory delegate (dict stores with ConstraintEngine validation) |
| `pg_backend.py` | Composite PgBackend forwarding to per-table PG delegates |
| `memory_backend.py` | Composite InMemoryBackend forwarding to per-table InMemory delegates |
| `__init__.py` | Barrel file re-exporting Backend, Row types, PgBackend, InMemoryBackend |

## Protocol Derivation Rules

Methods are derived algorithmically from the schema structure, not hand-written. The protocol definitions, method signatures, and parameter types are all generated from the resolved model, ensuring that every table's CRUD operations, FK-based lookups, unique constraint lookups, and state machine transitions are covered automatically. When the schema changes, regenerating the query layer updates all protocols and implementations to match.

**Writer methods** (per-table):
- `create_<table>`: INSERT. Parameters: required NOT NULL columns without defaults, optional columns with defaults or nullable. Returns PK type.
- `update_<table>`: UPDATE. PK as positional params, all updatable columns as `Optional[T] = None` keyword-only. Skipped for append-only tables.
- `delete_<table>`: DELETE. PK as positional params, returns bool. Skipped for append-only tables.

**Reader methods** (per-table):
- `get_<table>`: SELECT by PK. Returns `Optional[<Table>Row]`.
- `get_<table>_by_<fk_col>`: One per incoming FK (reverse graph edges). Returns `list[<Table>Row]`.
- `get_<table>_by_<unique_cols>`: One per unique constraint. Multi-column uniques join column names with `_`. Returns `Optional[<Table>Row]`.
- `list_<table>`: Paginated SELECT. `limit: int = 100, offset: int = 0`. Returns `list[<Table>Row]`.

**SM transition methods**:
- `<transition_name>_<table>`: One per named transition. PK as positional params, `requires` fields as keyword-only params. Returns None.

**Exclusion rules**:
- Auto PK columns (Identity or DefaultExpr) excluded from create params.
- Generated columns excluded from create and update params.
- Identity columns excluded from create and update params.
- Append-only tables skip update and delete methods entirely.

## Architecture: Context + Delegate + Forwarding

The query layer avoids Python's method resolution order complexity by using a flat delegation pattern with three distinct layers. Each layer has a clear responsibility: contexts hold connection state, per-table delegates implement the actual database operations, and composite backends provide the unified API by forwarding calls to the appropriate delegate. This architecture eliminates diamond inheritance issues and makes each component independently testable.

1. **Context**: Holds connection state. `PgContext` wraps an asyncpg pool. `InMemoryContext` wraps dict stores and unique indexes.

2. **Per-table delegates**: Each table gets a `<Table>Pg` and `<Table>Mem` class that implements all Writer and Reader methods for that table. Each delegate receives the context in `__init__` and operates independently.

3. **Composite backends**: `PgBackend` and `InMemoryBackend` create all per-table delegates in `__init__` and expose one-line forwarding methods that delegate to the appropriate per-table instance.

This pattern means:
- No diamond inheritance or MRO issues
- Each per-table delegate is independently testable
- The composite backends satisfy the Backend protocol via structural subtyping (no explicit inheritance needed)
- Adding a table means adding delegate files and forwarding methods -- no base class changes

## Constraint Registry

The `_constraints.py` file provides a data-driven constraint system that enables the InMemoryBackend to enforce the same database rules as PostgreSQL without requiring a live database connection. Every NOT NULL, ENUM, UNIQUE, CHECK, FK, ON DELETE, and state machine transition constraint from the schema is represented as a declarative Constraint dataclass with a kind, table, column, and parameters. This allows the InMemoryBackend to validate writes against the full set of schema constraints.

### ConstraintKind

```python
class ConstraintKind(Enum):
    NOT_NULL = "not_null"
    ENUM = "enum"
    UNIQUE = "unique"
    FK = "fk"
    CHECK_RANGE = "check_range"
    CHECK_COMPARISON = "check_comparison"
    CHECK_LENGTH = "check_length"
    CHECK_PATTERN = "check_pattern"
    ON_DELETE_CASCADE = "on_delete_cascade"
    ON_DELETE_RESTRICT = "on_delete_restrict"
    ON_DELETE_SET_NULL = "on_delete_set_null"
    STATE_MACHINE_TRANSITION = "state_machine_transition"
```

### Per-Table Constraint Lists

Each table gets a `<TABLE>_CONSTRAINTS: list[Constraint]` variable containing all constraints that apply to that table, derived from the schema's column definitions, foreign keys, unique constraints, CHECK expressions, and state machine transitions. The constraint list is generated at code generation time from the resolved model, so it always reflects the current schema without manual maintenance. Constraint kinds are categorized for efficient validation.
- NOT NULL columns
- Enum-typed columns (valid values list)
- CHECK constraints (classified as range, comparison, length, or pattern)
- Unique constraints
- Outgoing FKs (existence check on insert/update)
- Incoming FKs via reverse graph (ON_DELETE_CASCADE, RESTRICT, SET_NULL)
- State machine transitions (full transition map in params)

### ConstraintEngine

Static methods on the ConstraintEngine class validate constraints against in-memory dictionary stores, implementing the same logical checks that PostgreSQL enforces via CHECK constraints, triggers, and foreign key references. The engine handles INSERT validation (NOT NULL, ENUM, UNIQUE, FK, CHECK), UPDATE validation (merging old and new values, plus state machine transition checks), and DELETE processing (recursive ON DELETE CASCADE, RESTRICT blocking, and SET NULL propagation).

- `validate_insert(table, constraints, row, stores, unique_indexes)`: Checks NOT_NULL, ENUM, UNIQUE, FK, CHECK_*, skips STATE_MACHINE_TRANSITION (no old state).
- `validate_update(table, constraints, old_row, new_row, pk_columns, stores, unique_indexes)`: Merges old+new, runs insert validation on merged row, then checks STATE_MACHINE_TRANSITION (validates old->new transition is allowed).
- `process_delete(table, pk_value, all_constraints, stores, unique_indexes)`: Processes ON_DELETE actions recursively. CASCADE deletes child rows (with recursive cascade). RESTRICT blocks if children exist. SET_NULL nullifies FK columns.

### ALL_CONSTRAINTS

A `dict[str, list[Constraint]]` mapping every table name to its complete constraint list, aggregating constraints from all tables in the schema into a single lookup structure. This global mapping is used by the `process_delete` method for cascade processing, since deleting a row from one table may trigger ON DELETE CASCADE actions that affect child rows in other tables. The recursive cascade traversal uses ALL_CONSTRAINTS to discover which tables have foreign keys pointing to the deleted row's table.

## State Machine Single-Source-of-Truth

State machine definitions in TOML flow to three enforcement points from the same model, ensuring that the database trigger, the PgBackend Python validation, and the InMemoryBackend constraint engine all enforce exactly the same transition rules. This single-source-of-truth approach means transition rules are never duplicated or manually synchronized across enforcement layers. All three derive from `model.SMTransitionMap`, and the transition map flows from the TOML through the model to each output.

1. **Database trigger**: BEFORE UPDATE trigger validates transitions in PostgreSQL (generated by `generate` package).
2. **PgBackend codegen**: SELECT FOR UPDATE, validate in Python, then UPDATE (defense-in-depth alongside the trigger).
3. **InMemoryBackend codegen**: ConstraintEngine validates via STATE_MACHINE_TRANSITION constraint kind.

All three derive from `model.SMTransitionMap`, ensuring consistency. The transition rules are never duplicated -- they flow from the TOML through the model to each output.

## Table Groups

Schema TOML can define groups that organize tables into logical subsets for selective code generation. When groups are configured, the `--group` flag filters which tables are included in the generated query layer output. The `--backends` flag controls which backend implementations to generate, allowing projects to generate only the PgBackend for production deployments or only the InMemoryBackend for testing. Both flags can be combined for fine-grained control.

```toml
[groups]
core = ["users", "roles"]
orders = ["orders", "order_items"]
```

The `--backends` flag controls which backends to generate (`pg`, `memory`, or both). When both are generated (the default), the query layer produces the full file set for both backends.

## Backend Filtering

```bash
# Generate only PgBackend
pgdesign codegen --lang python --mode query-layer --backends pg

# Generate only InMemoryBackend
pgdesign codegen --lang python --mode query-layer --backends memory

# Generate both (default)
pgdesign codegen --lang python --mode query-layer
```

When a backend is filtered out, its files are not generated and the `__init__.py` barrel file adjusts its imports accordingly.

## Dual-Backend Conformance

Conformance between the two backends is verified at the codegen level through Go tests that inspect the generated Python code, not through Python runtime checks. This compile-time verification ensures that both backends maintain identical public method sets, matching parameter signatures, and consistent constraint enforcement logic. The verification covers 8 specific conformance properties that together guarantee behavioral equivalence between the PgBackend and InMemoryBackend.

1. PgBackend and InMemoryBackend have the exact same set of public methods
2. Both implement every method declared in the Backend protocol
3. PgBackend uses parameterized SQL (`$1`, `$2`) for every query
4. InMemoryBackend delegates to ConstraintEngine for every write operation
5. SM transition methods exist on both backends with matching signatures
6. Cascade delete always references ALL_CONSTRAINTS
7. Both composites have identical forwarding method sets
8. Per-table PG and InMemory delegates have method parity
