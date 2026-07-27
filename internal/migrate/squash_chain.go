package migrate

// Chain-mode squash = consolidation (roadmap 5.3, L3+L4).
//
// A squash is NOT a rewrite of history: it is an ADDITIONAL chain edge. Given a
// live path e_1 -> ... -> e_n (from-revision R_from to to-revision R_to), squash
// mints a CONSOLIDATION EDGE whose parent is R_from, whose target is R_to, and
// whose op-list is the ORDERED CONCATENATION of the superseded path's ops. The
// concatenation preserves every DML/RawSQL op verbatim BY CONSTRUCTION (it never
// drops or folds — that hazard belonged to the retired optimizer). The superseded
// originals RETIRE INTACT to migrations/archive/ (moved, never rewritten), so a
// mid-range database resumes by walking the archived originals through the 5.0
// path-finder, and applied-history rows that reference their edge_ids stay
// resolvable. Tracking/journal rows are left untouched.
//
// The M200 applied-version refusal (legacy squash) DIES here: consolidation is
// legal regardless of applied state, precisely because originals archive intact
// and mid-range databases resume via the path-finder.
//
// SQUASH SOUNDNESS (L3/L5's functor equation) holds DEFINITIONALLY under the
// concatenation form: apply(consolidation) executes exactly the same op sequence
// as apply(e_1..e_n), so it lands at the same manifest and renders the same SQL.
// The commutation smoke test pins this.
//
// The A6 disjointness invariant (path_finder.md) is enforced HERE at creation: a
// new consolidation's superseded-edge-id set must not intersect any existing
// consolidation's set. The consistency checker re-verifies it.
//
// 5.4 note: checksums live ONLY on the apply surface. There is no rollback
// checksum surface — post-5.6 rollback reads the journal, never these files.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/rev"
)

// ChainSquashResult reports a chain-mode consolidation squash.
type ChainSquashResult struct {
	ConsolidationFile string   // written consolidation edge filename (live)
	ConsolidationID   string   // consolidation edge content id
	FromRevision      string   // resolved from-revision string ("" for genesis)
	ToRevision        string   // resolved to-revision string
	SupersededIDs     []string // superseded (archived) edge content ids, in path order
	ArchivedFiles     []string // archived-original filenames, in path order
	OpCount           int      // ops in the consolidation edge
	DownForm          string   // "manifest-diff" | "composed-recorded-downs" | "irreversible"
}

// SquashChain consolidates the live path from fromRef to toRef into one
// consolidation edge, archiving the superseded originals intact. fromRef/toRef are
// rev-or-edge references (see resolveSquashEndpoint). slug is the consolidation
// edge's display name; empty auto-derives one.
//
// It is a pure file operation: it does not read or trust the database (applied
// state is irrelevant to consolidation). The caller keeps --db mandatory and runs
// the pre-upgrade guard, per 0.6d.
func SquashChain(p *ChainProject, fromRef, toRef, slug string) (*ChainSquashResult, error) {
	live, err := p.LoadLiveEdges()
	if err != nil {
		return nil, err
	}
	if len(live) == 0 {
		return nil, fmt.Errorf("migrate squash: chain has no live edges to consolidate")
	}

	fromRev, err := resolveSquashEndpoint(live, fromRef, false)
	if err != nil {
		return nil, fmt.Errorf("migrate squash: --from %q: %w", fromRef, err)
	}
	toRev, err := resolveSquashEndpoint(live, toRef, true)
	if err != nil {
		return nil, fmt.Errorf("migrate squash: --to %q: %w", toRef, err)
	}

	path, err := findLiveConsolidationPath(live, fromRev, toRev)
	if err != nil {
		return nil, fmt.Errorf("migrate squash: %w", err)
	}

	// All edges in the range must share a model class (the consolidation carries it).
	class := path[0].Class
	for _, e := range path {
		if e.Class != class {
			return nil, fmt.Errorf("migrate squash: range mixes model classes (%s and %s); cannot consolidate", class, e.Class)
		}
	}

	// Ordered concatenation of the superseded path's ops (verbatim). Collect the
	// superseded edge-id set for A6 and archival.
	var ops []SelfContainedOp
	var supersededIDs []string
	for _, e := range path {
		ops = append(ops, e.Ops...)
		supersededIDs = append(supersededIDs, e.ID())
	}

	// A6 disjointness: reject if this superseded set intersects any existing
	// consolidation's set (path_finder.md — enforced at CREATION).
	if err := checkConsolidationDisjoint(live, supersededIDs); err != nil {
		return nil, fmt.Errorf("migrate squash: %w", err)
	}

	downForm, _ := classifyConsolidationDown(ops)

	if slug == "" {
		slug = defaultSquashSlug(fromRev, toRev)
	}
	ce := Edge{
		Parent:            fromRev,
		Target:            toRev,
		Slug:              slug,
		Class:             class,
		Ops:               ops,
		Consolidation:     true,
		SupersededEdgeIDs: supersededIDs,
	}

	// Write the consolidation edge live, then retire the originals to archive/
	// INTACT (a move — never a rewrite). The endpoints already exist, so no new
	// revision manifest is written (parent/target are existing revisions).
	ceName, err := p.WriteEdge(ce)
	if err != nil {
		return nil, fmt.Errorf("migrate squash: write consolidation edge: %w", err)
	}
	var archived []string
	for _, e := range path {
		name, err := moveEdgeToArchive(p, e)
		if err != nil {
			return nil, fmt.Errorf("migrate squash: archive original %s: %w", e.ID()[:12], err)
		}
		archived = append(archived, name)
	}

	return &ChainSquashResult{
		ConsolidationFile: ceName,
		ConsolidationID:   ce.ID(),
		FromRevision:      revString(fromRev),
		ToRevision:        toRev.String(),
		SupersededIDs:     supersededIDs,
		ArchivedFiles:     archived,
		OpCount:           len(ops),
		DownForm:          downForm,
	}, nil
}

