---
title: "Seed Data Generation"
description: "How pgdesign generates type-aware test data for all 42 PostgreSQL types with CHECK/UNIQUE awareness, FK dependency ordering, and multiple output formats."
---

# Seed Data Generation

The seed generator produces type-aware test data for all tables in a pgdesign schema. It respects FK dependencies, CHECK constraints, UNIQUE constraints, semantic types, and column nullability. Output is either INSERT statements or PostgreSQL COPY format.

Source: `internal/seed/seed.go`, `internal/seed/regen.go`

## Overview

The entry point is `Generate(schema, rowsPerTable, rng, cfg)`. It walks tables in topological order (`Schema.TableOrder()`), generates row data for each table, and emits SQL. The `*rand.Rand` parameter enables deterministic output when seeded with a fixed value.

The generation pipeline per table:

1. Analyze CHECK constraints to extract hints (range, length, regex)
2. Determine which FK columns participate in cycles (need deferred UPDATE)
3. Filter out columns that should not appear in output (generated columns, `ALWAYS` identity, `auto_id` semantic type)
4. For COPY format, additionally exclude serial/bigserial/smallserial columns (COPY cannot use DEFAULT)
5. Build unique constraint tracking (including PK as a unique constraint)
6. Generate rows with retry loop for uniqueness (up to 100 attempts per row)
7. Emit in the configured format
8. After all tables, emit deferred UPDATE statements for FK cycle resolution

## Regex String Generation

Located in `internal/seed/regen.go`. Uses Go's `regexp/syntax` package to walk the regex AST and generate matching strings.

### Algorithm

1. Parse the regex pattern with `regexp/syntax.Parse()` using Perl syntax
2. Simplify the AST via `re.Simplify()`
3. Walk the AST recursively via `regenWalk`, handling each `syntax.Op` type:
   - `OpLiteral`: emit the literal runes directly
   - `OpCharClass`: pairs of `[lo, hi]` ranges; pick a range uniformly, then pick a rune uniformly within that range
   - `OpAnyCharNotNL` / `OpAnyChar`: random printable ASCII character (code points 33-126)
   - `OpCapture`: recurse into the sub-expression
   - `OpConcat`: generate each sub-expression in sequence
   - `OpAlternate`: pick one branch uniformly at random
   - `OpStar` (0+): generate 0 to `regenDefaultRepeat` (5) repetitions
   - `OpPlus` (1+): generate 1 to `regenDefaultRepeat` (5) repetitions
   - `OpQuest` (0 or 1): 50% chance of generating the sub-expression
   - `OpRepeat`: generate between `Min` and `Max` repetitions; if `Max` is unbounded (-1), cap at `Min + regenDefaultRepeat`
   - Anchors (`OpBeginLine`, `OpEndLine`, `OpBeginText`, `OpEndText`), `OpWordBoundary`, `OpNoWordBoundary`, `OpEmptyMatch`, `OpNoMatch`: no output
4. Return the generated string

### Use Cases

- CHECK constraints with regex patterns (e.g., `col ~ '^[A-Z]{3}-\d{4}$'`)
- Ensures generated data passes constraint validation without rejection sampling

## Distributions

### Zipf Distribution (FK References)

- Parameters: `s=1.5`, `v=1`, upper bound = referenced table's row count minus 1
- Implementation: Go's `rand.NewZipf` with the parent table's generated value list
- Applied to all FK columns via the `ref` semantic type and during cycle resolution UPDATEs

In real databases, FK distributions are highly skewed -- some parent rows are referenced far more often than others (popular categories, active users). A Zipf distribution with s=1.5 models this: the first few parent rows absorb the majority of references, producing realistic skew rather than uniform spread.

### Log-Normal Distribution (Money)

- Used when `SemanticTypeName == "money"`
- Formula: `exp(NormFloat64() * 1.0 + 4.0)`, then `abs()`, then capped at 999999
- Produces values centered around `exp(4) ~= 55` with a long right tail
- Many small amounts, few large amounts -- representative of financial data (prices, transactions, balances)

### Uniform Distribution (Default)

- Used for all other column types
- Integers: `Intn(10000)`, dates: random day within past year, timestamps: random date + random HH:MM:SS
- Range types generate a random lower bound and a random span

### NULL Injection

- 10% probability for nullable columns (checked via `col.NotNull == false`)
- Applied before type-specific generation -- if the coin flip says NULL, the value is NULL regardless of type
- NOT NULL columns never receive NULL values

## Constraint-Aware Generation

### CHECK Constraint Parsing

The `analyzeChecks` function parses each table's CHECK constraints using `sqlexpr.Parse()` and extracts hints for three recognized patterns:

**Range constraints** (`col >= 18 AND col <= 65`): The `extractRange` function recognizes an AND node with comparison operators (`>=`, `<=`, `>`, `<`) on both sides referencing the same column. It handles both `col >= X` and `X <= col` orientations. Strict inequalities (`>`, `<`) are adjusted by 1 to produce inclusive bounds. Generated values are uniformly distributed within `[min, max]`.

