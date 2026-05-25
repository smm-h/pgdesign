# Golden-file test infrastructure

testdata/DESIGN.md describes 20 fixture files across 4 categories. The testdata/ directory is completely empty (only contains the DESIGN.md itself). Individual packages have their own testdata/ subdirectories with minimal fixtures.

## Described fixtures

### Schema fixtures (testdata/schemas/)
- minimal.toml: bare minimum valid schema
- comprehensive.toml: all features exercised
- gamehome.toml: real-world example
- multi-file/: multi-file schema (depends on pgdesign.toml config system)

### Expected output (testdata/expected/)
- minimal.sql, comprehensive.sql, gamehome.sql: DDL golden files
- minimal.d2: D2 diagram golden file
- minimal.json: JSON output golden file (JSON generate format not yet implemented)

### Error fixtures (testdata/errors/)
- missing-comment.toml (E202)
- missing-pk.toml (E203)
- fk-no-on-delete.toml (E201)
- circular-fk.toml (W008)
- varchar-usage.toml (E207)
- bad-ref.toml (E204)

### Audit fixtures (testdata/audit/)
- 2nf-violation.toml
- 3nf-violation.toml
- clean.toml

### Format fixtures (testdata/format/)
- unformatted.toml
- canonical.toml

## Notes

Some packages already have their own testdata (e.g., internal/parse/testdata/minimal.toml). This todo is about the project-level golden-file infrastructure for end-to-end testing.

## Effort

Medium. Creating the fixtures is straightforward; the test harness for golden-file comparison needs to be built.
