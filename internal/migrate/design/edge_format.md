# The edge artifact format (5.0)

ONE file per edge in `migrations/chain/`. There is no separate "migration file"
vs "chain-edge file" — one concept, one artifact. Worked example:
`testdata/edge-2217c601ab9d-create-users.json` (a genesis edge creating table
`users`). The mechanical check `edge_roundtrip_test.go` proves this format
round-trips through the real 1.1 encoder machinery.

## Serialization format: JSON (decided)

**Decision: JSON, using the same canonical byte discipline as `enc`/`rev`**
(compact, `SetEscapeHTML(false)`, trailing newline stripped, struct-field /
sorted-key order). Rationale, weighed against 5.1's self-contained-op needs and
the enc/objstore layer:

- **One canonical serializer everywhere (L1).** The roadmap's law bullet lists
  "op bodies" and "revision manifests" among the artifacts that share the single
  canonical serializer. Op payloads already live in the object store as canonical
  JSON (`enc` output). An edge file references those payloads by content id; JSON
  keeps ONE serialization discipline from the store up through the edge file.
- **Content-stability / git-merge (boundary item 6).** One-file-per-edge with
  content-derived names makes textual and allocation conflicts impossible ONLY if
  identical edges serialize to byte-identical files. Canonical JSON (structs +
  slices, no unordered maps in the edge body) is deterministic. TOML with
  comment-preservation (go-toml-edit) is LESS deterministic and would reintroduce
  a second parser for load-bearing committed data whose formatting quirks
  threaten byte-stability.
- **Self-contained ops (5.1).** Ops reference payloads by content id — plain
  strings JSON handles natively, matching the store's own JSON payloads. No
  structured payload is inlined in the edge file (no lossy mirrors, L1+L2).
- **Identity is format-independent anyway.** `chain.Edge.ID()` hashes a canonical
  projection of the reconstructed `Edge`, NOT the file bytes, so the on-disk
  format does not feed identity. JSON is chosen for the discipline/merge reasons
  above, not because identity requires it.

TOML (today's migration-file format) is rejected: the roadmap moves OFF semver
TOML migration files, and edge files are machine-authored, content-addressed,
git-reviewed artifacts for which determinism dominates human-authoring
ergonomics.

## Filename

`edge-<edge-content-hash-prefix>-<slug>.json`, e.g.
`edge-2217c601ab9d-create-users.json`. The prefix is the first 12 hex chars of
`chain.Edge.ID()`; the slug is the human display name. Content-derived, so
parallel edges, endomorphisms (R→R), and concurrent-branch allocation can never
collide on a name or race a counter. A display SEQUENCE for listings is derived
from graph topology at listing time, never stored as identity.

## Body schema

```json
{
  "format_version": 1,
  "codec": 1,
  "class": "registry_present",
  "parent": "",
  "target": "registry_present:29a1...92be",
  "slug": "create-users",
  "ops": [
    {
      "kind": "create_table",
      "target": {"kind": "table", "name": "users"},
      "invertibility": "mechanically-invertible",
      "payload_id": "db89...fdb3",
      "down": {
        "kind": "drop_table",
        "target": {"kind": "table", "name": "users"},
        "invertibility": "mechanically-invertible",
        "payload_id": "db89...fdb3"
      }
    }
  ]
}
```

- `format_version` — `rev.FormatVersion` (envelope/framing generation).
- `codec` — `enc.CodecVersion` (epoch; the consistency checker flags a chain
  carrying differing epochs as corruption).
- `class` — the model class of the endpoints (`registry_present` |
  `registry_absent`). Load-bearing: `chain.Manifest` is class-blind (roadmap
  handoff note), so the edge file must carry the class for class-aware endpoint
  checks.
- `parent` — the from-revision string, or `""` for a genesis edge (null parent).
- `target` — the to-revision string (`<class>:<hex>`).
- `slug` — human display name; participates in identity (two otherwise-identical
  edges with different slugs are different edges).
- `consolidation` (roadmap 5.3, omitempty) — `true` marks a SQUASH CONSOLIDATION
  edge (a parallel edge whose op-list is the ordered concatenation of a superseded
  path). Absent/false on ordinary edges. The path-finder prefers consolidation
  edges in its 4a tie-break.
- `superseded` (roadmap 5.3, omitempty) — the list of chain-edge ids this
  consolidation supersedes (the archived originals whose op-lists it
  concatenates). Present iff `consolidation` is true (the reader enforces the
  biconditional). The A6 disjointness invariant (path_finder.md) is checked
  against these sets at CREATION time (`SquashChain`) and re-verified by the
  consistency checker. Like `down`, these two fields are NON-IDENTITY metadata:
  `chain.Edge.ID()` does NOT hash them, so a consolidation edge and a hypothetical
  plain edge with identical ops/endpoints/slug would share an id — a case the
  op-list concatenation (which mixes intermediate churn no net-diff edge produces)
  makes practically unreachable. The single-edge biconditional is verified at load;
  the cross-edge disjointness and the superseded-ids-resolve-to-archive property
  are the consistency checker's job.
- `ops[]` — the ordered op-list. Each entry carries exactly the facets
  `chain.Edge.ID()` observes plus the DOWN reference:
  - `kind` — op family.
  - `target` — the structured `enc.Key` (`kind`, `schema?`, `name`, `arg_sig?`),
    so the manifest key round-trips exactly (overload signatures included)
    without re-parsing `Key.String()`.
  - `invertibility` — the L4 class as a string.
  - `payload_id` — objstore content id of the op's structured payload (or of a
    content-addressed opaque blob for RawSQL/DML bodies). The full payload lives
    in `migrations/objects/`; the edge file only references it.
  - `down` — the down-op reference (same shape, recursively). Present for
    mechanically-invertible and declared-inverse ops; omitted for non-invertible.
    It is a DERIVED CACHE of the up-op payload, never independently trusted — see
    TENSION 1 for the derivation rule and the LOAD-time verifier that catches a
    corrupted/tampered `down`.

