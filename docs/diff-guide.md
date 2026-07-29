---
title: "Diff Guide"
description: "Guide to pgdesign's diff command: comparing your TOML schema against a live database, another TOML file, or a git ref, with risk-annotated output."
---

# Diff Guide

The `pgdesign diff` command compares your current TOML schema against a target and reports every difference: tables, columns, enums, views, functions, constraints, indexes, policies, sequences, domains, composite types, state machines, and partitioning configuration. Each column change carries a risk classification (Safe, Caution, or Dangerous) so you know what will happen before you run a migration.

Source: `cmd/pgdesign/handlers_diff.go`, `internal/diff/`

:-: ref path="internal/diff" lang="go"

## When to use diff

Use `diff` whenever you want to preview what a migration would change without actually generating or applying one. It answers the question: "what is different between what I have declared and what exists somewhere else?"

Common scenarios:

- You edited the TOML schema and want to see what changed before running `migrate generate`
- You want to verify that a running database matches your schema (drift detection)
- You want to compare your working copy against a branch or tag to see the schema delta of a PR
- You want to compare two independent TOML schema files

## The three modes

The diff command has exactly three modes, specified by mutually exclusive flags: `--live` compares against a running database, `--against` compares two TOML files, and `--base` compares against a git ref. You must pass exactly one flag per invocation.

### `--live` -- compare against a running database

```
pgdesign diff schema.toml --live postgres://user:pass@localhost/mydb
```

Connects to the PostgreSQL database, introspects its catalog (tables, columns, types, constraints, indexes, policies, triggers, functions, sequences, views, materialized views), and compares the result against your compiled TOML schema. The connection URL can also come from the `PGDESIGN_DB` environment variable.

