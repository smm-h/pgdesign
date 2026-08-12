---
title: pgdesign
description: "pgdesign is a PostgreSQL schema compiler: TOML schemas become SQL DDL, with normal-form auditing, content-addressed migrations, and code generation."
order: 0
---

# pgdesign

A PostgreSQL schema compiler. You declare your database in TOML; pgdesign compiles it to SQL DDL and enforces database design principles on the way.

Columns are NOT NULL unless you opt out, foreign keys need an explicit `on_delete`, every table needs a comment, and normal-form violations are reported with counterexamples rather than left for production to find.

## Start here

- [Quickstart](quickstart.md) -- install it, write a schema, generate DDL
- [Format reference](format-reference.md) -- the full TOML syntax
- [Semantic types](semantic-types.md) -- built-in types, enums, domains, composites, and state machines
- [Validation rules](validation-rules.md) -- every error, warning, and info code

## Guides

- [The migration chain](migration-chain.md) -- revisions, edges, the journal, and the integrity guarantees
- [Migration guide](migration-guide.md) -- generating, applying, rolling back, squashing, and rebasing
- [Cross-repository imports](imports.md) -- referencing another project's tables across a git pin
- [Code generation](codegen-guide.md) -- type-safe application code in six languages
- [Design intelligence](design-intelligence.md) -- cascade analysis, dead columns, natural keys, and row sizing
- [Workload analysis](workload-analysis.md) -- index recommendations from schema shape and live statistics
- [Diffing](diff-guide.md) -- against a database, another TOML, or a git ref
- [Architecture laws](architecture-laws.md) -- the invariants the kernel is built on

## Reference

- [CLI reference](cli-index.md) -- every command and flag
- [API reference](gen-index.md) -- every package
