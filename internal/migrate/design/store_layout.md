# Store-root layout (5.0)

Visible (non-dot) directory names for committed load-bearing data, under the
project's `migrations/` root:

```
migrations/
  objects/     content-addressed object store (objstore.Store root)
  revisions/   whole-model revision manifests (one file per revision)
  chain/       edge artifacts (one file per edge; edge_format.md)
  archive/     retired originals (superseded by squash; rebased-away by rebase)
```

## migrations/objects/

An `objstore.Store` root (`internal/objstore`). Content-addressed: path is a
git-style two-hex fanout `objects/ab/cdef…`; the on-disk envelope is
`magic "PGO1" | big-endian codec epoch | content`. Holds every encoded object
(`enc.EncodeObjects` output: tables, views, enums, domains, composites,
sequences, functions, SM types, the schema-meta header) AND every op payload
(structured op bodies; RawSQL/DML opaque blobs). Puts are idempotent; identity is
location-free; reads verify the epoch and re-hash the content. Manifests and
edges reference this store by content id.

## migrations/revisions/

One file per revision: a class-tagged, kind-qualified SORTED MAP of manifest key
→ object-id, plus the authoritative revision string and codec epoch. Worked
example: `testdata/revision-shop.json`; pinned by `revision_roundtrip_test.go`.

```json
{
  "revision": "registry_present:29a1...92be",
  "class": "registry_present",
  "codec": 1,
  "entries": {
    "schema:shop": "5aef...fcd2",
    "table:users": "db89...fdb3"
  }
}
```

- `entries` keys are `enc.Key.String()` (collision-free across kinds); JSON map
  encoding sorts keys, so the serialized form is deterministic.
- `class` is load-bearing: `chain.Manifest` is class-blind, and manifest-equal
  implies revision-equal ONLY same-class. The file carries the class so the
  reconstruction and the consistency checker stay class-aware.
- **Reconstruction path (5.9's "deserialize head manifest via objstore"):**
  resolve each id via `objstore.Get`, decode with `enc.DecodeObject`,
  `Canonicalize`, and `rev.Compute(model, class)` — the result MUST equal
  `revision`. `revision_roundtrip_test.go` mechanically checks this, and that the
  cross-class `Equal` does not error (class marker preserved).

The authoritative revision remains `rev.Compute` over the whole-model form; the
manifest file is the store-facing index over the same per-object bytes. Storing
the key→id map (not the whole-model bytes) is deliberate: 5.9 deserializes via
objstore, and the map + store reconstruct the identical bytes.

## migrations/chain/

One file per edge (`edge_format.md`). Location-addressed files (their bytes need
not hash to their names); append-onlyness is CHECKED POLICY via the consistency
checker (closure + edge-endpoint + epoch homogeneity), not structural
impossibility (L2).

## migrations/archive/

Retired originals move here intact, never rewritten or deleted:

- **Squash (5.3):** the superseded path's edges retire to `archive/`, reachable
  via their edges so a mid-range database applies the remaining originals through
  the path-finder.
- **Rebase (5.10):** rebased-away edges retire to `archive/`; the REBASE
  revision-remap table (fork-resolution only) is written so a database stamped at
  a rebased-away revision is SERVED, not orphaned. apply consults the remap before
  declaring a position unreachable.

Archive files use the identical edge-artifact format; they are just not on the
live head path. The path-finder is ARCHIVE-INCLUSIVE (`path_finder.md`).

## The rebase revision-remap table (on disk, not in the DB)

A REBASE-ONLY chain artifact (roadmap L2/L3/5.10) mapping rebased-away revisions
→ their live re-parented revisions. It lives with the chain on disk (a file under
`migrations/`, e.g. `migrations/remap.json`; exact name owned by 5.10). apply and
the consistency checker consult it; it is NOT a database structure and NOT part
of `pgdesign_chain_position`. Outside a rebase it is empty/absent.