// resolveSquashEndpoint resolves a rev-or-edge reference to a revision. The forms
// are disambiguated by shape:
//   - "genesis" -> the zero (genesis) revision (valid only as a from-endpoint).
//   - a reference CONTAINING ':' -> a revision string (full or a unique prefix of a
//     known live endpoint revision).
//   - otherwise (bare hex) -> a live EDGE id prefix; atTarget selects the edge's
//     target (a to-endpoint) vs its parent (a from-endpoint).
func resolveSquashEndpoint(live []Edge, ref string, atTarget bool) (rev.Revision, error) {
	if ref == "genesis" {
		if atTarget {
			return rev.Revision{}, fmt.Errorf("genesis is not a valid --to endpoint")
		}
		return rev.Revision{}, nil
	}
	if strings.Contains(ref, ":") {
		return resolveEndpointRevision(live, ref)
	}
	return resolveEndpointEdge(live, ref, atTarget)
}

// resolveEndpointRevision matches ref against the set of live endpoint revisions
// (edge parents and targets) by exact string or unique prefix.
func resolveEndpointRevision(live []Edge, ref string) (rev.Revision, error) {
	seen := map[string]rev.Revision{}
	for _, e := range live {
		if !e.Parent.IsZero() {
			seen[e.Parent.String()] = e.Parent
		}
		seen[e.Target.String()] = e.Target
	}
	if r, ok := seen[ref]; ok {
		return r, nil
	}
	var matches []rev.Revision
	for s, r := range seen {
		if strings.HasPrefix(s, ref) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return rev.Revision{}, fmt.Errorf("no live edge endpoint matches revision %q", ref)
	default:
		return rev.Revision{}, fmt.Errorf("revision %q is ambiguous (%d live endpoints match)", ref, len(matches))
	}
}

