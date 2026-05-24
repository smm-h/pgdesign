# internal/semtype

Semantic type system. Maps type names to PostgreSQL types with enforced attributes.

## Types

- `TypeDef` -- struct: Name, Kind (scalar|enum|composite), BaseType (PG type string), NotNull (bool), Default (literal value), DefaultExpr (SQL expression), Check (constraint expression, VALUE placeholder), Unique (bool), Comment (string), EnumValues ([]string), Generated (expression), Stored (bool).
- `Registry` -- holds named TypeDefs. Thread-safe read access.

## Builtin types (12)

| Name | PG type | NOT NULL | Default | Check |
|------|---------|----------|---------|-------|
| id | uuid | yes | gen_random_uuid() | -- |
| ref | uuid | yes | -- | -- |
| timestamp | timestamptz | yes | now() | -- |
| timestamp_optional | timestamptz | no | -- | -- |
| money | bigint | yes | 0 | -- |
| slug | text | yes | -- | ^[a-z0-9-]+$ |
| email | text | yes | -- | basic email regex |
| short_text | text | yes | -- | LENGTH(VALUE) <= 255 |
| json | jsonb | yes | '{}' | -- |
| json_array | jsonb | yes | '[]' | -- |
| counter | bigint | yes | 0 | -- |
| flag | boolean | yes | false | -- |
| auto_id | bigint | yes | GENERATED ALWAYS AS IDENTITY | -- |

## User-defined types

Loaded from `[types.*]` in TOML. Support all attributes that builtins have. Two kinds:

1. Scalar types: define `base`, optionally `not_null`, `default`, `default_expr`, `check`, `unique`, `comment`.
2. Enum types: define `kind = "enum"`, `values = [...]`, `comment`.

## Resolution

`Registry.Resolve(name string) (*TypeDef, error)` -- Looks up a type by name. Returns error if not found. Used by model.Build() to expand column types into PG attributes.

## Composition rules

Column-level attributes override type-level:
- Column `nullable = true` overrides type's NotNull
- Column `default` overrides type's Default
- Column `default_expr` overrides type's DefaultExpr

Precedence: column > type > nothing (error if required fields missing after resolution).

## Validation on load

- Enum types must have at least one value
- Scalar types must have a valid base PG type (checked against allowlist)
- Check expressions must contain VALUE placeholder (for column-level checks)
- No circular type references (types cannot reference other user types as base -- only PG primitives)
