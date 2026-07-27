package diff

import (
	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
)

// ChangedObjectKeys is the per-object-id DIFF FAST PATH (roadmap kernel 1.4,
// Part I). It builds both models' revision manifests (kind-qualified key ->
// object-id) and returns the keys whose canonical bytes differ: objects present
// on one side only, or present on both with a different content id. A caller can
// then DEEP-diff only these objects and skip every object whose id is unchanged,
// turning a whole-schema comparison into O(changed objects).
//
// When the result is empty the two models are byte-identical object-for-object,
// hence ≈_syn-equal, hence diff-empty (the forward conformance direction).
//
// WIRING CHOICE (reported): this is the "kernel utility diff CONSUMES" option,
// not a short-circuit injected into Diff. Diff remains the AUTHORITATIVE full
// comparison for two reasons: (1) building two manifests on every Diff call
// would add an encode pass to the common (unequal) path; (2) more importantly, a
// manifest-equality short-circuit inside Diff would MASK Diff's own
// object-by-object logic from the forward-conformance test (which compares a
// model against its round-trip) — the test would then exercise the short-circuit
// rather than Diff. Keeping the fast path as a consumed utility preserves both
// Diff's semantics and the test's teeth. The on-disk chain (roadmap 5.2), where
// both manifests are already materialized, is the natural caller that prunes its
// O(objects) work through this function.
func ChangedObjectKeys(desired, actual *model.Schema) ([]enc.Key, error) {
	dm, err := chain.BuildManifest(desired)
	if err != nil {
		return nil, err
	}
	am, err := chain.BuildManifest(actual)
	if err != nil {
		return nil, err
	}
	return chain.ChangedKeys(dm, am), nil
}
