---
title: "typeinfo Package"
description: "Reference for the typeinfo package providing structured PostgreSQL type parsing, normalization, alias resolution, and SQL type string reconstruction."
---

# typeinfo Package

The `typeinfo` package provides structured PostgreSQL type representation. It parses raw type strings (from TOML schemas, `format_type()` output, etc.) into a normalized struct and reconstructs SQL type strings for DDL generation.

## Type Struct

```go
type Type struct {
    Base       string // normalized short-form PG type: "varchar", "timestamptz", "int4", "bool"
    DomainName string // domain name when column is domain-backed (populated by build.go, NOT by Parse)
    Params     Params
}

type Params struct {
    Precision   *int   // for timestamp, time, interval, numeric
    Scale       *int   // for numeric (second param)
    Length      *int   // for varchar, char, bit, varbit
    RawModifier string // raw parenthesized portion for extension types
}
```

- `Base`: The canonical short-form PostgreSQL type name, normalized via the alias map. For array columns, includes the `[]` suffix (e.g., `"text[]"`, `"int4[]"`).
- `DomainName`: Set only by `model/build.go` during `Schema.Build()` when a column's type resolves to a scalar type with a CHECK constraint (producing a CREATE DOMAIN). Never set by `Parse()`. When present, `Reconstruct()` returns the domain name directly.
- `Params`: Structured type parameters. For known PG types, the appropriate named field is populated. For unknown/extension types (e.g., `vector(1536)`), only `RawModifier` is set.

## Alias Map

Parse normalizes all type names through this alias map, which maps verbose PostgreSQL type names to their canonical short-form equivalents. All input is lowercased before lookup. This normalization ensures that type comparisons between TOML schema definitions and introspected live database types produce consistent results regardless of how the type was originally specified. Types not found in the map pass through unchanged.

| Input | Canonical Output |
|-------|-----------------|
| `character varying` | `varchar` |
| `character` | `char` |
| `char` | `char` |
| `double precision` | `float8` |
| `boolean` | `bool` |
| `integer` | `int4` |
| `smallint` | `int2` |
| `bigint` | `int8` |
| `real` | `float4` |
| `timestamp with time zone` | `timestamptz` |
| `timestamp without time zone` | `timestamp` |
| `time with time zone` | `timetz` |
| `time without time zone` | `time` |
| `bit varying` | `varbit` |
| `int` | `int4` |
| `float` | `float8` |
| `decimal` | `numeric` |
| `serial` | `serial` |
| `bigserial` | `bigserial` |
| `smallserial` | `smallserial` |

Types not in the alias map pass through unchanged (e.g., `text`, `uuid`, `jsonb`, `int4range`, extension types like `vector`).

## Parse API

```go
func Parse(raw string) Type
```

Parses a raw PostgreSQL type string into a structured Type. Handles:

- Case normalization (all input lowercased)
- Alias resolution (e.g., `"integer"` becomes `"int4"`)
- Parameter extraction (e.g., `"varchar(255)"` extracts Length=255)
- Multi-word types with interior parameters (e.g., `"timestamp(3) with time zone"` becomes Base=`"timestamptz"`, Precision=3)
- Array suffix handling (e.g., `"integer[]"` becomes Base=`"int4[]"`)

DomainName is never set by Parse.

### Parameter Semantics

The parameter string inside parentheses is interpreted differently based on the resolved base type. For numeric types, the first parameter is precision and the optional second is scale. For timestamp and time types, the single parameter controls fractional second precision. For string and bit types, the parameter specifies the maximum length. Extension types and any unrecognized types store the raw parenthesized value in RawModifier for passthrough in DDL generation.

| Base Type | Single Param Meaning | Two Params |
|-----------|---------------------|------------|
| `numeric`, `decimal` | Precision | Precision, Scale |
| `timestamp`, `timestamptz`, `time`, `timetz`, `interval` | Precision | N/A |
| `varchar`, `char`, `varbit`, `bit` | Length | N/A |
| All others | RawModifier | N/A |

## Reconstruct API

```go
func Reconstruct(t Type) string
```

Rebuilds a SQL type string from a Type struct for DDL generation.

Rules:
1. If `DomainName` is set, return it directly (domain-backed columns use the domain name in DDL).
2. If `Base` is empty, return empty string.
3. Strip `[]` suffix from Base, reconstruct params, then re-append `[]`.
4. Parameter reconstruction follows the same type-to-field mapping as parsing: numeric uses Precision+Scale, timestamp/time/interval use Precision, varchar/char/bit/varbit use Length.
5. `RawModifier` is used as fallback when no named fields are set.

Examples:
- `Type{Base: "varchar", Params: Params{Length: ptr(255)}}` produces `"varchar(255)"`
- `Type{Base: "timestamptz", Params: Params{Precision: ptr(3)}}` produces `"timestamptz(3)"`
- `Type{Base: "numeric", Params: Params{Precision: ptr(12), Scale: ptr(6)}}` produces `"numeric(12,6)"`
- `Type{Base: "vector", Params: Params{RawModifier: "1536"}}` produces `"vector(1536)"`
- `Type{Base: "varchar[]", Params: Params{Length: ptr(100)}}` produces `"varchar(100)[]"`
- `Type{DomainName: "email_address", Base: "varchar"}` produces `"email_address"`
- `Type{Base: "text"}` produces `"text"`

## DomainName Semantics

The `DomainName` field exists to support scalar types with CHECK constraints that produce `CREATE DOMAIN` statements. When a column's type resolves to such a scalar type during `Schema.Build()`, `build.go` populates `DomainName` with the domain name.

This separation is deliberate:
- `Parse()` deals with raw PostgreSQL type strings and has no knowledge of the schema's type system.
- `DomainName` is schema-level metadata that requires type resolution.
- `Reconstruct()` checks `DomainName` first, ensuring domain-backed columns use the domain name in DDL rather than the underlying PG type.

## Default Precision Table

PostgreSQL assigns implicit defaults when types are specified without explicit parameters. These are used by the `diff` package for type comparison (a type without explicit precision equals the same type with its default precision):

| Base Type | Default Value | Meaning |
|-----------|--------------|---------|
| `timestamp` | 6 | Microsecond precision |
| `timestamptz` | 6 | Microsecond precision |
| `time` | 6 | Microsecond precision |
| `timetz` | 6 | Microsecond precision |
| `interval` | 6 | Full range of fields |
| `bit` | 1 | Default bit length |
| `numeric` | None (arbitrary) | Omitting params gives arbitrary precision, not 0 |
| `varchar` | None (unlimited) | Omitting length gives unlimited, same as text |

Note: These defaults are not part of the `typeinfo` package itself but are used by `diff.typesEqualWithDefaults()` to avoid false positives when comparing types from TOML schemas against introspected live database types.

## Helper Functions

- `T(base string) Type` -- Concise constructor for test literals. Returns `Type{Base: base}` with zero-value params.
- `MustParse(raw string) Type` -- Parse with panic on empty input. Intended for test setup only.
- `Equal(other Type) bool` -- Deep comparison of two Types including all Params pointer fields.

## Consumers

The typeinfo package is imported by semtype, model, validate, generate, codegen, diff, migrate, seed, sql, and introspect, making it one of the most widely used internal packages. It sits at the bottom of the dependency graph with no internal dependencies, providing the foundation for type handling throughout the entire compilation pipeline from parsing through code generation and migration planning.
