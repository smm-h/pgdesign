# --split-mode nil check blocks all non-DDL Python codegen modes

## Problem

`pgdesign codegen --lang python --mode enums` (and `--mode types`, `--mode validators`, etc.) fails with:

```
error: --split-mode: invalid value '<nil>', must be one of: faceted, self-contained
```

But passing `--split-mode faceted` to a non-DDL mode then fails with:

```
error: --split-mode is only supported for Python DDL mode (--lang python --mode ddl)
```

Catch-22: the flag is required by the nil check but rejected by non-DDL modes.

## Cause

Same class of bug as the v0.19.0 panic and v0.19.2 fix. The `--split-mode` flag has no default value, so `kwargs["split_mode"]` is nil when not passed. The nil check fires before the mode-specific validation, blocking all Python codegen.

## Fix

Either:
- Give `--split-mode` a default (empty string or skip the check when nil)
- Only validate `--split-mode` when `--mode ddl` is selected

## Impact

`--mode enums` (new in v0.20.0) is completely unusable. `--mode types`, `--mode validators`, `--mode constants`, `--mode constraints` for Python are also all blocked.

Only `--mode ddl --split-mode faceted` and `--mode ddl --split-mode self-contained` work.
