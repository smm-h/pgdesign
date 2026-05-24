# testdata/

Golden-file test cases for all packages.

## Structure

```
testdata/
  schemas/
    minimal.toml          -- 3 tables, basic features
    comprehensive.toml    -- 10 tables, all features (enums, partitions, generated cols, partial indexes, opclass)
    gamehome.toml         -- Full gamehome schema (30+ tables, the real consumer)
    multi-file/           -- Multi-file project (auth.toml + game.toml + pgdesign.toml)
  expected/
    minimal.sql           -- Expected DDL for minimal.toml
    comprehensive.sql     -- Expected DDL for comprehensive.toml
    gamehome.sql          -- Expected DDL for gamehome.toml
    minimal.d2            -- Expected D2 output
    minimal.json          -- Expected JSON output
  errors/
    missing-comment.toml  -- Triggers E202
    missing-pk.toml       -- Triggers E203
    fk-no-on-delete.toml  -- Triggers E201
    circular-fk.toml      -- Triggers E205
    varchar-usage.toml    -- Triggers E207
    bad-ref.toml          -- Triggers E204
  audit/
    2nf-violation.toml    -- Partial dependency detected
    3nf-violation.toml    -- Transitive dependency detected
    clean.toml            -- No violations
  format/
    unformatted.toml      -- Input to pgdesign fmt
    canonical.toml        -- Expected output after formatting
```

## Test patterns

- Golden-file tests: parse + generate, compare output to expected file byte-for-byte
- Error tests: parse + validate, compare diagnostics to expected codes
- Roundtrip tests: parse -> build -> generate -> parse again -> compare IR equality
- Format tests: fmt input -> compare to canonical output
