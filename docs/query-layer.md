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

Methods are derived from the schema, not hand-written:

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

The query layer avoids Python's MRO complexity by using a flat delegation pattern:

1. **Context**: Holds connection state. `PgContext` wraps an asyncpg pool. `InMemoryContext` wraps dict stores and unique indexes.

2. **Per-table delegates**: Each table gets a `<Table>Pg` and `<Table>Mem` class that implements all Writer and Reader methods for that table. Each delegate receives the context in `__init__` and operates independently.

3. **Composite backends**: `PgBackend` and `InMemoryBackend` create all per-table delegates in `__init__` and expose one-line forwarding methods that delegate to the appropriate per-table instance.

This pattern means:
- No diamond inheritance or MRO issues
- Each per-table delegate is independently testable
- The composite backends satisfy the Backend protocol via structural subtyping (no explicit inheritance needed)
- Adding a table means adding delegate files and forwarding methods -- no base class changes

## Constraint Registry

The `_constraints.py` file provides a data-driven constraint system for the InMemoryBackend.

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

Each table gets a `<TABLE>_CONSTRAINTS: list[Constraint]` with entries derived from:
- NOT NULL columns
- Enum-typed columns (valid values list)
- CHECK constraints (classified as range, comparison, length, or pattern)
- Unique constraints
- Outgoing FKs (existence check on insert/update)
- Incoming FKs via reverse graph (ON_DELETE_CASCADE, RESTRICT, SET_NULL)
- State machine transitions (full transition map in params)

### ConstraintEngine

Static methods that validate constraints against in-memory stores:

- `validate_insert(table, constraints, row, stores, unique_indexes)`: Checks NOT_NULL, ENUM, UNIQUE, FK, CHECK_*, skips STATE_MACHINE_TRANSITION (no old state).
- `validate_update(table, constraints, old_row, new_row, pk_columns, stores, unique_indexes)`: Merges old+new, runs insert validation on merged row, then checks STATE_MACHINE_TRANSITION (validates old->new transition is allowed).
- `process_delete(table, pk_value, all_constraints, stores, unique_indexes)`: Processes ON_DELETE actions recursively. CASCADE deletes child rows (with recursive cascade). RESTRICT blocks if children exist. SET_NULL nullifies FK columns.

### ALL_CONSTRAINTS

A `dict[str, list[Constraint]]` mapping every table name to its constraint list. Used by `process_delete` for cascade processing -- deleting from one table may cascade to children in other tables.

## State Machine Single-Source-of-Truth

State machine definitions in TOML flow to three enforcement points from the same model:

1. **Database trigger**: BEFORE UPDATE trigger validates transitions in PostgreSQL (generated by `generate` package).
2. **PgBackend codegen**: SELECT FOR UPDATE, validate in Python, then UPDATE (defense-in-depth alongside the trigger).
3. **InMemoryBackend codegen**: ConstraintEngine validates via STATE_MACHINE_TRANSITION constraint kind.

All three derive from `model.SMTransitionMap`, ensuring consistency. The transition rules are never duplicated -- they flow from the TOML through the model to each output.

## Table Groups

Schema TOML can define groups:

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

Conformance is verified at the codegen level (Go tests, not Python runtime):

1. PgBackend and InMemoryBackend have the exact same set of public methods
2. Both implement every method declared in the Backend protocol
3. PgBackend uses parameterized SQL (`$1`, `$2`) for every query
4. InMemoryBackend delegates to ConstraintEngine for every write operation
5. SM transition methods exist on both backends with matching signatures
6. Cascade delete always references ALL_CONSTRAINTS
7. Both composites have identical forwarding method sets
8. Per-table PG and InMemory delegates have method parity
