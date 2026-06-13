# JSONB shape validation

## Problem

pgdesign has `json` and `json_array` semantic types that map to `jsonb`, but there is no way to declare or enforce the expected structure of the JSON data inside a column. A `json` column is an opaque blob as far as pgdesign is concerned.

This means pgdesign cannot:

- Declare that a JSONB column must contain specific keys
- Enforce value types per key (e.g., `price` must be numeric, `tags` must be an array)
- Validate nested structure (objects within objects, arrays of typed objects)
- Generate CHECK constraints or `pg_jsonschema` validation from a declarative shape definition
- Audit whether a JSONB column's actual contents match a declared schema (via `introspect` or `diff`)

## Desired behavior

Allow TOML schema files to declare the expected shape of a JSONB column, e.g.:

```toml
[columns.metadata]
type = "json"

[columns.metadata.shape]
required = ["title", "price"]

[columns.metadata.shape.properties.title]
type = "string"

[columns.metadata.shape.properties.price]
type = "number"

[columns.metadata.shape.properties.tags]
type = "array"
items = "string"
```

The compiler could then:

1. Validate the shape definition itself during `pgdesign validate`
2. Generate a CHECK constraint using `pg_jsonschema` (if available) or hand-rolled `jsonb_typeof` checks
3. Include the shape in `pgdesign audit` to flag mismatches between declared and actual data
4. Use the shape in `pgdesign diff` to detect drift between schema and live database

## Considerations

- Should the shape definition follow JSON Schema vocabulary, or use a simplified pgdesign-native syntax?
- Should `pg_jsonschema` be a hard dependency, or should pgdesign generate fallback CHECK constraints using built-in Postgres functions?
- Nested shapes (objects within objects) add complexity to both the TOML definition and the generated SQL
- Array item types: homogeneous arrays are common (`["a", "b"]`), heterogeneous arrays (mixed types) are rare but exist
- How does this interact with the W004 lint rule that already suggests normalizing JSONB arrays into tables?

## Affected commands

- `generate`: emit CHECK constraints from shape definitions
- `validate`: validate shape definitions in TOML
- `audit`: check JSONB columns against declared shapes
- `diff`: include shape in schema comparison
- `introspect`: infer shape from live data (stretch goal)