This mode uses `introspect.Introspect` to read the live database, then runs `DiffLive` -- a class-aware comparison that suppresses semantic type name comparisons (the database has no concept of pgdesign's semantic types, so comparing would produce false positives).

When the connection succeeds, pgdesign also performs **live round-trip normalization**: it round-trips boolean predicates (CHECK expressions, partial-index WHERE clauses, exclusion WHERE clauses, policy USING/WITH CHECK expressions) from the desired side through the target database. PostgreSQL computes its own canonical form, resolving catalog-dependent cast differences (e.g., `status = 'active'` vs `status = 'active'::text`) that no pure normalizer can reach. This is best-effort -- if the round-trip connection fails, the diff still runs without it.

The live server's PostgreSQL version is resolved onto the desired model before diffing, so a pinned-but-stale `[meta].version` does not surface as a spurious `pg_version changed` line.

Use this mode to:

- Detect schema drift in a deployed database
- Verify that a migration was applied correctly
- Audit a database against the declared schema

### `--against` -- compare against another TOML file

```
pgdesign diff schema.toml --against other/schema.toml
```

Parses and builds both TOML schemas independently, then runs a full `Diff` comparison between them. Both sides are registry-present models, so all fields are compared -- including semantic type names, which affect codegen output even when the underlying PostgreSQL type is identical.

Use this mode to:

- Compare two versions of a schema stored in different files
- Diff a feature branch schema against the main branch schema (when both are checked out)
- Compare schemas from different projects to find structural differences

### `--base` -- compare against a git ref

```
pgdesign diff schema.toml --base main
pgdesign diff schema.toml --base HEAD~3
pgdesign diff schema.toml --base v0.5.0
```

Extracts the schema files from the specified git ref (branch, tag, or commit) using `git show`, parses and builds them, then diffs against your current working-copy schema. The ref can be anything `git show` accepts: a branch name, a tag, a commit SHA, or a relative ref like `HEAD~1`.

The command resolves which files to extract by reading `pgdesign.toml` from the git ref (if it exists at that ref) to find the `project.schemas` list. If the config file does not exist at the ref, it falls back to the same file paths as the current invocation.

Like `--against`, both sides are registry-present models, so semantic type names are compared.

Use this mode to:

- Review schema changes in a PR (diff against `main`)
- See what changed since a specific release (diff against a version tag)
- Inspect the schema delta between any two points in history

## Reading the diff output

### Terminal output (default)

The default output is a colored terminal format showing additions, removals, and modifications across all schema object types. The first line is a summary count of changes (e.g., "3 additions, 2 removals, 1 change"), followed by per-object details with risk classification.

**Symbols:**

| Symbol | Color | Meaning |
|--------|-------|---------|
| `+` | green | Added (new object) |
| `-` | red | Removed (object deleted) |
| `~` | yellow | Changed (object modified) |

**Object types reported:** extensions, enums, tables, views, materialized views, composite types, domains, sequences, state machine transitions, functions, state machine types.

**Column changes** include a risk badge:

| Badge | Meaning |
|-------|---------|
| `[SAFE]` | No data risk. Default changes, comment changes, safe widenings (e.g., `integer` to `bigint`). |
| `[CAUTION]` | Potential data impact. Requires careful review. |
| `[DANGEROUS]` | Data loss or table rewrite risk. Type narrowing, dropping NOT NULL on a populated column, collation changes. |

Each changed column lists the specific fields that differ (`type`, `nullable`, `default`, `comment`, `generated`, `stored`, `identity`, `array`, `collation`, `json_schema`, `statistics`, `semantic_type`), showing the old and new values.

**Enum changes** distinguish between safe appends (values added at the end) and middle insertions (which require `BEFORE`/`AFTER` syntax in `ALTER TYPE`). Reordering is flagged as dangerous.

**Table changes** detail every sub-object: columns, foreign keys, indexes, unique constraints, check constraints, exclusion constraints, triggers, policies, RLS settings, partitioning, maintenance (partman) config, and append-only status.

Example output:

```
2 table(s) changed (3 column(s) modified), 1 enum(s) changed

~ enum order_status
  + cancelled (safe, appended)

~ table orders
  + column tracking_number text [SAFE]
  ~ column amount [CAUTION]
    type: integer -> bigint
  - column legacy_code

~ table users
  ~ column email [SAFE]
    default: "" -> "unknown@example.com"
```

### JSON output (`--json`)

Pass `--json` for machine-readable output. The JSON structure mirrors the `SchemaDiff` type exactly, with fields for every object category (`tables_added`, `tables_removed`, `tables_changed`, `enums_added`, etc.). Empty arrays are included; empty optional fields are omitted.

```
pgdesign diff schema.toml --live $PGDESIGN_DB --json
```

The JSON output is useful for CI pipelines, automated drift detection, or feeding into other tools.

### Empty diff

An empty diff means the schema and target are semantically identical after normalization -- every object type (tables, columns, constraints, indexes, views, functions, sequences, policies, triggers) matches between the two sides. When the schema matches the target exactly, the output format determines the representation:

- Terminal: prints `Schema is up to date.`
- JSON: all arrays are empty, all optional fields are null

## Normalization

The diff engine applies 4 categories of normalization before comparing to avoid false positives from cosmetic differences that do not represent real schema changes. Without normalization, differences in whitespace, keyword casing, default precision values, and type aliases would surface as spurious schema drift:

- **SQL expressions** (CHECK constraints, index WHERE clauses, policy expressions, generated column expressions, trigger WHEN conditions) are compared via the `sqlparse.ExprEqual` normalizer, which handles whitespace, case folding of keywords, and cast alias normalization
- **Default values** are normalized: expression defaults go through the SQL normalizer; literal defaults are compared exactly (case-sensitive, since `'Active'` and `'active'` are distinct values)
- **Type precision** uses default-aware comparison: `timestamp` (no precision) equals `timestamp(6)` because 6 is PostgreSQL's default microsecond precision
- **Index methods** default to `btree` when unspecified, matching PostgreSQL's behavior
- **Policy types** default to `PERMISSIVE` when unspecified
- **Interval strings** in partman config normalize unit words (`month`/`mon`/`mons` all compare equal)

## The rename gate

When the diff detects a table or column that was removed and another was added with an identical definition (differing only in name), it treats this as a **plausible rename** and raises a hard error instead of silently generating a destructive drop-and-recreate.

To resolve the error, either:

1. Declare the rename in `[renames]` in `pgdesign.toml` -- this tells the migration system to emit `ALTER TABLE ... RENAME` (data-preserving) instead of `DROP` + `CREATE`
2. Make the definitions genuinely different (e.g., change a comment) if the drop-and-recreate is intentional

The rename gate runs after the diff and before operation lowering. It applies to both table-level and column-level renames, and detects ambiguous cases (one removed object matching multiple added objects) as a separate hard error.
