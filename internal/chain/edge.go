package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/smm-h/pgdesign/internal/rev"
)

// sortedStrings returns a sorted copy of ss.
func sortedStrings(ss []string) []string {
	out := make([]string, len(ss))
	copy(out, ss)
	sort.Strings(out)
	return out
}

// Edge is a parent-linked migration edge between two revisions. An edge carries
// a parent (from) revision, a target (to) revision, an ordered op-list, and a
// human-readable slug. Its IDENTITY is CONTENT-DERIVED (see ID): the edge is
// named by a hash of its content plus the slug, never by a counter. A DISPLAY
// SEQUENCE for listings is derived from graph topology at listing time and is
// never part of identity — so parallel edges, pure-DML endomorphisms (R -> R),
// and concurrent branch allocation can never collide on a name or race a
// counter.
//
// The GENESIS edge has a NULL parent, modeled as a zero-value rev.Revision
// (Parent.IsZero() == true): it establishes an initial revision from nothing.
type Edge struct {
	// Parent is the from-revision. A genesis edge's Parent is the zero Revision
	// (Parent.IsZero()).
	Parent rev.Revision
	// Target is the to-revision.
	Target rev.Revision
	// Ops is the ordered op-list the edge applies.
	Ops []Op
	// Slug is the human-readable display name (auto-derived in later phases,
	// override-able). It participates in identity so two otherwise-identical
	// edges given different slugs are different edges.
	Slug string
}

// IsGenesis reports whether the edge has a null parent (establishes an initial
// revision from nothing).
func (e Edge) IsGenesis() bool { return e.Parent.IsZero() }

// edgeContent is the canonical, hashable projection of an edge's identity. It
// deliberately excludes any display sequence (identity is topology-independent)
// and captures each op by its stable observable facets — kind, target, L4
// class, and payload content-id — never by a Go pointer or in-memory address.
type edgeContent struct {
	Parent string      `json:"parent"` // Parent.String(), or "" for genesis
	Target string      `json:"target"`
	Slug   string      `json:"slug"`
	Ops    []opContent `json:"ops"`
}

type opContent struct {
	Kind          string `json:"kind"`
	Target        string `json:"target"`
	Invertibility int    `json:"invertibility"`
	PayloadID     string `json:"payload_id"`
}

// ID returns the content-derived identity of the edge: the lowercase SHA-256 hex
// of its canonical content projection. Equal content (same parent, target,
// slug, and op sequence by their observable facets) yields the same id; any
// difference — different ops, a different slug, a different endpoint — yields a
// different id. Parallel edges (same parent and target, different ops or slug)
// and endomorphisms (parent == target) therefore never collide unless they are
// genuinely the SAME edge.
func (e Edge) ID() string {
	parent := ""
	if !e.IsGenesis() {
		parent = e.Parent.String()
	}
	c := edgeContent{
		Parent: parent,
		Target: e.Target.String(),
		Slug:   e.Slug,
		Ops:    make([]opContent, len(e.Ops)),
	}
	for i, op := range e.Ops {
		c.Ops[i] = opContent{
			Kind:          op.Kind(),
			Target:        op.Target().String(),
			Invertibility: int(op.Invertibility()),
			PayloadID:     op.PayloadID(),
		}
	}
	sum := sha256.Sum256(canonicalJSON(c))
	return hex.EncodeToString(sum[:])
}

