// Package enc is pgdesign's canonical per-object encoder: it maps each resolved
// model object to canonical JSON bytes, and decodes those bytes back to the
// object. It is the code form of law L1 (one canonical form): two models that
// are ≈_syn (equal under the structural sublanguage defined by the
// order-semantics table) encode to identical bytes, so id = hash(enc(x))
// is a content identity.
//
// Scope (roadmap kernel 1.1): this package delivers the PER-OBJECT encoder and
// the manifest-key machinery groundwork. The whole-model form, the envelope,
// and the single serializer are roadmap 1.5 and deliberately NOT built here.
//
// Design:
//
//   - Every top-level encoded form carries a CODEC VERSION (epoch) field and a
//     self-describing "kind" field. Ids are epoch-relative (L2): a change to
//     enc or N re-keys the world, so the codec version travels with the bytes.
//   - Encoding goes through DEDICATED form structs (the *Form types), never the
//     model structs directly. This makes the canonical byte order a property of
//     the encoder, independent of the model struct field order: a field-order
//     refactor of a model struct cannot shift identity.
//   - Per-field presence semantics distinguish unset from zero. Model fields
//     that are already pointers (defaults, statistics, cost/rows, sequence
//     bounds) stay pointers in the form; nil is omitted, a non-nil pointer to a
//     zero value is preserved.
//   - Map-typed fields (index opclasses/collations/with, schema groups,
//     transition Requires, state-machine transition maps) are emitted as JSON
//     objects. encoding/json sorts object keys, which is the deliberate,
//     stable key-ordering mechanism; set-valued leaf slices are sorted by the
//     encoder before emission.
//   - The exclusion allowlist (see policy.go) records, for every DDL-reaching
//     model struct and every registry-snapshot struct, which exported fields
//     are encoded and which are excluded WITH A REASON. The reflection-based
//     totality guard (policy_test.go) turns red the moment a new field is added
//     without being classified.
//
// The order-semantics table — exhaustive over the Model, classifying each
// collection's collection-order and intra-object order as SEMANTIC or
// CANONICAL-ONLY — is committed alongside this package in ORDER_SEMANTICS.md.
// That table IS the definition of ≈_syn on the structural sublanguage.
//
// enc is pure kernel: it imports only model, semtype, typeinfo, fd, and the
// standard library. It never imports migrate, introspect, serve, or cmd.
package enc

// CodecVersion is the codec epoch stamped into every encoded form. Ids are
// epoch-relative: a deliberate change to enc's field policy, ordering, or the
// normalizer it will eventually consume (roadmap 1.2) bumps this constant and
// re-keys the store. Such bumps are rare, deliberate breaking-major events
// (L2).
const CodecVersion = 1
