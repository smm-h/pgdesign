# Missing validation rules

Several validation rules described in internal/validate/DESIGN.md are not implemented.

## Missing error rules

- E200 missing-column-type: column has no type specified
- E212 fk-missing-index: FK columns lack an index (currently auto-generated in model.enrich, but no validation rule warns about it explicitly)
- E213 generated-col-refs-generated: generated column references another generated column
- E215 constraint-missing-not-valid: only applies in migration context, may belong in migrate/ instead

## Missing warning rules

- W003 boolean-states: multiple boolean columns that could be an enum state machine
- W004 json-could-be-table: jsonb column that should be a separate table
- W007 redundant-index: index is a subset of another index (distinct from E206 duplicate-index which checks prefix)

## Partial rules

- E204 fk-ref-not-found: checks table existence but not column reference validity
- E211 naming-convention: checks table and column names but not index names

## Missing extension validation

- Index method validation against declared extensions (only opclass is checked, not gin/gist method itself)

## Effort

Small per rule. Each rule is an independent function following the existing pattern.
