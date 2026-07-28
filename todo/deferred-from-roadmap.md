# Deferred items from the 0.25.x roadmap (active watch-list)

Extracted from the completed comprehensive roadmap (now in todo/.done/)
so the deferred items stay visible to triage. Each item carries its
activation trigger; none is actionable without it.

- **Rename-index follow-up** — after a declared table rename, auto-named
  indexes/constraints keep old names live; the differ emits cosmetic
  DROP+CREATE churn on the next diff (safe, not data loss). The clean fix
  (rename ops also emitting ALTER INDEX/CONSTRAINT RENAME) is
  CODEC-AFFECTING (any DDLOp field addition re-keys every delta op in the
  store). Trigger: bundle with the next deliberate epoch event or a
  pre-1.0 codec finalization pass.
- **Squash op-list optimizer** — consolidation is concatenation-only by
  design; cancellation/merging/folding as a terminating rewriting system
  (dependency-aware side conditions, critical pairs, Newman's lemma) is
  specified in the roadmap's out-of-scope entry. Trigger: a consolidation
  edge's size demonstrably hurting a consumer.
- **postgres:11 CI leg** — the pre-PG12 non-transactional enum-add class
  is covered by a path-selection unit test only. Trigger: a pipeline
  performance benchmark showing the EOL container's cost is acceptable.
- **Test schema mode** — needs its own design round.
- **N-project import topology** (beyond the two-project case) — needs its
  own design round.
- **Manifest + per-language linter ecosystem** — evidence-gated on a
  consumer actually wanting linter-level enforcement of generated-type
  usage.
- **Three-way model merge** (pushout over a common-ancestor revision) —
  recorded alternative to rebase-only fork resolution; the kernel makes it
  nearly free when wanted.
- **Live round-trip exactness residue** — the minimal forward-simulation
  rule set persists where round-trip cannot reach; grow only on evidenced
  false-drift reports.
- **Epoch recovery** — event-time procedure documented in the roadmap
  (never bump go-pgquery routinely; the CI pin guard forecloses accidents;
  the rekey tool is written at event time if history continuity matters).
- **Phase 10: the interactive frontend** on the serve API contract —
  deliberately unplanned; the DB-free /api/schema + /graph + /doc
  endpoints are its stable boundary.
- **selfdoc STALE001-on-release snag** — generated cli-index.md carries a
  version line, so every release trips STALE001 and needs a manual
  baseline accept; a structural fix belongs in selfdoc (todo filed there),
  but if selfdoc's fix changes behavior, pgdesign's release flow should
  drop its workaround.
