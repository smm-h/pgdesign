# internal/introspect

Live PostgreSQL database introspection. Extracts schema into the resolved IR.

## Function

`Introspect(conn *pgx.Conn, schemaName string) (*model.Schema, []Diagnostic)`

## Data sources (pg_catalog)

| PG catalog table | What we extract |
|-----------------|-----------------|
| pg_namespace | Schema existence |
| pg_class (relkind='r','p') | Tables (regular + partitioned) |
| pg_attribute | Columns: name, type OID, NOT NULL, has_default, attnum (for ordering) |
| pg_attrdef + pg_get_expr() | Column defaults (reconstructed expression) |
| pg_type | Type names, enum check |
| pg_enum | Enum values (ordered by enumsortorder) |
| pg_constraint (contype='p') | Primary keys |
| pg_constraint (contype='f') | Foreign keys: columns, ref table, ref columns, ON DELETE action |
| pg_constraint (contype='u') | Unique constraints |
| pg_constraint (contype='c') + pg_get_constraintdef() | Check constraints (expression text) |
| pg_index + pg_get_indexdef() | Indexes: columns, method, partial WHERE, INCLUDE, opclass |
| pg_am | Index access method names |
| pg_description | Comments on tables and columns (objoid + objsubid) |
| pg_partitioned_table | Partition strategy and key |
| pg_inherits | Partition parent-child relationships |
| pg_extension | Installed extensions |

## Column ordering

Columns are ordered by `pg_attribute.attnum` (physical creation order). This matches what `\d` shows in psql.

## Reverse type mapping

Introspected columns have raw PG types (e.g., "uuid", "timestamptz", "bigint"). The introspector does NOT reverse-map these to semantic types -- that's a lossy operation. Instead, `Column.SemanticTypeName` is left empty for introspected schemas. The TOML export will use raw PG types.

A future `pgdesign adopt` command could suggest semantic type mappings (e.g., "this uuid column with gen_random_uuid() default looks like the `id` type").

## TOML export

`Export(schema *model.Schema) ([]byte, error)` -- Serializes the IR back to pgdesign TOML format using go-toml-edit for clean, formatted output. This enables bootstrapping: introspect an existing DB, export to TOML, commit, now it's managed by pgdesign.

Export respects pgdesign.toml format settings (column order, table order) if a config exists.

## Connection

Uses pgx/v5. Accepts standard `postgres://user:pass@host:port/db` URL. No connection pooling needed (introspection is a one-shot operation). Timeout configurable.