**Length constraints** (`length(code) <= 5`): The `extractLength` function recognizes a comparison where the left side is a function call to `length`, `char_length`, or `character_length`. The base string is generated normally via `generateBaseString`, then truncated to the maximum length.

**Regex constraints** (`code ~ '^[A-Z]{3}-[0-9]{4}$'`): The `extractRegex` function recognizes `~` or `~*` operators where the right side is a string literal. The pattern is passed to `regenFromPattern` to generate a matching string directly.

### Priority

CHECK hints take priority over default type-based generation. When a column matches a hint, the hint's strategy is used instead of the type's default. If `regenFromPattern` fails (invalid regex), the generator falls back to default string generation.

### UNIQUE Enforcement

- A `uniqueTracker` maintains a set of seen value combinations per constraint
- Both explicit `UNIQUE` constraints and the primary key are tracked
- For composite constraints, the tuple of column values is joined with a null byte separator
- Up to 100 retries per row to find a non-duplicate combination
- If all attempts fail, the last generated values are used anyway (no hard error)

## Column Filtering

These column types are excluded from generated output:

- **Generated columns** (`col.Generated != ""`): PostgreSQL computes these automatically
- **ALWAYS identity columns** (`col.Identity == "ALWAYS"`): PostgreSQL assigns values automatically
- **auto_id semantic type**: treated as server-generated; emits DEFAULT in INSERT format
- **Serial types** (serial, bigserial, smallserial): emit DEFAULT in INSERT format; excluded entirely in COPY format (COPY cannot use DEFAULT)

## FK Cycle Handling

### The Problem

Circular FK references (e.g., `departments.head_id -> employees.id`, `employees.dept_id -> departments.id`) create a chicken-and-egg problem: you cannot insert into either table without the other existing first.

### Two-Pass Solution

The generator uses `Schema.CycleGroups` (pre-computed by the model builder) to identify which tables participate in cycles and which specific FK columns create the cycle.

1. **Pass 1 -- INSERT with NULLs**: For tables in a cycle, FK columns pointing to cycle peers are set to NULL in the initial INSERT. All other columns are generated normally. PK values are tracked for later use.
2. **Pass 2 -- UPDATE with values**: After all tables have been seeded, deferred UPDATE statements backfill the NULL FK columns with Zipf-distributed references to the now-existing parent rows. Each row gets its own UPDATE statement keyed on the PK.

### S001 Diagnostic

If a cycle-breaking FK column is declared NOT NULL, the generator emits a `S001` warning (severity: Warning). The two-pass strategy requires nullable cycle columns. The developer must either make one FK in the cycle nullable or restructure the schema to break the cycle.

## Output Formats

### INSERT Format

- Standard `INSERT INTO schema.table (col1, col2, ...) VALUES` with value tuples
- Batch INSERT: up to 1000 rows per statement (hardcoded `batchSize = 1000`). Tables with more rows produce multiple INSERT statements.
- Each value tuple is indented with two spaces
- SQL literal escaping: single quotes doubled (`''`)
- Arrays use `ARRAY[val1, val2, ...]` syntax
- Function calls (e.g., `NOW() - interval '...'`) used for timestamp semantic types
- Type casts (e.g., `::jsonb`, `::tsvector`) appended where needed

### COPY Format

PostgreSQL `COPY ... FROM stdin` for bulk loading. Faster than INSERT for large datasets.

