# --split-by-file flag accepted but has no effect on output

## Problem

`pgdesign codegen schema/ --lang python --mode ddl --split-by-file` produces the same `schema_ddl.py` + `schema_executor.py` as without the flag. The changelog for v0.19.0 says it should produce per-concern files: `extensions.py`, `types.py`, `tables_<source>.py` (one per input TOML), and `post_tables.py`.

## Reproduction

```
mkdir -p /tmp/split_test
pgdesign codegen schema/ --lang python --mode ddl --split-by-file --output /tmp/split_test
ls /tmp/split_test/
```

Expected: `extensions.py`, `types.py`, `tables_trace.py`, `tables_dispatch.py`, `post_tables.py`
Actual: `schema_ddl.py`, `schema_executor.py` (same as without `--split-by-file`)

Also tested without `--output` (stdout) — same merged output, no split markers.

## Context

orxtra has two schema owners (`trace.toml` with 16 tables, `dispatch.toml` with 4 tables) and needs separate generated files so each module can own its own DDL. The merged output works but requires both modules to import from a shared generated file, breaking the clean module ownership boundary.
