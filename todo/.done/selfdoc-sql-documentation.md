# Document compiled SQL output with selfdoc

## Context

pgdesign compiles TOML schema definitions to SQL DDL. selfdoc v0.16.0 ships a SQL extractor that can parse PostgreSQL DDL: CREATE TABLE/VIEW/TYPE/FUNCTION, column definitions with types/constraints/defaults, and COMMENT ON statements.

Adding SQL source entries to pgdesign's selfdoc.json would auto-generate documentation pages for the compiled schema — tables, columns, types, constraints, and any COMMENT ON documentation. Every project that uses pgdesign (e.g., gamehome) would get schema documentation for free if pgdesign's compiled output is documented.

## What this enables

- API reference pages for every table, view, and type in the compiled schema
- Column-level documentation (name, type, nullable, default, constraints) via the `table-schema` directive
- COMMENT ON text surfaced as descriptions in docs
- Coverage enforcement: selfdoc check ensures every CREATE statement is documented

## What to do

1. Add a SQL source entry to selfdoc.json pointing at the compiled output directory (wherever pgdesign writes .sql files)
2. Run `selfdoc gen` to generate doc pages
3. Run `selfdoc check` to verify coverage
4. Customize skeleton descriptions where needed