- Header: `COPY schema.table (col1, col2, ...) FROM stdin;`
- Rows: tab-separated values, one per line
- NULL: represented as `\N`
- Booleans: `t` / `f` (not `true` / `false`)
- Arrays: PostgreSQL array literal format `{val1,val2,val3}` (converted from `ARRAY[...]` syntax)
- Type casts stripped (e.g., `::jsonb` removed)
- Function calls (NOW(), etc.) converted to literal timestamps
- Escaping via `escapeCopy`: backslash-escapes for `\`, `\t`, `\n`, `\r`
- Terminator: `\.` on its own line
- Serial columns excluded from the column list (COPY cannot use DEFAULT)

The `toCopyValue` function handles the conversion from INSERT-format SQL values to COPY-format values, including stripping type casts, converting array syntax, unquoting SQL strings, and re-escaping for the COPY protocol.

### Transaction Wrapping

- Default mode: output is wrapped in `BEGIN;` / `COMMIT;`
- Apply mode (`cfg.Apply == true`): no transaction wrapping, since the caller manages its own transaction

## Edge-Case Mode (`--mode edge-cases`)

When `cfg.Mode == "edge-cases"`, the generator forces exactly 1 row per table (ignoring the `rowsPerTable` parameter) and produces boundary values designed to stress-test application code.

Values by type:

| Type | Edge-case value |
|------|----------------|
| UUID / `id` semantic type | `'00000000-0000-0000-0000-000000000000'` |
| text, varchar, char, email, short_text, slug | `''` (empty string) |
| boolean / flag | `true` |
| integer | First column: `0`, subsequent: `2147483647` (MAX_INT) |
| bigint | First column: `0`, subsequent: `9223372036854775807` |
| smallint | First column: `0`, subsequent: `32767` |
| money, counter, numeric, real/float | `0` |
| timestamp, timestamptz | `'0001-01-01 00:00:00'` |
| date | `'0001-01-01'` |
| time, timetz | `'00:00:00'` |
| interval | `'0'` |
| bytea | `'\\x'` (empty bytes) |
| inet | `'0.0.0.0'` |
| cidr | `'0.0.0.0/0'` |
| macaddr | `'00:00:00:00:00:00'` |
| jsonb, json | `'null'::jsonb` / `'null'::json` |
| xml | `'<empty/>'` |
| tsvector, tsquery | empty cast (`''::tsvector`) |
| Range types (int4range, etc.) | `'empty'` |
| Multirange types | `'{}'::type` (empty multirange) |
| Array columns | `ARRAY[]::type[]` (empty array with cast) |
| Nullable columns | `NULL` |
| FK references | First generated value from parent table |
| Enums | First declared value |

Integer edge-case values rotate between 0 and MAX via a per-PGType counter (`intTypeCounter`), so if a table has two integer columns, the first gets 0 and the second gets MAX_INT.

## Semantic Type Handling

Semantic types (`col.SemanticTypeName`) take priority over PGType-based generation:

| Semantic type | Generation strategy |
|--------------|---------------------|
| `id` | Random UUID v4 |
| `auto_id` | `DEFAULT` |
| `ref` | Zipf-distributed pick from parent table's generated values |
| `email` | `'user{rowIdx}@example.com'` |
| `short_text` | `'sample_{table}_{col}_{rowIdx}'` |
| `timestamp`, `timestamp_optional` | `NOW() - interval '{N} days'` (random N in 0-364) |
| `money` | Log-normal distribution, capped at 999999 |
| `counter` | Uniform `Intn(10000)` |
| `flag` | Random boolean |
| `slug` | `'{table}-{rowIdx}'` |
| `json` | JSON object with `_schema` key if `json_schema` is set, else `'{}'::jsonb` |
| `json_array` | `'[]'::jsonb` |
| enum name | Random pick from declared enum values |

If the semantic type name matches an enum name, a random enum value is selected. The same enum lookup applies to `col.PGType` as a fallback.

## Type Coverage

The generator handles these PostgreSQL types:

- **Numeric**: boolean, integer, bigint, smallint, serial, bigserial, smallserial, numeric, real, float4, float8
- **String**: text, citext, varchar, char
- **Binary**: bytea (random 8-byte hex)
- **Date/time**: date, time, timetz, timestamp, timestamptz, interval
- **UUID**: uuid (v4 format with correct version/variant bits)
- **Network**: inet (random IPv4), cidr (random /24), macaddr (random 6-byte)
- **JSON**: json (`'{}'::json`), jsonb (`'{}'::jsonb` or schema-aware generation)
- **XML**: xml (`'<val>item_{rowIdx}</val>'`)
- **Text search**: tsvector, tsquery (random word pairs from a 10-word vocabulary)
- **OID**: oid (random integer 1-100000)
- **Range**: int4range, int8range, numrange, daterange, tsrange, tstzrange (half-open `[lo, hi)` intervals)
- **Multirange**: int4multirange, int8multirange, nummultirange, datemultirange, tsmultirange, tstzmultirange (single-range multirange literals)
- **User-defined enums**: random pick from declared values
- **Array columns**: 1-5 elements of the base type, using `ARRAY[...]` syntax
- **Unknown types**: `NULL::typename /* unsupported seed type */` -- generates valid SQL while making the gap visible

## Per-Table Row Counts

Configurable via `SeedConfig.TableRows` (map of table name to row count). Tables not in the map use the `rowsPerTable` default. In edge-cases mode, the row count is forced to 1 regardless of configuration.

## Clean Flag

When `SeedConfig.Clean == true`, generates a `TRUNCATE ... CASCADE` statement before any seed data. Tables are listed in reverse dependency order (reverse of `Schema.TableOrder()`) so that child tables appear before parent tables in the TRUNCATE list.

## Deterministic Output

The `*rand.Rand` parameter controls all random decisions. Passing the same seed produces identical output across runs -- useful for reproducible test data in CI. The `--seed` CLI flag surfaces this capability.

## JSONB Schema-Aware Generation

When a JSONB column has a `json_schema` attribute (referencing an external JSON Schema file), the generator produces a JSON object containing:
- `_schema`: the schema file reference
- `id`: a random integer

This ensures the generated JSON has a recognizable structure rather than an empty object.