## How identity is derived (the mechanical check)

`chain.Edge.ID()` hashes `edgeContent{parent, target, slug, ops[]}` where each op
projects to `{kind, target.String(), invertibility:int, payload_id}`. The edge
file carries exactly these, so reconstructing an `Edge` from the file and calling
`ID()` re-derives the filename's hash prefix. `edge_roundtrip_test.go`:

1. builds the fixture model, stores its objects, derives `target` (rev.Compute)
   and `payload_id` (the manifest entry) from the kernel — no hardcoded hashes;
2. constructs the edge, serializes it, and (first run) seeds the committed
   fixture;
3. parses the committed fixture, RESOLVES each `payload_id` against the store,
   reconstructs the `Edge`, and asserts `Edge.ID()` equals the derivation and the
   filename prefix.

An epoch-level change to `enc`/`rev` re-keys the world and turns this test red —
the intended tripwire.

## TENSIONS surfaced against the kernel (design gate)

1. **The down-op is NOT in `chain.Edge.ID()`'s projection.** `edgeContent`
   projects only `{kind, target, invertibility, payload_id}` of each UP op; the
   `down` reference is absent from identity. Two edges with identical up-ops but
   different recorded downs would therefore have the SAME edge id — a collision if
   downs could vary independently. **Resolution / design constraint:** the down
   must be a pure function of the up-op's content. For mechanically-invertible
   ops the down is DERIVED (drop⁻¹ = create, etc.), so it is a function of the up
   by construction. For declared-inverse ops (incl. vacuous DML inverses) the
   recorded inverse must be carried INSIDE the up-op's structured payload (covered
   by `payload_id`), NOT as an independent edge-file field that escapes identity.
   The edge file's `down` field is thus a DERIVED CACHE of the up-op payload,
   NEVER independently trusted. The derivation rule, by L4 class:
   - **mechanically-invertible:** the down is DERIVED at load from the up-op
     (drop⁻¹ = create, etc.) — the stored `down` is a pre-resolved copy of that
     derivation.
   - **declared-inverse / DML:** the down is RE-DERIVED at load from the inverse
     embedded INSIDE the up-op payload (covered by `payload_id`), never from an
     independent edge-file field.
   In both cases the VERIFIER is edge-file LOAD (5.2's reader): it re-derives the
   down from the up-op and compares it against the stored `down`. A mismatch
   between the stored `down` and the re-derivation is a HARD ERROR — corruption
   (or tamper) is caught at READ time, before any apply, and never fed to
   rollback. Because the down is always re-derivable from the up, 5.1 MUST make
   the up-op payload determine the down so identity stays total; the stored
   `down` buys apply/path-finding a pre-resolved reference without ever becoming
   a second source of truth. Flagged prominently: this is a real constraint the
   kernel's identity projection imposes on 5.1's op design. (The journal's
   `down_op` column is the separate, apply-time home of the resolved down for
   5.6's file-free rollback; the edge-file `down` and the journal `down_op` are
   two views of the same payload-determined inverse.)

2. **`chain.Op.Target()` is a mandatory `enc.Key`, but DML/RawSQL ops have no
   object target.** `Edge.ID()` hashes `op.Target().String()`, so every op —
   including arbitrary-SQL DML/RawSQL — must yield a stable `enc.Key`.
   `enc.Kind` has no `dml`/`raw` constant. **Resolution:** 5.1 mints PSEUDO-TARGET
   keys for data ops using a dedicated `enc.Kind` (`"dml"` / `"raw"`). The exact
   grammar is PINNED HERE — byte-stable and identity-load-bearing, so it cannot be
   left to 5.1:

   > `Key{Kind:"dml", Name:"<edge-seq>"}` → `dml:<edge-seq>`
   > `Key{Kind:"raw", Name:"<edge-seq>"}` → `raw:<edge-seq>`

   where `<edge-seq>` is the ZERO-BASED op sequence within the edge (the op's
   position in the edge's op-list — the same `seq` the journal keys on). This
   label is deterministic, seq-scoped, and collision-free WITHIN an edge (each op
   has a distinct seq); it is MEANINGLESS ACROSS edges (op 0 of edge A and op 0 of
   edge B both render `dml:0`, and that is correct — pseudo-targets carry no
   cross-edge meaning). That is exactly what identity needs: `Edge.ID()` hashes
   `op.Target().String()` per-op within one edge, so a per-edge-unique,
   cross-edge-meaningless label keeps identical DML edges byte-identical without
   ever pretending a data op names a schema object.

   Explicitly: the 5.2 `OpSimulator` treats `dml`/`raw`-kind ops as MANIFEST
   NO-OPS (data ops change rows, not the schema manifest). Pseudo-target keys
   NEVER resolve IN, and are NEVER required BY, any manifest or the consistency
   checker (`VerifyClosure` / `VerifyEdgeEndpoint`): they appear only in the
   edge's op projection and the journal `target` column, never in
   `migrations/revisions/` and never in a store lookup. Flagged: 5.1 owns adding
   the pseudo-kind; 5.0 PINS the grammar and the simulator's no-op treatment.
