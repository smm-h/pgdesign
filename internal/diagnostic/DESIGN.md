# internal/diagnostic

Shared error/warning type used by all passes (parse, model, validate, audit, migrate).

## Types

- `Severity` -- enum: Error, Warning, Info, Hint.
- `Diagnostic` -- struct: Severity, Code (string, e.g. "E001", "W003"), File (source TOML path), Table (name), Column (name, optional), Message (human-readable), Suggestion (optional fix recommendation).
- `Diagnostics` -- slice type with helpers: `HasErrors() bool`, `Errors() []Diagnostic`, `Warnings() []Diagnostic`.

## Codes

Stable identifiers for machine consumption. Prefixed by severity:
- E001-E099: Parse errors
- E100-E199: Build/resolution errors
- E200-E299: Validation rule violations
- W001-W099: Anti-pattern warnings
- W100-W199: Audit warnings (NF findings)

## Rendering

- `RenderTerminal(diags Diagnostics, color bool)` -- Human-readable output with ANSI colors. Groups by file, then by severity.
- `RenderJSON(diags Diagnostics)` -- JSON array of diagnostic objects. Stable schema for tooling/visualization/CI.

## Design notes

All packages return `[]Diagnostic` alongside their primary result. The CLI aggregates all diagnostics from all passes and renders once at the end. This allows maximum error reporting (never stops at first error unless the pass literally cannot continue).
