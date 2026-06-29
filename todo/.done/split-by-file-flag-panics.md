# --split-by-file BoolFlag panics when not passed

## Problem

`pgdesign codegen schema/ --lang python --mode ddl` panics with:

```
panic: interface conversion: interface {} is nil, not bool
goroutine 1 [running]:
main.handleCodegen(...)
    cmd/pgdesign/handlers_codegen.go:21
```

Line 21: `splitByFile := kwargs["split-by-file"].(bool)` — the type assertion fails because the flag value is nil when not passed.

This breaks ALL Python DDL codegen, not just `--split-by-file` mode, because the handler reads the flag unconditionally before any codegen logic.

## Cause

`cli.go:175` defines `BoolFlag("split-by-file", ...)` without a default value. strictcli's `BoolFlag` constructor doesn't set a default, so `kwargs["split-by-file"]` is nil when the flag isn't passed.

## Fix

Either:
- Add a default to the flag definition: `strictcli.BoolFlag("split-by-file", "...", strictcli.WithDefault(false))`
- Or use a nil-safe read: `splitByFile, _ := kwargs["split-by-file"].(bool)` (Go zero-value for bool is false)

## Severity

Regression — all Python DDL codegen is broken in v0.19.0+. The non-split mode that worked in v0.16.2 no longer works.
