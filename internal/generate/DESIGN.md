# internal/generate

DDL generator. Transforms resolved IR into PostgreSQL DDL.

## Function

`Generate(schema *model.Schema, opts Options) string`

Options:
- Idempotent (bool) -- IF NOT EXISTS guards
- IncludeComments (bool, default true)
- Format (SQL | D2 | JSON)

## Emission order

1. `CREATE SCHEMA IF NOT EXISTS <schema>`
2. `CREATE EXTENSION IF NOT EXISTS <ext>` (declaration order from [meta].extensions)
3. `CREATE TYPE <schema>.<name> AS ENUM (...)` for all enums
4. `CREATE TABLE` for all tables in topo order. Includes: columns, inline PK, PARTITION BY. Does NOT include FK constraints inline.
5. `CREATE TABLE ... PARTITION OF` for declared partitions (children of partitioned tables)
6. `ALTER TABLE ADD CONSTRAINT` for all FKs (deferred to handle cycles safely)
7. `ALTER TABLE ADD CONSTRAINT` for all UNIQUE constraints
8. `ALTER TABLE ADD CONSTRAINT` for all CHECK constraints
9. `CREATE INDEX` for all explicit indexes
10. `CREATE INDEX` for all auto-generated FK indexes (those not covered by explicit indexes)
11. `COMMENT ON TABLE` for all tables
12. `COMMENT ON COLUMN` for all columns with comments
13. `ALTER TABLE ... OWNER TO` for tables with owner set

## Determinism

Output is fully deterministic. Same input always produces byte-for-byte identical output. Within each step, items are ordered by: topo sort (tables), alphabetical (constraints, indexes, comments within a table).

## Cycle handling

Tables in cycle groups are emitted in step 4 without FK constraints. Their FK constraints are added in step 6 via ALTER TABLE. This means all referenced tables exist before any FK is added.

## D2 format

When `Format = D2`, outputs D2 diagram language source text:
- Tables as shapes with columns listed
- FK relationships as edges (labeled with ON DELETE action)
- Can be rendered to SVG via the D2 Go library (in serve/) or saved as .d2 file

## JSON format

When `Format = JSON`, outputs the full resolved IR as JSON. Stable schema for external tooling consumption.

## pg_partman integration

For tables with MaintenanceConfig, after the CREATE TABLE + partition setup, emit:
```sql
SELECT partman.create_parent('<schema>.<table>', '<column>', '<strategy>', '<interval>');
UPDATE partman.part_config SET premake = <N>, retention = '<period>' WHERE parent_table = '<schema>.<table>';
```
