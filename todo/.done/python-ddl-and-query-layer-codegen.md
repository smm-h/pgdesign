# Python DDL and query-layer codegen

## Problem

A consumer monorepo has a pgdesign TOML schema (16 tables, ~784 lines) and a hand-maintained Python DDL module used with asyncpg. The Python module follows a specific pattern:

- Per-table `CREATE_*` string constants containing raw SQL
- A `TABLE_NAMES` dict mapping logical names to table names
- An `ALL_STATEMENTS` list for ordered schema creation
- Custom SQL blocks (REVOKE for immutable tables, NOTIFY triggers)

pgdesign's current codegen modes (`--mode constants`, `--mode types`) don't produce this pattern. The consumer maintains both the TOML and the Python DDL in parallel, and they've diverged (10+ SQL-level differences: enum vs TEXT choices, uuid function names, PK syntax style, etc.). There's no automated way to detect or fix divergence.

## Feature 1: Python DDL codegen mode

A new codegen mode that generates a complete Python module consumable by asyncpg-based projects:

- Per-table `CREATE TABLE` string constants (named `CREATE_<TABLE>`)
- A `TABLE_NAMES` dict mapping logical keys to SQL table names
- An `ALL_CREATE_STATEMENTS` list in dependency order
- An `ensure_schema(pool)` async function that creates all tables idempotently
- Support for custom SQL blocks: triggers, REVOKE statements, index creation, extension activation (these are currently expressible in TOML via `[extensions]`, `[[indexes]]`, etc. but not emitted as Python constants)

The output should be a single `.py` file that a project drops into its source tree and imports. The consumer should never need to hand-edit it — if the TOML is the source of truth, the generated Python must capture everything.

## Feature 2: Query-layer abstraction generation

Longer-term and more ambitious. From a pgdesign schema, generate a typed Python query module:

- A `StorageWriter` protocol with typed methods for each table's write operations (create, update, transition for state-machine tables)
- A `StorageReader` protocol with typed methods for common read patterns (by PK, by FK, filtered lists)
- A `PgBackend` implementation using asyncpg
- An `InMemoryBackend` implementation using dicts/lists (for tests and lightweight consumers)
- State machine enforcement: if the schema defines a status column with legal transitions (via CHECK constraints or pgdesign metadata), the generated code enforces them

This would let any pgdesign consumer get a typed storage layer for free, instead of hand-writing ~50+ methods per project. The consumer that prompted this request is planning to build a StorageBackend abstraction with ~56 methods across 16 tables — work that could be fully derived from the schema definition.

### Design considerations

- The generated protocols should use domain sub-protocols (e.g., per-table or per-domain-group) rather than one monolithic interface, so consumers can depend on only what they need
- Transaction semantics matter: some write methods need transactional guarantees. The generation should express this (e.g., methods that do read-for-update + write as a single transaction)
- LISTEN/NOTIFY and advisory locks are PG-specific features that don't map to in-memory backends. The generation should separate these into a distinct protocol (EventBus, LockManager) rather than mixing them into the storage interface
- The InMemoryBackend is valuable for tests and for consumers that want to use the schema-derived types without requiring a running PG instance

## Effort estimate

Feature 1 (DDL codegen): moderate. The SQL generation already exists in `pgdesign generate`. The new work is emitting it as Python constants in a structured module rather than monolithic SQL.

Feature 2 (query-layer): large. Requires designing the protocol generation strategy, handling diverse table patterns (append-only, state-machine, simple CRUD), and generating two backend implementations. Could be phased: start with PgBackend generation, add InMemoryBackend later.
