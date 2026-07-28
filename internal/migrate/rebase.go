package migrate

// Chain-mode fork resolution: `migrate rebase <head>` (roadmap 5.10, L3+L2).
//
// Two branches each appending an edge from a common point is normal (two heads).
// Detection alone is a dead end (apply hard-errors with a ForkError pointing
// here); rebase RESOLVES the fork. It re-parents the OTHER head's tail onto the
// kept head:
//
//   - The tail (the edges exclusive to the rebased-away head) is re-parented onto
//     the kept head. Each tail edge's ops RE-SIMULATE from the new parent's
//     manifest, producing a new to-manifest, a new content-derived revision, and a
//     new edge file (same ops/slug/class, new endpoints -> new content id).
//   - The rebased-away originals RETIRE to migrations/archive/ INTACT (moved,
//     never rewritten or deleted), so they stay loadable and the checker can
//     confirm they are reachable.
//   - The REBASE REVISION-REMAP TABLE gains one entry per rebased-away edge
//     (old target -> new target). The path-finder's canon() consults it, so a
//     database stamped at a rebased-away revision is SERVED FORWARD to the live
//     head via the remap, never orphaned.
//
// A rebase that orphaned stamped databases would merely relocate the dead end;
// the remap is what makes fork resolution complete. This is a PURE FILE operation
// (no DB): served-forward is proven at apply time through the remap.

import (
	"fmt"
	"sort"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
)

// RebaseResult reports a chain-mode rebase.
type RebaseResult struct {
	KeptHead       string            // the head the tail was re-parented onto
	RebasedHead    string            // the rebased-away head (old target of the last tail edge)
	NewHead        string            // the new head after re-parenting (new target of the last tail edge)
	ReparentedFrom []string          // old edge ids, in path order
	ReparentedTo   []string          // new edge ids, in path order
	ArchivedFiles  []string          // archived-original filenames, in path order
	Remap          map[string]string // rebased-away revision -> live re-parented revision
}

// RebaseChain resolves a two-head fork by re-parenting the tail of the head NOT
// named by keepRef onto the head named by keepRef. keepRef is a revision-or-edge
// reference (resolveSquashEndpoint semantics, at-target). It requires EXACTLY two
// live heads; anything else is a hard error.
func RebaseChain(p *ChainProject, keepRef string) (*RebaseResult, error) {
	live, err := p.LoadLiveEdges()
	if err != nil {
		return nil, err
	}
	if len(live) == 0 {
		return nil, fmt.Errorf("migrate rebase: chain has no live edges")
	}
	remap, err := p.LoadRemap()
	if err != nil {
		return nil, err
	}

	heads := findHeadRevs(live, remap)
	if len(heads) != 2 {
		return nil, fmt.Errorf("migrate rebase: expected exactly two live heads to resolve a fork, found %d %v — rebase resolves a two-head fork", len(heads), headStrings(heads))
	}

	keep, err := resolveSquashEndpoint(live, keepRef, true)
	if err != nil {
		return nil, fmt.Errorf("migrate rebase: --head %q: %w", keepRef, err)
	}
	keepStr := canon(keep.String(), remap)

	// Identify the kept head and the rebased-away head.
	var keptHead, rebaseHead rev.Revision
	switch {
	case canon(heads[0].String(), remap) == keepStr:
		keptHead, rebaseHead = heads[0], heads[1]
	case canon(heads[1].String(), remap) == keepStr:
		keptHead, rebaseHead = heads[1], heads[0]
	default:
		return nil, fmt.Errorf("migrate rebase: --head %q does not name either live head %v", keepRef, headStrings(heads))
	}

	tail, err := forkTailEdges(live, keptHead, rebaseHead, remap)
	if err != nil {
		return nil, err
	}
	if len(tail) == 0 {
		return nil, fmt.Errorf("migrate rebase: no tail edges to re-parent (the rebased head is already an ancestor of the kept head)")
	}

	class := tail[0].Class
	for _, e := range tail {
		if e.Class != class {
			return nil, fmt.Errorf("migrate rebase: tail mixes model classes (%s and %s)", class, e.Class)
		}
	}

	sim := opSimulator{store: p.store}
	result := &RebaseResult{
		KeptHead:    keptHead.String(),
		RebasedHead: rebaseHead.String(),
		Remap:       map[string]string{},
	}

	newParent := keptHead
	for _, e := range tail {
		from, err := p.ReadRevisionManifest(newParent)
		if err != nil {
			return nil, fmt.Errorf("migrate rebase: read new-parent manifest %s: %w", newParent, err)
		}
		ops := make([]chain.Op, len(e.Ops))
		for i, o := range e.Ops {
			ops[i] = o
		}
		to, err := sim.Simulate(from, ops)
		if err != nil {
			return nil, fmt.Errorf("migrate rebase: re-simulate edge %s onto %s: %w", e.ID()[:12], newParent, err)
		}
		newTarget, err := revisionOfManifest(p, to, class)
		if err != nil {
			return nil, fmt.Errorf("migrate rebase: compute re-parented revision for edge %s: %w", e.ID()[:12], err)
		}
		if err := p.WriteRevisionManifest(newTarget, class, to); err != nil {
			return nil, err
		}
		ne := Edge{Parent: newParent, Target: newTarget, Slug: e.Slug, Class: class, Ops: e.Ops}
		if _, err := p.WriteEdge(ne); err != nil {
			return nil, fmt.Errorf("migrate rebase: write re-parented edge: %w", err)
		}
		result.ReparentedFrom = append(result.ReparentedFrom, e.ID())
		result.ReparentedTo = append(result.ReparentedTo, ne.ID())
		result.Remap[e.Target.String()] = newTarget.String()
		newParent = newTarget
	}
	result.NewHead = newParent.String()

	// Retire the rebased-away originals INTACT to archive/ (a move, never a rewrite).
	for _, e := range tail {
		name, err := moveEdgeToArchive(p, e)
		if err != nil {
			return nil, fmt.Errorf("migrate rebase: archive original %s: %w", e.ID()[:12], err)
		}
		result.ArchivedFiles = append(result.ArchivedFiles, name)
	}

	// Write the remap so rebased-away databases are served forward.
	if err := p.WriteRemap(result.Remap); err != nil {
		return nil, err
	}

	if err := VerifyChainConsistency(p); err != nil {
		return nil, fmt.Errorf("migrate rebase: chain consistency check failed after re-parenting: %w", err)
	}
	return result, nil
}

