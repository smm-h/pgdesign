// Package chain is the pure kernel of pgdesign's migration algebra: revision
// manifests, the parent-linked edge graph (the free category on edges), and
// three-way typed invertibility (roadmap kernel 1.4, laws L3 + L4 + L7).
//
// It is deliberately PURE. It imports only the other kernel packages
// (internal/enc, internal/rev, internal/objstore, internal/model) plus the
// standard library, and pgregory.net/rapid in TEST files only. It never imports
// migrate, introspect, serve, or cmd — the abstract Op interface here is what
// keeps the dependency direction kernel <- adapter: roadmap 5.1's concrete op
// families implement Op, so the kernel reasons about migrations without knowing
// their concrete shapes.
//
// Scope boundary (what 1.4 is NOT): there are no concrete op families, no
// on-disk chain files, no tracking schemas, and no database — those are
// phase 5. 1.4 is the types and the laws and their property tests.
//
// Revision / manifest reconciliation (ONE concept, not two): Part I of the
// roadmap frames the revision two ways that must be reconciled:
//
//   - "revision = hash of canonical bytes" (kernel 1.5, internal/rev): rev
//     concatenates every per-object canonical form, in sorted manifest-key
//     order, behind a versioned+class-tagged preamble, and takes the SHA-256 of
//     that whole-model byte string. This is the AUTHORITATIVE revision, the
//     opaque class-tagged rev.Revision printed by validate/build.
//   - "revision = id of a whole-model manifest — a sorted map of kind-qualified
//     keys -> object-id" (Part I / this package): a Manifest.
//
// These are the SAME content viewed two ways, and there is exactly ONE Revision
// type (rev.Revision) and ONE revision value per (model, class). The
// reconciliation is precise:
//
//   - Both derive from the identical per-object encoding, enc.EncodeObjects(s).
//   - A Manifest maps each kind-qualified enc.Key to object-id = SHA-256 of that
//     object's canonical bytes (objstore.ID) — exactly the bytes rev embeds.
//   - So the Manifest is the MERKLE SUMMARY of the whole-model form: the
//     whole-model form embeds the object bytes; the manifest embeds their
//     hashes. Under SHA-256 collision resistance (L2, boundary item 14) they
//     carry the same information, so revision-equal <=> manifest-equal.
//
// This package therefore does NOT mint a second hash and call it "the
// revision". RevisionOf delegates to rev.Compute; the Manifest is the
// store-facing, Merkle-facing, diff-fast-path index over the same objects. The
// authoritative identity is rev.Compute (whole-model form); the roadmap's
// "revision = id of the manifest" phrasing is honored as "the manifest and the
// revision are two faithful summaries of one per-object byte set", and
// ConsistentRevisionAndManifest / the property tests pin that they cannot
// disagree.
//
// Why the whole-model FORM (bytes) is authoritative rather than a hash of the
// key->id map: the whole-model form is SELF-CONTAINED — a revision can be
// verified from its own bytes with no store present (rev.Parse does exactly
// this). A hash of the key->id map would require the store to resolve ids
// before the content could be recovered, coupling identity verification to
// store availability. 1.5 shipped the self-contained form; 1.4 adopts it as the
// single revision and layers the manifest on top as the index.
package chain
