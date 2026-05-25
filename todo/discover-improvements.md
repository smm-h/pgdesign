# FD discovery improvements

The core TANE algorithm is fully implemented, but several DESIGN.md features around its CLI integration are missing.

## Missing CLI flags

- `--tables <list>`: limit discovery to specific tables (no filtering logic exists)
- `--approximate`: expose ApproximateThreshold to users (the mechanism exists in Options and fdHolds, but threshold is always 0.0)

## Missing parallelism

DESIGN.md specifies TANE runs per-table in bounded goroutines (default GOMAXPROCS) with a progress callback. The code is entirely synchronous — single-table, single-goroutine. The caller in main.go also processes tables sequentially.

## Inferred FD marking

When FDs are discovered from live data, the resulting audit diagnostics (W101, W102) are indistinguishable from those produced by declared FDs. An Info diagnostic says "Discovered N FD(s) from data sample" but individual NF violations don't indicate their source.

## Effort

Small-medium. The parallelism is the main work item; the CLI flags are trivial.
