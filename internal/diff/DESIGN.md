# internal/diff

Schema differ. Compares two IR schemas (desired vs actual) and produces a structured diff.

## Function

`Diff(desired, actual *model.Schema) *SchemaDiff`

## Types

- `SchemaDiff` -- TablesAdded ([]TableDiff), TablesRemoved ([]string), TablesChanged ([]TableDiff), EnumsAdded/Removed/Changed, ExtensionsAdded/Removed.
- `TableDiff` -- TableName, ColumnsAdded ([]ColumnDiff), ColumnsRemoved ([]string), ColumnsChanged ([]ColumnChange), FKsAdded/Removed/Changed, IndexesAdded/Removed/Changed, UniquesAdded/Removed/Changed, ChecksAdded/Removed/Changed, CommentChanged (*string), PKChanged, PartitioningChanged, OwnerChanged.
- `ColumnChange` -- Name, TypeChanged (old->new), NullableChanged, DefaultChanged, GeneratedChanged, CommentChanged.
- Each change carries a `risk.Classification` (from internal/risk).

## Matching algorithm

1. Match tables by qualified name (schema.table).
2. For matched tables: match columns by name. Match constraints/indexes by name.
3. Unmatched in desired = added. Unmatched in actual = removed. Matched with differences = changed.

## Type change classification

Uses a type compatibility matrix:
- Widening (safe): int->bigint, varchar(50)->varchar(100), varchar->text
- Narrowing (dangerous): bigint->int, varchar(100)->varchar(50), text->varchar(50)
- Lateral (dangerous): int->text, text->int, uuid->bigint
- Same family resize: adjusts based on direction

## Default expression comparison

Defaults are compared as normalized strings (lowercase, trim whitespace). Known equivalences:
- `now()` == `now()` (exact match only; does NOT consider CURRENT_TIMESTAMP equivalent because they have different semantics in some contexts)

If defaults differ textually, it's a change -- even if semantically equivalent. Conservative approach: flag it, let user decide.

## Enum changes

- Values added at end: safe (ALTER TYPE ADD VALUE)
- Values removed: dangerous (requires type recreation)
- Values reordered: dangerous (requires type recreation)
- Values added in middle: safe in PG14+ (ADD VALUE BEFORE/AFTER), dangerous in older versions

## Empty diff

If desired == actual, `SchemaDiff.IsEmpty()` returns true. CLI reports "schema is up to date."

## CLI output

Human-readable diff with color:
- Green `+` for additions
- Red `-` for removals
- Yellow `~` for changes (with old->new)
- Risk level badge on each change: [SAFE] [CAUTION] [DANGEROUS]
