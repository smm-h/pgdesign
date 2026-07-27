package migrate

// Model reconstruction from the object store (roadmap 5.2 / 5.9 deserialization seam).
//
// ReconstructModel rebuilds the whole-model IR for a revision from its on-disk
// revision manifest plus the content-addressed object store: read the manifest
// (key -> object-id), resolve every object-id to its canonical bytes, and decode
// the object set back into a *model.Schema (enc.DecodeObjects, which Canonicalizes).
// This is the inverse of BuildManifestInto and realizes decode∘enc = id on
// canonicalized models (L2). It is the shared seam 5.2's generate wiring uses to
// obtain the head model (so the schema-meta prev is computed correctly) and 5.9's
// pure generation reuses.
//
// ChainHead resolves the single live head of a chain project: its revision and
// its reconstructed model. A chain with no live edges is genesis (zero revision,
// nil model); more than one live head is an unresolved fork.

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
)

// ReconstructModel rebuilds the model for revision r from p's revision manifest
// and object store. A missing manifest, an unresolved object id, or a decode
// failure is a hard error (no silent partial model).
func ReconstructModel(p *ChainProject, r rev.Revision) (*model.Schema, error) {
	m, err := p.ReadRevisionManifest(r)
	if err != nil {
		return nil, err
	}
	objs := make(map[enc.Key][]byte, len(m))
	for k, id := range m {
		b, err := p.store.Get(id)
		if err != nil {
			return nil, fmt.Errorf("migrate: reconstruct %s: object %s does not resolve: %w", r, id, err)
		}
		objs[k] = b
	}
	s, err := enc.DecodeObjects(objs)
	if err != nil {
		return nil, fmt.Errorf("migrate: reconstruct %s: %w", r, err)
	}
	return s, nil
}

// ChainHead returns the single live head's revision and reconstructed model. When
// the chain has no live edges it returns the zero revision and a nil model
// (genesis). More than one live head is a *ForkError.
func ChainHead(p *ChainProject) (rev.Revision, *model.Schema, error) {
	live, err := p.LoadLiveEdges()
	if err != nil {
		return rev.Revision{}, nil, err
	}
	if len(live) == 0 {
		return rev.Revision{}, nil, nil // genesis
	}
	kEdges := make([]chain.Edge, len(live))
	for i, e := range live {
		kEdges[i] = e.chainEdge()
	}
	heads := chain.FindHeads(kEdges)
	switch len(heads) {
	case 1:
		prev, err := ReconstructModel(p, heads[0])
		if err != nil {
			return rev.Revision{}, nil, err
		}
		return heads[0], prev, nil
	default:
		strs := make([]string, len(heads))
		for i, h := range heads {
			strs[i] = h.String()
		}
		return rev.Revision{}, nil, &ForkError{Heads: strs}
	}
}
