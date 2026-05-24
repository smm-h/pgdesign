# internal/validate

Strict validation engine. Operates on the resolved IR. Returns []Diagnostic.

## Architecture

Each rule is a function: `func(schema *model.Schema) []Diagnostic`. A registry holds all rules. `Validate(schema, ruleSet)` runs all rules and aggregates results.

## Strict rules (errors -- block generate)

| Code | Name | Description |
|------|------|-------------|
| E200 | missing-column-type | Column has no resolved PG type |
| E201 | fk-missing-on-delete | FK constraint has no ON DELETE clause |
| E202 | table-missing-comment | Table has no comment |
| E203 | table-missing-pk | Table has no primary key (no explicit pk, no id-typed column) |
| E204 | fk-ref-not-found | FK references a table or column that doesn't exist in schema |
| E205 | circular-fk | Circular FK dependency detected (warning, not error -- generates ALTER TABLE) |
| E206 | duplicate-index | Index is a prefix of another index on same table |
| E207 | varchar-usage | varchar(N) used; should use text + CHECK |
| E208 | timestamp-no-tz | timestamp without time zone used; should use timestamptz |
| E209 | serial-usage | serial/bigserial used; should use auto_id (identity) or id (uuid) |
| E210 | float-money | float/real/double used on column with money-related name |
| E211 | naming-convention | Table/column/index name violates configured naming pattern |
| E212 | fk-missing-index | FK column has no covering index and auto-index was not generated (should not happen but safety check) |
| E213 | generated-col-refs-generated | Generated column expression references another generated column |
| E214 | opclass-no-extension | Index uses opclass from extension not declared in [meta].extensions |
| E215 | constraint-missing-not-valid | FK/CHECK added without NOT VALID (migration safety -- only applies in migration context) |

## Anti-pattern warnings

| Code | Name | Description |
|------|------|-------------|
| W001 | god-table | Table has >30 columns. Suggest decomposition. |
| W002 | orphan-table | Table has no FK relationships (neither referencing nor referenced). May be intentional. |
| W003 | boolean-states | Table has 3+ boolean columns that suggest a state machine. Consider enum. |
| W004 | json-could-be-table | jsonb array column with consistent structure. Could be a separate table. |
| W005 | missing-timestamps | Non-join table has no created_at or updated_at column. |
| W006 | prefer-text | char(n) usage. Suggest text. |
| W007 | redundant-index | Index columns are a subset of another index (not just prefix). |

## Configurable rules

Rules can be enabled/disabled via pgdesign.toml:

```toml
[validate]
disable = ["W002", "W005"]
naming_pattern = "snake_case"
max_columns = 30
```

## Extension validation

When `[meta].extensions` declares extensions, validate/ checks:
- Index method references (gin, gist) are valid for declared extensions
- Opclass references (gin_trgm_ops, jsonb_path_ops) match a known extension from the registry
- Uses internal/extregistry for the lookup
