# Phase-5 design gate (5.0)

Complete designs for the database functor (migrate), produced BEFORE 5.1/5.2
implement them (planning discipline). This is a human design gate with ONE
mechanical check.

## Deliverables

| # | Deliverable | Files |
|---|---|---|
| 1 | The three DB structures (DDL + rationale) | `tracking_schema.sql`, `tracking_schema.md` |
| 2 | The edge artifact format + worked example + mechanical check | `edge_format.md`, `testdata/edge-2217c601ab9d-create-users.json`, `edge_roundtrip_test.go` |
| 3 | Store-root layout | `store_layout.md`, `testdata/revision-shop.json`, `revision_roundtrip_test.go` |
| 4 | The path-finder (deterministic total rule) | `path_finder.md` |
| 5 | Command-surface disposition table | `command_surface.md` |
| 6 | Tracking-write-path reconciliation | `tracking_write_path.md` |

## The mechanical check

`edge_roundtrip_test.go` and `revision_roundtrip_test.go` parse the committed
worked-example fixtures, resolve their content ids against a fixture object
store, and re-derive their hashes through the REAL 1.1 encoder machinery
(`internal/enc`, `internal/rev`, `internal/objstore`, `internal/chain`) — no
hardcoded hashes. The two fixtures are cross-consistent (the `users` table object
id and the `shop` revision are shared between them). An epoch-level change to the
encoder re-keys the world (L2) and turns these tests red — the intended tripwire.

## Serialization-format decision: JSON

The edge artifact and the revision manifest are serialized as **JSON, using the
same canonical byte discipline as `enc`/`rev`** (compact, `SetEscapeHTML(false)`,
sorted keys / struct order). Weighed against 5.1's self-contained-op needs and
the enc/objstore layer:

- **One canonical serializer everywhere (L1):** op payloads already live in the
  object store as canonical JSON; edge/revision files reference them by content
  id — one discipline top to bottom.
- **Content-stability / git-merge (boundary item 6):** one-file-per-edge with
  content-derived names avoids conflicts ONLY if identical edges serialize
  byte-identically; canonical JSON is deterministic, TOML-with-comments is not.
- **Identity is format-independent:** `chain.Edge.ID()` hashes a projection of the
  reconstructed edge, not the file bytes, so JSON is chosen for discipline/merge,
  not forced by identity.

TOML (today's migration-file format) is rejected: the roadmap moves off semver
TOML files, and edge/revision files are machine-authored content-addressed
artifacts where determinism dominates authoring ergonomics. Full rationale in
`edge_format.md`.

## Spec tensions surfaced against the real kernel (the point of a design gate)

These are flagged prominently; none is silently resolved.

1. **Edge identity omits the down-op.** `chain.Edge.ID()` projects only the UP
   op's `{kind,target,invertibility,payload_id}`. For identity to stay total, the
   down MUST be a pure function of the up: mechanically-invertible downs are
   derived; declared/DML inverses must be carried INSIDE the up-op payload
   (covered by `payload_id`), never as an independent edge-file field. 5.1 owns
   this constraint. (`edge_format.md` TENSION 1.)

2. **`chain.Op.Target()` is a mandatory `enc.Key`, but DML/RawSQL ops have no
   object target.** 5.1 must mint pseudo-target keys (a `dml`/`raw` `enc.Kind`
   with a deterministic label) that never appear in a manifest. (`edge_format.md`
   TENSION 2.)

3. **The path-finder tie-break is incomplete as stated.** "Consolidation-edge
   preference" is not a total order; two equal-length equal-consolidation paths
   remain tied. The design adds a final lexicographic edge-id tie-break to make
   the rule provably total, and makes multiple-reachable-heads a hard error (fork
   → `migrate rebase`) rather than an arbitrary pick. (`path_finder.md` TENSIONS
   1–2.)

4. **The three-structure naming under-specifies where `version`/`description`/
   `checksum` live.** The roadmap enumerates `pgdesign_migration_ops` as
   op-identity columns only, yet the view needs edge-level attributes and names no
   fourth table. The design denormalizes the three edge-level columns onto op rows
   (functionally dependent on `edge_id`) to honor the three-structure [%%] naming;
   a fourth `pgdesign_applied_edges` table is the rejected relational alternative.
   (`tracking_schema.md` TENSION 1.)

5. **`applied_at`/`checksum` for prefix rows survive the view only via fold-time
   verbatim insertion.** A view cannot both derive `applied_at` from a journal AND
   surface pre-upgrade `applied_at` verbatim unless the underlying rows carry it.
   The upgrade fold inserts synthetic confirmed ops with `confirmed_at :=
   old.applied_at` and `checksum := old.checksum`, so the uniform view derivation
   `max(confirmed_at)` reproduces them verbatim — no view special-case. This is
   what makes the upgrade's ASSERT-view-reproduces-snapshot step pass on its own
   columns. (`tracking_schema.md`, applied_at section.)

6. **`chain.Manifest` is class-blind.** manifest-equal implies revision-equal only
   same-class, so both the edge file and the revision file carry the model class,
   and the consistency checker / reconstruction stay class-aware. Pinned by
   `revision_roundtrip_test.go` (cross-class `Equal` must not error).

7. **`version`'s post-content-identity meaning is a free choice.** The view's
   `version` column is `edge_id` for post-upgrade edges (survives endomorphisms)
   and the preserved semver for prefix rows — a [%%] decision, reversible.
   (`tracking_schema.md` TENSION 2.)

## Home

These designs live in `internal/migrate/design/` (colocated with the consumer,
`internal/migrate`, and able to import the kernel packages the mechanical check
exercises). Nothing here is imported by production code; 5.1/5.2 implement the
designs, they do not import this package.