// canonicalJSON marshals v to compact JSON with HTML escaping disabled and the
// trailing newline stripped, matching enc/rev's byte discipline so edge ids are
// stable across Go versions and independent of struct-field encoding quirks.
func canonicalJSON(v any) []byte {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	// edgeContent contains only strings/ints/slices — marshalling cannot fail.
	_ = e.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// FindHeads returns the HEAD revisions of an edge set: revisions that are some
// edge's target and are NOT any (non-genesis) edge's parent. A linear chain has
// one head; a fork has two or more. The result is deterministic, ordered by the
// revision string, and de-duplicated. Genesis parents (null) are not revisions
// and never count as "having a child edge below them" for this purpose.
func FindHeads(edges []Edge) []rev.Revision {
	// Revisions are compared by their String() form (class-tagged hex), which is
	// collision-free across classes and never triggers cross-class Equal errors
	// during graph bookkeeping.
	parents := make(map[string]struct{}, len(edges))
	targets := make(map[string]rev.Revision, len(edges))
	var targetOrder []string
	for _, e := range edges {
		if !e.IsGenesis() {
			parents[e.Parent.String()] = struct{}{}
		}
		ts := e.Target.String()
		if _, seen := targets[ts]; !seen {
			targetOrder = append(targetOrder, ts)
		}
		targets[ts] = e.Target
	}
	var heads []rev.Revision
	seen := make(map[string]struct{})
	for _, ts := range sortedStrings(targetOrder) {
		if _, isParent := parents[ts]; isParent {
			continue
		}
		if _, dup := seen[ts]; dup {
			continue
		}
		seen[ts] = struct{}{}
		heads = append(heads, targets[ts])
	}
	return heads
}

// FindGenesis returns the genesis edges of an edge set (those with a null
// parent), in input order.
func FindGenesis(edges []Edge) []Edge {
	var out []Edge
	for _, e := range edges {
		if e.IsGenesis() {
			out = append(out, e)
		}
	}
	return out
}

// ComposePath is composition in the free category (L3): the concatenation of a
// CONTIGUOUS path of edges' op-lists. The edges must chain end-to-end — each
// edge's target must equal the next edge's parent — or ComposePath returns an
// error naming the break. The identity morphism (an empty path) is VIRTUAL: it
// is the empty op-list, never a stored edge, so composing zero edges yields nil
// ops with no error.
//
// Composition operates on OP-LISTS, not on Deltas (Deltas do not compose). A
// consolidation edge whose ops are exactly this concatenation is squash-sound by
// construction under the adopted concatenation form (roadmap 5.3); the
// substantive squash-commutation check lives there, not here.
func ComposePath(edges []Edge) ([]Op, error) {
	if len(edges) == 0 {
		return nil, nil
	}
	var ops []Op
	for i, e := range edges {
		if i > 0 {
			prev := edges[i-1].Target
			if prev.String() != e.Parent.String() {
				return nil, fmt.Errorf("chain: non-contiguous path: edge %d target %s != edge %d parent %s", i-1, prev, i, e.Parent)
			}
		}
		ops = append(ops, e.Ops...)
	}
	return ops, nil
}

// OpSimulator is the plug-in point for edge-endpoint consistency: given a
// from-manifest and an op-list, it returns the manifest the ops produce. It is
// the second half of the store<->chain consistency check (the first is
// VerifyClosure). Concrete simulation of op families lands with roadmap 5.2;
// the kernel defines only this interface so the checker (VerifyEdgeEndpoint)
// exists now and 5.2 supplies the simulator.
type OpSimulator interface {
	Simulate(from Manifest, ops []Op) (Manifest, error)
}

// VerifyEdgeEndpoint is the EDGE-ENDPOINT CONSISTENCY primitive: it asserts that
// simulating the edge's ops on its from-manifest reproduces its to-manifest. It
// requires an OpSimulator (roadmap 5.2); passing nil is a hard error, not a
// silent skip — a caller that cannot simulate must not claim the endpoint is
// consistent.
func VerifyEdgeEndpoint(e Edge, from, to Manifest, sim OpSimulator) error {
	if sim == nil {
		return fmt.Errorf("chain: edge-endpoint check for %s requires an OpSimulator (op simulation lands with roadmap 5.2)", e.ID())
	}
	got, err := sim.Simulate(from, e.Ops)
	if err != nil {
		return fmt.Errorf("chain: simulating edge %s: %w", e.ID(), err)
	}
	if !got.Equal(to) {
		d := to.Diff(got)
		return fmt.Errorf("chain: edge %s ops do not map its from-manifest to its to-manifest (added=%v removed=%v changed=%v)", e.ID(), d.Added, d.Removed, d.Changed)
	}
	return nil
}
