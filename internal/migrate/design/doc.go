// Package design is the phase-5 DESIGN GATE (roadmap subphase 5.0). It carries
// no production code: it holds the committed design documents, the reviewed DDL
// fixtures, the edge-artifact format spec + worked example, and the ONE
// MECHANICAL CHECK the gate is required to have — a Go test that parses the
// worked-example edge artifact, resolves its op payloads against a fixture
// object store, and re-derives its content hash through the real 1.1 encoder
// machinery (internal/enc, internal/rev, internal/objstore, internal/chain).
//
// Nothing here is imported by migrate, serve, or cmd. 5.1 and 5.2 IMPLEMENT the
// designs recorded here; they do not import this package. The package exists so
// that:
//
//   - the designs are committed, reviewable artifacts (not conversation), and
//   - the edge format is not merely asserted to "round-trip through the 1.1
//     encoder" but MECHANICALLY PROVEN to, so an epoch-level change to enc/rev
//     (which re-keys the world, L2) turns this test red exactly as intended.
//
// Documents (read in this order):
//
//   - README.md              — index, the serialization-format decision, and the
//     spec tensions surfaced against the real kernel APIs.
//   - tracking_schema.sql    — reviewed DDL for the three DB structures.
//   - tracking_schema.md     — rationale for the three structures, the view's
//     applied_at derivation, and the intent/confirm state machine.
//   - edge_format.md         — the edge-artifact format spec.
//   - store_layout.md        — migrations/{objects,revisions,chain,archive}/.
//   - path_finder.md         — the deterministic total path-finding rule.
//   - command_surface.md     — the migrate flag/subcommand disposition table.
//   - tracking_write_path.md — the single tracking write path 5.5 adopts.
package design
