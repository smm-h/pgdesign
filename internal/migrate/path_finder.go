package migrate

// The path-finder (roadmap 5.2, path_finder.md).
//
// A DETERMINISTIC TOTAL rule replacing today's flat semver sort: given a
// database's chain position, it searches the edge graph (live chain + archive)
// for the path the database must apply to reach the single live head.
//
// Two domains, one rule (amendment A5):
//   - HEAD-FINDING runs over LIVE edges only, so an archived edge's superseded /
//     rebased-away target never surfaces as a spurious head.
//   - TRAVERSAL is ARCHIVE-INCLUSIVE, so a mid-consolidation or rebased-away
//     database can walk archived originals to the live head.
//
// Both phases canonicalize every revision through the rebase remap (5.10 owns
// the remap contents; the seam is here, empty now). Comparison is by the
// canonical String() form.
//
// The rule is TOTAL (path_finder.md TENSION 1): among shortest-edge-count paths,
// prefer more consolidation edges (4a), then the lexicographically-least sorted
// edge-id sequence (4b).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/rev"
)

// RemapTable maps a rebased-away revision's String() form to its live
// re-parented revision's String() form (roadmap 5.10, store_layout.md). It is a
// REBASE-ONLY on-disk artifact; outside a rebase it is empty. The path-finder
// consults it so a database stamped at a rebased-away position is served forward,
// never orphaned.
type RemapTable map[string]string

// canon follows remap to a fixpoint, returning s's live-canonical revision
// string. An empty remap is the identity.
func canon(s string, remap RemapTable) string {
	for i := 0; i <= len(remap); i++ {
		next, ok := remap[s]
		if !ok {
			break
		}
		s = next
	}
	return s
}

// NoPathError reports that pos is not reachable to any live head (corrupt /
// off-chain position).
type NoPathError struct{ Pos string }

func (e *NoPathError) Error() string {
	return fmt.Sprintf("migrate: no path from chain position %s to a live head (corrupt or off-chain position)", e.Pos)
}

// ForkError reports that more than one live head is reachable (an unresolved
// fork). It points at `migrate rebase`.
type ForkError struct{ Heads []string }

func (e *ForkError) Error() string {
	return fmt.Sprintf("migrate: chain has %d reachable heads %v — an unresolved fork; run `migrate rebase`", len(e.Heads), e.Heads)
}

// canonEdge is an edge projected into remap-canonical string space for graph
// reasoning. id is the edge's content identity; consol marks a consolidation
// edge (5.3; false for every edge until 5.3 lands).
type canonEdge struct {
	parent string // "" for a genesis edge
	target string
	id     string
	consol bool
	idx    int // index into the original slice (for returning the chosen Edges)
}

func toCanonEdges(edges []Edge, remap RemapTable) []canonEdge {
	out := make([]canonEdge, len(edges))
	for i, e := range edges {
		out[i] = canonEdge{
			parent: canon(revString(e.Parent), remap),
			target: canon(e.Target.String(), remap),
			id:     e.ID(),
			consol: e.Consolidation,
			idx:    i,
		}
	}
	return out
}

// FindPath returns the ordered edges a database at chain position pos must apply
// to reach the single live head. pos is a revision String() (or "" if the
// database has never been stamped — genesis). liveEdges is migrations/chain/;
// allEdges is chain + archive (archive-inclusive traversal). remap is the rebase
// remap (empty until 5.10).
//
// It returns:
//   - an empty slice and nil when the database is already at the head (up to date);
//   - a *NoPathError when pos is off-chain;
//   - a *ForkError when more than one head is reachable.
func FindPath(pos string, remap RemapTable, liveEdges, allEdges []Edge) ([]Edge, error) {
	start := canon(pos, remap)

	heads, err := findLiveHeads(liveEdges, remap)
	if err != nil {
		return nil, err
	}
	all := toCanonEdges(allEdges, remap)

	// Reachable heads from start (archive-inclusive traversal).
	var reachableHeads []string
	for _, h := range heads {
		if start == h || reachable(start, h, all) {
			reachableHeads = append(reachableHeads, h)
		}
	}
	sort.Strings(reachableHeads)
	switch len(reachableHeads) {
	case 0:
		// If start is itself a head, the database is up to date.
		for _, h := range heads {
			if start == h {
				return nil, nil
			}
		}
		return nil, &NoPathError{Pos: start}
	case 1:
		// fall through
	default:
		return nil, &ForkError{Heads: reachableHeads}
	}
	target := reachableHeads[0]
	if start == target {
		return nil, nil // already at the head
	}

	paths := allForwardPaths(start, target, all)
	if len(paths) == 0 {
		return nil, &NoPathError{Pos: start}
	}
	best := choosePath(paths)
	out := make([]Edge, len(best))
	for i, ce := range best {
		out[i] = allEdges[ce.idx]
	}
	return out, nil
}

