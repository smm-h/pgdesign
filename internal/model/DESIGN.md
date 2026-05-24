# internal/model

Resolved intermediate representation (IR). The canonical in-memory schema that all downstream packages consume.

## Build pipeline

`Build(raw *parse.RawSchema, reg *semtype.Registry) (*Schema, []Diagnostic)`

Internally phased:
1. `resolve()` -- Expand semantic types into PG types + attributes. Resolve enum references. Apply column-level overrides (nullable, default). Resolve generated columns.
2. `order()` -- Build FK dependency graph. Topological sort (Kahn's algorithm). Detect cycles, group them. Produce `TableOrder` (DAG tables sorted) + `CycleGroups` (mutually-referencing clusters).
3. `enrich()` -- Materialize auto-indexes for FK columns (skipped if HasIndexCovering). Auto-generate constraint names where not explicitly provided. Compute candidate keys (for NF audit). Resolve FK ref_table names (bare = same schema, qualified = cross-schema).

If resolve() fails on some tables, it still returns a partial IR for the tables that succeeded, allowing downstream passes to report on what they can.

## IR types

- `Schema` -- Name (string), Extensions (ordered []string), Enums ([]Enum), Tables (ordered []Table), CycleGroups ([][]string -- table names in each cycle).
- `Table` -- Name, Schema, Comment, Columns (ordered []Column), PK ([]string), FKs ([]FK), Indexes ([]Index), Uniques ([]UniqueConstraint), Checks ([]CheckConstraint), Partitioning (*PartitionSpec), Dependencies ([]FuncDep), Maintenance (*MaintenanceConfig), Owner (string, optional).
- `Column` -- Name, PGType, NotNull, Default (resolved literal), DefaultExpr (resolved expression), Generated (expression), Stored (bool), Comment, SemanticTypeName (original type name, for roundtrip/introspect mapping).
- `FK` -- Name, Columns, RefSchema, RefTable, RefColumns, OnDelete.
- `Index` -- Name, Columns, Method (btree|gin|gist|hash), Opclass (per-column), Where (partial), Include (covering), IsAutoFK (generated for FK coverage).
- `UniqueConstraint` -- Name, Columns.
- `CheckConstraint` -- Name, Expr.
- `Enum` -- Schema, Name, Values, Comment.
- `PartitionSpec` -- Strategy (range|list|hash), Column, Children ([]PartitionSpec -- recursive for sub-partitioning).
- `MaintenanceConfig` -- Premake, Retention, RetentionKeepTable.
- `FuncDep` -- Determinant ([]string), Dependent ([]string).

## Key methods

- `Schema.TableOrder() []Table` -- Returns tables in dependency order (topo-sorted). Cycle group tables appear after their non-cyclic dependencies.
- `Table.HasIndexCovering(columns []string) bool` -- Returns true if any explicit index's leading columns are a superset of the given columns. Used to skip FK auto-index.
- `Table.CandidateKeys() [][]string` -- Computed from declared FuncDeps using attribute closure (delegates to internal/fd package). Cached after first computation.
- `Schema.TableByName(schema, name string) *Table` -- Lookup for FK resolution.

## id/pk precedence rule

1. If table has explicit `pk = [...]` field, those columns are the PK regardless of types.
2. Else if exactly one column has semantic type `id` or `auto_id`, that column is the PK.
3. Else: diagnostic E004 "table missing primary key".

## Cycle handling

Tables in cycle groups cannot be topo-sorted relative to each other. The build phase still resolves them fully -- they just won't have a deterministic order among themselves. The generator handles this by emitting them without inline FK constraints, then adding FKs via ALTER TABLE.
