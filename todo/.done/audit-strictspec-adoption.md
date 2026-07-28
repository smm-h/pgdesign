# Audit the strictspec adoption (external session hand-off)

An external session added a strictspec 0.1.0 document gate in front of the
TOML parse layer (strictspec is now a direct Go dependency). Committed but
NOT released — rides along with the next release. This todo exists so the
work can be audited first.

## What changed and why

Why: parse.go duplicated document-shape validation (unknown keys, types,
required fields) that strictspec generates from a schema — with the three
custom scalars (identifier, pgtype, sql-expression) this repo motivated.

- ARCHITECTURE: gate, not swap. strictspec validates the whole document
  BEFORE the native walk; RawSchema construction stays native (the walk is
  inseparable from comment-preserving node capture). One authority per
  property: strictspec owns shape + scalar lexemes; native model/validate
  keeps semantics and cross-field rules.
- `internal/parse/pgschema/pgdesign.schema.toml` (713 lines, the full
  surface incl. recursive partitioning and node-kind unions) + manifest
  registering the three custom scalars (lexeme rules calibrated to the real
  corpus — e.g. `fk.ref_table` stays string for schema-qualified names,
  `index.columns` stays string for sort suffixes) + generated validator
  (committed 444) + `internal/parse/gate.go` mapping strictspec diagnostics
  into pkg/diagnostic (Severity=Error, Table/Column derived from paths).
- parse.go: 2136 → 1741 lines (−395): 14 knownKeys maps, unknown-key guards,
  ~117 type-mismatch branches and gate-owned required checks deleted.
- BREAKING: schema documents require `format_version = 1`
  (`scripts/stamp_format_version.sh`; all 28 in-repo schema TOMLs stamped);
  unknown keys are now hard errors (previously W001 warnings); a shape-invalid
  document yields diagnostics with no partial schema.
- Bug surfaced by the gate: 3 test fixtures used a compact
  `[tables.X.columns]` form the old parser SILENTLY IGNORED (zero columns
  parsed); now rejected — fixtures converted to proper column subtables.
- go-toml-edit bumped transitively v0.2.2 → v0.3.0. Suite: 43 packages
  green; go vet clean.

## Audit points

1. Verify the corpus-calibrated scalar relaxations match intent (identifier
   vs string decisions listed above) — they widen what the paper design
   proposed, on corpus evidence.
2. The gate.go path→Table/Column derivation is heuristic (parsed from the
   rendered path) — spot-check it against a few real diagnostics.
3. The unknown-key W001→hard-error change is a real behavior break for any
   external schema authors — confirm the changelog framing suffices.