// revisionOfManifest reconstructs the model a manifest describes and returns its
// content-derived revision. It is how a re-simulated (re-parented) manifest gets a
// stable, content-derived revision label without a source model.
func revisionOfManifest(p *ChainProject, m chain.Manifest, class rev.ModelClass) (rev.Revision, error) {
	s, err := reconstructFromManifest(p, m)
	if err != nil {
		return rev.Revision{}, err
	}
	return rev.Compute(s, class)
}

// findHeadRevs returns the live heads (targets that are no live edge's parent),
// comparing revisions in remap-canonical space (A5). Results are sorted.
func findHeadRevs(live []Edge, remap RemapTable) []rev.Revision {
	isParent := map[string]bool{}
	for _, e := range live {
		if !e.Parent.IsZero() {
			isParent[canon(e.Parent.String(), remap)] = true
		}
	}
	seen := map[string]bool{}
	var heads []rev.Revision
	for _, e := range live {
		cs := canon(e.Target.String(), remap)
		if !isParent[cs] && !seen[cs] {
			seen[cs] = true
			heads = append(heads, e.Target)
		}
	}
	sort.Slice(heads, func(i, j int) bool { return heads[i].String() < heads[j].String() })
	return heads
}

func headStrings(heads []rev.Revision) []string {
	out := make([]string, len(heads))
	for i, h := range heads {
		out[i] = h.String()
	}
	return out
}

// forkTailEdges returns the edges exclusive to rebaseHead's branch (the tail),
// in forward order from the fork point to rebaseHead. It walks backward from
// rebaseHead until it reaches an ancestor of keptHead (the fork point) or genesis.
// A non-unique backward step (two live edges share a target) is a hard error.
func forkTailEdges(live []Edge, keptHead, rebaseHead rev.Revision, remap RemapTable) ([]Edge, error) {
	keptAnc := ancestorsInclusive(live, keptHead, remap)
	byTarget := map[string][]Edge{}
	for _, e := range live {
		byTarget[canon(e.Target.String(), remap)] = append(byTarget[canon(e.Target.String(), remap)], e)
	}

	var rev0 []Edge
	cur := canon(rebaseHead.String(), remap)
	guard := len(live) + 1
	for cur != "" && !keptAnc[cur] && guard >= 0 {
		guard--
		edges := byTarget[cur]
		if len(edges) == 0 {
			break // reached a revision with no producing edge (a genesis target)
		}
		if len(edges) > 1 {
			return nil, fmt.Errorf("migrate rebase: revision %s has %d producing edges — the fork is not a simple tail", cur, len(edges))
		}
		rev0 = append(rev0, edges[0])
		cur = canon(revString(edges[0].Parent), remap)
	}
	// Reverse into forward order (fork point -> rebaseHead).
	tail := make([]Edge, len(rev0))
	for i, e := range rev0 {
		tail[len(rev0)-1-i] = e
	}
	return tail, nil
}

// ancestorsInclusive returns the set (canonical strings) of every revision on any
// backward path from r, including r itself.
func ancestorsInclusive(live []Edge, r rev.Revision, remap RemapTable) map[string]bool {
	byTarget := map[string][]Edge{}
	for _, e := range live {
		byTarget[canon(e.Target.String(), remap)] = append(byTarget[canon(e.Target.String(), remap)], e)
	}
	out := map[string]bool{}
	var walk func(s string)
	walk = func(s string) {
		if s == "" || out[s] {
			return
		}
		out[s] = true
		for _, e := range byTarget[s] {
			walk(canon(revString(e.Parent), remap))
		}
	}
	walk(canon(r.String(), remap))
	return out
}

// reconstructFromManifest rebuilds the model a manifest describes from the object
// store (the in-memory-manifest variant of ReconstructModel, used by rebase to
// label a re-simulated manifest without a written revision file).
func reconstructFromManifest(p *ChainProject, m chain.Manifest) (*model.Schema, error) {
	objs := make(map[enc.Key][]byte, len(m))
	for k, id := range m {
		b, err := p.store.Get(id)
		if err != nil {
			return nil, fmt.Errorf("migrate: reconstruct manifest: object %s does not resolve: %w", id, err)
		}
		objs[k] = b
	}
	return enc.DecodeObjects(objs)
}
