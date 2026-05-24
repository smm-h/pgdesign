# internal/format

Canonical TOML formatter. Implements `pgdesign fmt`.

## Purpose

Normalizes schema TOML files to a single canonical form. Eliminates git conflicts between independent edits by ensuring every valid schema has exactly one textual representation.

## Function

`Format(input []byte, config *FormatConfig) ([]byte, error)`

Reads TOML via go-toml-edit (preserving comments), reorders sections and keys to canonical form, writes back using go-toml-edit's `Format()` for consistent whitespace/indentation.

## Canonical ordering rules

Configured via pgdesign.toml `[format]` section. Defaults:

### Top-level sections
Always: `[meta]` -> `[types.*]` -> `[tables.*]`

### [meta] fields
Fixed: version, schema, extensions

### Types
Alphabetical by type name.

### Tables
Configurable (`table_order`):
- `dependency` (default): FK targets before FK sources (topo sort). Alphabetical for ties and cycle members.
- `alphabetical`: strict alphabetical by table name.

### Within a table
Fixed: comment -> pk -> columns -> fks -> indexes -> unique -> checks -> partitioning -> maintenance -> dependencies

### Columns within a table
Configurable (`column_order`):
- `pk_fk_alpha` (default): PK columns first, then FK columns (alphabetical among themselves), then remaining columns alphabetically.
- `alphabetical`: all columns alphabetically regardless of role.
- `fk_last`: PK first, then non-FK non-PK alphabetically, then FK columns last.
- `preserve`: do not reorder columns (keep source order). Only normalizes whitespace/indentation.

### FKs, indexes, unique, checks
Alphabetical by constraint/index name.

### Dependencies
Preserved in declaration order (no reordering -- order may be semantically meaningful to the user).

## Check mode

`Format(input, config)` returns the canonical form. CLI compares input to output:
- If identical: exit 0 (file is canonical)
- If different: exit 1, optionally print diff

## Comment preservation

go-toml-edit preserves all comments through reordering. Comments attached to a key/section move with that key/section when reordered. Standalone comments (not attached to any key) stay at their relative position.

## Multi-file

`pgdesign fmt` accepts a file or directory. When given a directory, formats all `.toml` files listed in pgdesign.toml's `[project].schemas` list.