// findLiveHeads returns the live heads (as canonical strings), using the kernel's
// FindHeads over remap-canonicalized LIVE edges (A5: head-finding is live-only).
func findLiveHeads(liveEdges []Edge, remap RemapTable) ([]string, error) {
	kEdges := make([]chain.Edge, 0, len(liveEdges))
	for _, e := range liveEdges {
		parentStr := canon(revString(e.Parent), remap)
		targetStr := canon(e.Target.String(), remap)
		parentRev, err := rev.ParseRevision(parentStr) // "" -> zero (genesis)
		if err != nil {
			return nil, fmt.Errorf("migrate: path-finder: canonical parent %q: %w", parentStr, err)
		}
		targetRev, err := rev.ParseRevision(targetStr)
		if err != nil {
			return nil, fmt.Errorf("migrate: path-finder: canonical target %q: %w", targetStr, err)
		}
		// Ops are irrelevant to head-finding; the kernel only reads endpoints.
		kEdges = append(kEdges, chain.Edge{Parent: parentRev, Target: targetRev, Slug: e.Slug})
	}
	heads := chain.FindHeads(kEdges)
	out := make([]string, len(heads))
	for i, h := range heads {
		out[i] = h.String()
	}
	return out, nil
}

// reachable reports whether target is reachable from start over forward edges
// (an edge is usable iff its parent equals the current frontier).
func reachable(start, target string, edges []canonEdge) bool {
	if start == target {
		return true
	}
	visited := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range edges {
			if e.parent == cur && !visited[e.target] {
				if e.target == target {
					return true
				}
				visited[e.target] = true
				queue = append(queue, e.target)
			}
		}
	}
	return false
}

// allForwardPaths enumerates every simple forward path from start to target
// (each edge used at most once, which bounds endomorphism handling).
func allForwardPaths(start, target string, edges []canonEdge) [][]canonEdge {
	var paths [][]canonEdge
	used := make([]bool, len(edges))
	var cur []canonEdge
	var dfs func(frontier string)
	dfs = func(frontier string) {
		if frontier == target && len(cur) > 0 {
			cp := make([]canonEdge, len(cur))
			copy(cp, cur)
			paths = append(paths, cp)
			// Do NOT return: a longer path could also reach target (e.g. through
			// archived originals past a consolidation endpoint). The chooser prunes.
		}
		for i, e := range edges {
			if used[i] || e.parent != frontier {
				continue
			}
			used[i] = true
			cur = append(cur, e)
			dfs(e.target)
			cur = cur[:len(cur)-1]
			used[i] = false
		}
	}
	dfs(start)
	return paths
}

// choosePath applies the total tie-break rule: fewest edges (3); then more
// consolidation edges (4a); then lexicographically-least sorted edge-id sequence
// (4b). The result is unique and deterministic.
func choosePath(paths [][]canonEdge) []canonEdge {
	best := paths[0]
	for _, p := range paths[1:] {
		if lessPath(p, best) {
			best = p
		}
	}
	return best
}

// lessPath is the strict total order the chooser minimizes over.
func lessPath(a, b []canonEdge) bool {
	if len(a) != len(b) {
		return len(a) < len(b) // (3) fewest edges
	}
	ca, cb := countConsolidation(a), countConsolidation(b)
	if ca != cb {
		return ca > cb // (4a) prefer more consolidation edges
	}
	return edgeIDSeq(a) < edgeIDSeq(b) // (4b) lexicographic on sorted edge-ids
}

func countConsolidation(p []canonEdge) int {
	n := 0
	for _, e := range p {
		if e.consol {
			n++
		}
	}
	return n
}

// edgeIDSeq is the sorted-edge-id sequence of a path, joined for a total
// lexicographic comparison.
func edgeIDSeq(p []canonEdge) string {
	ids := make([]string, len(p))
	for i, e := range p {
		ids[i] = e.id
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}
