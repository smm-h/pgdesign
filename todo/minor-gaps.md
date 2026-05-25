# Minor implementation gaps

Smaller items that don't warrant individual todo files.

## Dead code / unwired features

- `--strict-nf` global CLI flag: registered but never read. Should promote NF violations to Error severity and cause generate to refuse DDL output.
- `generate --format json`: advertised as a valid format choice but returns "not implemented"
- `Options.PGVersion` in generate: field exists but is never read; no PG-version-conditional syntax

## Model improvements

- CandidateKeys() recomputes every call; DESIGN.md says "cached after first computation"
- Constraint naming in model.enrich() uses hardcoded fmt.Sprintf; should use sql.ConstraintName()
- Index.Unique field absent from model IR (RawIndex.Unique has no place in resolved model)
- Index.Opclass is a single string; should be per-column

## SQL builder

- CreateIndex missing CONCURRENTLY flag support
- No DO $$ wrapper for idempotent constraint statements (ALTER TABLE ADD CONSTRAINT has no IF NOT EXISTS guard)
- Per-column opclass support (currently one opclass for whole index)

## Diff

- GeneratedChanged field missing from ColumnChange struct (Generated/Stored not compared)
- Enum position-aware diffing: no reorder detection, no middle-insert detection, no PG version dependent classification

## Diagnostic

- RenderTerminal does not group by file then severity as specified; renders in insertion order

## Extregistry

- pg_partman and pg_cron register unqualified function names (create_parent vs partman.create_parent)
- btree_gin and btree_gist have limited opclass coverage

## Introspect

- Export uses manual string building, not go-toml-edit
- No configurable timeout (uses context.Background with no deadline)

## Dependencies

- BurntSushi/toml used for migration file parsing alongside go-toml-edit (unlisted dependency; consider consolidating)

## Documentation pages

docs/DESIGN.md lists 5 manual doc pages that don't exist:
- docs/format-reference.md
- docs/semantic-types.md
- docs/validation-rules.md
- docs/migration-guide.md
- docs/quickstart.md
