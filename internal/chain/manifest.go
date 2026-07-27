package chain

import (
	"fmt"
	"sort"

	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/rev"
)

// Manifest is a whole-model revision manifest: a map of kind-qualified manifest
// keys (enc.Key) to object-ids (objstore content ids). It is the Merkle summary
// of a model — one level of a two-level Merkle DAG (store objects below,
// manifests above). Manifest comparison is key-wise symmetric difference (see
// Diff); the diff fast path compares per-object ids before deep comparison (see
// ChangedKeys). enc.Key is a comparable struct, so it is a valid map key.
type Manifest map[enc.Key]string

// BuildManifest computes the revision manifest of a resolved model: for every
// schema object, its kind-qualified key -> object-id (SHA-256 of the object's
// canonical bytes). It is a pure function of the CANONICALIZED model; the caller
// is responsible for having Built/Canonicalized s.
func BuildManifest(s *model.Schema) (Manifest, error) {
	objs, err := enc.EncodeObjects(s)
	if err != nil {
		return nil, err
	}
	m := make(Manifest, len(objs))
	for k, b := range objs {
		m[k] = objstore.ID(b)
	}
	return m, nil
}

// Putter is the write side of a content-addressed store (objstore.Store
// implements it). Kept minimal so the kernel depends on a capability, not a
// concrete store.
type Putter interface {
	Put(content []byte) (string, error)
}

// BuildManifestInto encodes every object of s, PUTS each into store, and returns
// the resulting manifest. It is the bridge that makes a manifest's ids resolve:
// after this call VerifyClosure(m, store) passes. The put ids equal the manifest
// ids by construction (both are SHA-256 of the same canonical bytes).
func BuildManifestInto(s *model.Schema, store Putter) (Manifest, error) {
	objs, err := enc.EncodeObjects(s)
	if err != nil {
		return nil, err
	}
	m := make(Manifest, len(objs))
	for k, b := range objs {
		id, err := store.Put(b)
		if err != nil {
			return nil, fmt.Errorf("chain: storing object %s: %w", k, err)
		}
		m[k] = id
	}
	return m, nil
}

// Equal reports whether two manifests map the same keys to the same object-ids.
func (m Manifest) Equal(other Manifest) bool {
	if len(m) != len(other) {
		return false
	}
	for k, id := range m {
		if oid, ok := other[k]; !ok || oid != id {
			return false
		}
	}
	return true
}

// ManifestDelta is the key-wise symmetric difference of two manifests, plus the
// keys present in both whose object-ids differ. It is a FLAT description of
// change at the object granularity — not a morphism (Deltas do not compose;
// composition happens on op-lists, L3). All key slices are sorted by
// Key.String() for determinism.
type ManifestDelta struct {
	// Added holds keys present in the target manifest but not the base.
	Added []enc.Key
	// Removed holds keys present in the base manifest but not the target.
	Removed []enc.Key
	// Changed holds keys present in both whose object-ids differ.
	Changed []enc.Key
}

// Empty reports whether the delta has no added, removed, or changed keys.
func (d ManifestDelta) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// Diff computes the manifest delta from base (m) to target (other): keys only in
// other are Added, keys only in m are Removed, keys in both with differing ids
// are Changed. This is the manifest-level comparison Part I calls "key-wise
// symmetric difference". Renames are delete+add at this level by construction
// (a renamed object has a different key), gated by roadmap 5.9's detection.
func (m Manifest) Diff(other Manifest) ManifestDelta {
	var d ManifestDelta
	for k, oid := range other {
		if id, ok := m[k]; !ok {
			d.Added = append(d.Added, k)
		} else if id != oid {
			d.Changed = append(d.Changed, k)
		}
	}
	for k := range m {
		if _, ok := other[k]; !ok {
			d.Removed = append(d.Removed, k)
		}
	}
	sortKeys(d.Added)
	sortKeys(d.Removed)
	sortKeys(d.Changed)
	return d
}

// ChangedKeys is the per-object-id DIFF FAST PATH primitive: the set of keys
// that differ between two manifests (added, removed, or content-changed),
// sorted. A deep differ can compare per-object ids first and DEEP-compare only
// these keys, skipping every object whose id is unchanged. When the result is
// empty the two models are byte-identical object-for-object, hence ≈_syn-equal,
// hence diff-empty (the forward conformance direction). Callers that already
// hold both manifests (roadmap 5.2's on-disk chain) use this directly; the pure
// differ consumes it via a whole-model equality short-circuit (see
// internal/diff).
func ChangedKeys(base, target Manifest) []enc.Key {
	d := base.Diff(target)
	out := make([]enc.Key, 0, len(d.Added)+len(d.Removed)+len(d.Changed))
	out = append(out, d.Added...)
	out = append(out, d.Removed...)
	out = append(out, d.Changed...)
	sortKeys(out)
	return out
}

func sortKeys(ks []enc.Key) {
	sort.Slice(ks, func(i, j int) bool { return ks[i].String() < ks[j].String() })
}

// RevisionOf returns the AUTHORITATIVE revision of a model under model class
// class. It delegates to rev.Compute (the whole-model form hash) — see the
// package doc's reconciliation note: the manifest and the revision are two
// summaries of the same per-object bytes, and there is exactly one Revision
// type and one revision value. This function exists so callers reach the single
// revision through the chain package without re-deriving it.
func RevisionOf(s *model.Schema, class rev.ModelClass) (rev.Revision, error) {
	return rev.Compute(s, class)
}

// Resolver is the read side of a content-addressed store used by closure
// verification (objstore.Store implements Has). Kept minimal so the checker
// depends on a capability, not a concrete store.
type Resolver interface {
	Has(id string) (bool, error)
}

// VerifyClosure is the Merkle CLOSURE VERIFICATION primitive: it asserts that
// every object-id referenced by manifest m resolves in the store (Part I's
// "shared consistency checker"). A dangling id — a manifest referencing content
// the store does not hold — is a hard error naming the offending key and id.
//
// This is HALF of the store<->chain consistency check. The other half is
// edge-endpoint consistency (an edge's ops, simulated, map its from-manifest to
// its to-manifest); that half needs op SIMULATION, which lands with roadmap 5.2
// and plugs in via VerifyEdgeEndpoint + OpSimulator. The interfaces are defined
// here so 5.2 wires the simulator in without reshaping the checker.
func VerifyClosure(m Manifest, store Resolver) error {
	// Deterministic order so the FIRST reported dangling key is stable.
	keys := make([]enc.Key, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortKeys(keys)
	for _, k := range keys {
		id := m[k]
		ok, err := store.Has(id)
		if err != nil {
			return fmt.Errorf("chain: closure check: resolving %s (%s): %w", k, id, err)
		}
		if !ok {
			return fmt.Errorf("chain: closure violation: manifest key %s references object %s not present in the store", k, id)
		}
	}
	return nil
}
