# Faceted codegen: generate __init__.py

## Problem

When `pgdesign codegen --lang python --mode ddl --split-mode faceted` generates a package directory, the output includes `schema_executor.py` which uses relative imports (`from .extensions import STATEMENTS as _ext_stmts`). These relative imports require the directory to be a Python package (i.e., have `__init__.py`).

However, pgdesign does not generate `__init__.py`. The consumer must create it manually. This creates a conflict with `pgdesign codegen --check`, which flags any non-generated file in the output directory as an orphan and exits non-zero.

## Impact

A consumer cannot use both faceted codegen AND --check in CI without a wrapper script that tolerates the __init__.py orphan. The workaround is fragile and adds complexity.

## Proposed fix

Generate `__init__.py` as part of the faceted output. It can be minimal (just a comment header). This makes --check pass cleanly when the directory is a Python package.

## Affected consumer

A monorepo project generates faceted output into `schema/_generated/` and uses `--check` in CI. A wrapper script currently parses the --check output to distinguish the expected __init__.py orphan from real orphans.

## Additional type annotation issues

The generated `schema_executor.py` has minor mypy --strict failures:
- `fetch()` returns `list[dict]` instead of `list[dict[str, Any]]`
- `__aexit__` parameters lack type annotations
- `async with conn.transaction() as tx:` fails mypy because `transaction()` is typed to return `AsyncTransaction` but the `async with` expects a coroutine returning a context manager (the method should return `AsyncTransaction` directly, not a coroutine)

These force consumers to add mypy overrides for the generated path.