// resolveEndpointEdge matches ref against live edge ids by prefix and returns the
// matched edge's target (atTarget) or parent.
func resolveEndpointEdge(live []Edge, ref string, atTarget bool) (rev.Revision, error) {
	if len(ref) < 6 {
		return rev.Revision{}, fmt.Errorf("edge reference %q is too short (need >= 6 hex chars, or a revision containing ':')", ref)
	}
	var matches []Edge
	for _, e := range live {
		if strings.HasPrefix(e.ID(), ref) {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 1:
		if atTarget {
			return matches[0].Target, nil
		}
		return matches[0].Parent, nil
	case 0:
		return rev.Revision{}, fmt.Errorf("no live edge id matches %q", ref)
	default:
		return rev.Revision{}, fmt.Errorf("edge id %q is ambiguous (%d live edges match)", ref, len(matches))
	}
}

// findLiveConsolidationPath returns the unique forward path over LIVE edges from
// fromRev to toRev. It requires at least two edges (a single-edge "range" has
// nothing to consolidate) and a UNIQUE path (a fork is ambiguous).
func findLiveConsolidationPath(live []Edge, fromRev, toRev rev.Revision) ([]Edge, error) {
	fromStr := revString(fromRev)
	toStr := toRev.String()
	if fromStr == toStr {
		return nil, fmt.Errorf("--from and --to resolve to the same revision %q; nothing to consolidate", toStr)
	}
	canon := toCanonEdges(live, RemapTable{})
	paths := allForwardPaths(fromStr, toStr, canon)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no live path from %s to %s", displayRev(fromStr), toStr)
	}
	if len(paths) > 1 {
		return nil, fmt.Errorf("range %s..%s is ambiguous (%d live paths); consolidate an unforked range", displayRev(fromStr), toStr, len(paths))
	}
	best := paths[0]
	if len(best) < 2 {
		return nil, fmt.Errorf("range %s..%s spans a single edge; nothing to consolidate", displayRev(fromStr), toStr)
	}
	out := make([]Edge, len(best))
	for i, ce := range best {
		out[i] = live[ce.idx]
	}
	return out, nil
}

// displayRev renders a possibly-empty (genesis) revision string for messages.
func displayRev(s string) string {
	if s == "" {
		return "genesis"
	}
	return s
}

// checkConsolidationDisjoint enforces the A6 invariant: the new consolidation's
// superseded-edge-id set must be pairwise disjoint from every existing
// consolidation's set. A non-empty intersection is a HARD ERROR naming the
// overlapping edge and the existing consolidation.
func checkConsolidationDisjoint(live []Edge, newSuperseded []string) error {
	newSet := make(map[string]bool, len(newSuperseded))
	for _, id := range newSuperseded {
		newSet[id] = true
	}
	for _, e := range live {
		if !e.Consolidation {
			continue
		}
		for _, id := range e.SupersededEdgeIDs {
			if newSet[id] {
				return fmt.Errorf("A6 disjointness violation: edge %s is already superseded by consolidation %s (consolidation ranges must be pairwise disjoint)", id[:12], e.ID()[:12])
			}
		}
	}
	return nil
}

// classifyConsolidationDown decides the consolidation range's down FORM by L4
// type — no runtime judgment. A fully-mechanically-invertible range takes the
// manifest-diff down (MechanicalRange.ManifestDiffDown, the reversed composition
// of mechanical inverses); a range containing any declared-inverse (incl. vacuous
// DML) op composes the originals' recorded downs (InverseOfList); a range with a
// non-invertible op is irreversible (nil down).
func classifyConsolidationDown(ops []SelfContainedOp) (form string, down []chain.Op) {
	chainOps := make([]chain.Op, len(ops))
	for i, o := range ops {
		chainOps[i] = o
	}
	if chain.AllMechanicallyInvertible(chainOps) {
		mr, err := chain.NewMechanicalRange(chainOps)
		if err == nil {
			return "manifest-diff", mr.ManifestDiffDown()
		}
	}
	if inv, ok := chain.InverseOfList(chainOps); ok {
		return "composed-recorded-downs", inv
	}
	return "irreversible", nil
}

// defaultSquashSlug derives a filename-safe display slug for a consolidation edge
// from its endpoint revision-hash prefixes.
func defaultSquashSlug(fromRev, toRev rev.Revision) string {
	from := "genesis"
	if !fromRev.IsZero() {
		from = shortRevHash(fromRev)
	}
	return fmt.Sprintf("squash-%s-to-%s", from, shortRevHash(toRev))
}

// shortRevHash returns the first 8 hex chars of a revision's content hash (the
// portion after the "class:" prefix), for a compact, filename-safe slug fragment.
func shortRevHash(r rev.Revision) string {
	s := r.String()
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

// moveEdgeToArchive retires a superseded edge to migrations/archive/ INTACT: it
// renames the file (never re-encodes it), so the archived original is byte-for-byte
// the same artifact — its content-derived filename is identical in both directories.
func moveEdgeToArchive(p *ChainProject, e Edge) (string, error) {
	if e.File == "" {
		return "", fmt.Errorf("edge %s has no on-disk file to archive", e.ID()[:12])
	}
	name := filepath.Base(e.File)
	dst := filepath.Join(p.archivePath(), name)
	if err := os.MkdirAll(p.archivePath(), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(e.File, dst); err != nil {
		return "", err
	}
	return name, nil
}
