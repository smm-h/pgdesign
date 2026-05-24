# internal/parse

TOML parser using go-toml-edit AST walk. Lenient -- extracts structure without enforcing semantic rules.

## Input

One or more `.toml` schema files. Multi-file projects resolved via pgdesign.toml config.

## Output

`*RawSchema` + `[]Diagnostic`. Raw structs contain everything the TOML declared, in source order.

## Parsing approach

Uses go-toml-edit's `tomledit.Parse()` to get a `*DocumentNode`, then walks `Children` to extract sections. This preserves declaration order (critical for columns). Does NOT use `Unmarshal` because Go maps lose key ordering.

Walking pattern:
1. Extract `[meta]` section: version, schema, extensions.
2. Extract `[types.*]` section: iterate table children for each type definition.
3. Extract `[tables.*]` section: for each table, walk children for columns, fks, indexes, unique, checks, partitioning, dependencies, maintenance.

Column order within each table is preserved as a slice, not a map.

## Raw types

- `RawSchema` -- Meta, Types (ordered), Tables (ordered).
- `RawMeta` -- Version (int), Schema (string), Extensions ([]string).
- `RawType` -- Name, Kind, BaseType, Values, NotNull, Default, DefaultExpr, Check, Unique, Comment.
- `RawTable` -- Name, Comment, PK ([]string), Columns (ordered slice), FKs (map by name), Indexes (map by name), Uniques (map by name), Checks (map by name), Partitioning, Dependencies, Maintenance.
- `RawColumn` -- Name, Type, Nullable, Default, DefaultExpr, Generated, Stored, Comment.
- `RawFK` -- Name, Columns, RefTable, RefColumns, OnDelete.
- `RawIndex` -- Name, Columns, Method, Opclass, Where, Include, Unique.
- `RawUnique` -- Name, Columns.
- `RawCheck` -- Name, Expr.
- `RawPartitioning` -- Strategy (range|list|hash), Column, Partitions (recursive children).
- `RawDependency` -- Determinant ([]string), Dependent ([]string).
- `RawMaintenance` -- Premake (int), Retention (string), RetentionKeepTable (bool).

## Error recovery

Parser continues past errors. Unknown keys produce W-diagnostics. Type mismatches and missing required fields produce E-diagnostics. A schema with errors still produces a partial RawSchema so downstream passes can report additional issues.

## Multi-file support

`parse.Directory(dir string, config *ProjectConfig) (*RawSchema, []Diagnostic)` reads all TOML files listed in pgdesign.toml (or globs), merges them into a single RawSchema. Cross-file FK references work because all tables are merged before resolution.
