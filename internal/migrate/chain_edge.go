package migrate

// On-disk chain edges (roadmap 5.2, edge_format.md).
//
// One file per edge in migrations/chain/ (live) or migrations/archive/
// (superseded/rebased-away). The file is JSON with the same canonical byte
// discipline as enc/rev/chain, so identical edges serialize byte-identically and
// git never sees a textual or allocation conflict (boundary item 6). Identity is
// CONTENT-DERIVED via chain.Edge.ID(): the filename is edge-<id[:12]>-<slug>.json.
//
// LOAD verifies four things (edge_format.md, amendment A3):
//   - format_version / codec framing match this build;
//   - the edge file's class equals the class tagged inside its parent/target
//     revision strings (class carriage integrity, L7 — the handoff note);
//   - every op payload id resolves in the store (ParseOp) and every op's down
//     cache re-derives from its up payload (VerifyDown);
//   - the reconstructed edge's content id re-derives the filename's hash prefix.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/rev"
)

// validModelClass reports whether c is one of the two known model classes.
// rev.ModelClass.valid is unexported, so this is the migrate-side check.
func validModelClass(c rev.ModelClass) bool {
	return c == rev.RegistryPresent || c == rev.RegistryAbsent
}

// Edge is an on-disk chain edge: a content-identified migration between two
// revisions, its ops self-contained in the object store. It wraps the kernel's
// identity/graph model (chain.Edge) with the on-disk facets — the model class
// and the resolved SelfContainedOps — plus file bookkeeping (never part of
// identity).
type Edge struct {
	Parent   rev.Revision      // from-revision; zero (IsZero) for a genesis edge
	Target   rev.Revision      // to-revision
	Slug     string            // human display name (participates in identity)
	Class    rev.ModelClass    // endpoints' model class (class-aware checker; A?/L7)
	Ops      []SelfContainedOp // resolved ops (implement chain.Op)
	File     string            // absolute path this edge was loaded from ("" if in-memory)
	Archived bool              // true when loaded from archive/ (traversal-only)
	// Consolidation marks a squash consolidation edge (roadmap 5.3). The
	// path-finder prefers consolidation edges as its 4a tie-break. It is not yet
	// persisted in the edge file (5.3 owns that); loaded edges are always false
	// until 5.3 records the superseded-edge set. The seam exists so the path-finder
	// is total and consolidation-ready now.
	Consolidation bool
}

// chainEdge projects to the kernel's identity/graph edge.
func (e Edge) chainEdge() chain.Edge {
	ops := make([]chain.Op, len(e.Ops))
	for i, o := range e.Ops {
		ops[i] = o
	}
	return chain.Edge{Parent: e.Parent, Target: e.Target, Ops: ops, Slug: e.Slug}
}

// ID returns the edge's content-derived identity (chain.Edge.ID()).
func (e Edge) ID() string { return e.chainEdge().ID() }

// IsGenesis reports whether the edge has a null parent.
func (e Edge) IsGenesis() bool { return e.Parent.IsZero() }

// edgeFileJSON is the on-disk edge-artifact body (edge_format.md § Body schema).
type edgeFileJSON struct {
	FormatVersion int      `json:"format_version"`
	Codec         int      `json:"codec"`
	Class         string   `json:"class"`
	Parent        string   `json:"parent"` // "" for a genesis edge
	Target        string   `json:"target"`
	Slug          string   `json:"slug"`
	Ops           []OpJSON `json:"ops"`
}

// edgeFileName returns the content-derived filename for an edge id and slug.
func edgeFileName(id, slug string) string {
	return fmt.Sprintf("edge-%s-%s.json", id[:12], slug)
}

// WriteEdge serializes e into migrations/chain/ under its content-derived name
// and returns the filename. The write is idempotent: an identical edge yields the
// same filename and byte-identical content. class must be valid.
func (p *ChainProject) WriteEdge(e Edge) (string, error) {
	data, name, err := p.encodeEdge(e)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(filepath.Join(p.edgesPath(), name), data); err != nil {
		return "", err
	}
	return name, nil
}

// ArchiveEdge writes e into migrations/archive/ (retired originals). Same format
// and naming as a live edge; only the directory differs.
func (p *ChainProject) ArchiveEdge(e Edge) (string, error) {
	data, name, err := p.encodeEdge(e)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(filepath.Join(p.archivePath(), name), data); err != nil {
		return "", err
	}
	return name, nil
}

func (p *ChainProject) encodeEdge(e Edge) (data []byte, name string, err error) {
	if !validModelClass(e.Class) {
		return nil, "", fmt.Errorf("migrate: edge has unknown model class %q", e.Class)
	}
	// Class carriage integrity: the endpoints' revision strings must be tagged
	// with the edge's declared class (L7). A genesis parent is exempt (zero).
	if !e.IsGenesis() && e.Parent.Class() != e.Class {
		return nil, "", fmt.Errorf("migrate: edge parent class %q != edge class %q", e.Parent.Class(), e.Class)
	}
	if e.Target.Class() != e.Class {
		return nil, "", fmt.Errorf("migrate: edge target class %q != edge class %q", e.Target.Class(), e.Class)
	}
	f := edgeFileJSON{
		FormatVersion: rev.FormatVersion,
		Codec:         enc.CodecVersion,
		Class:         string(e.Class),
		Parent:        revString(e.Parent),
		Target:        e.Target.String(),
		Slug:          e.Slug,
		Ops:           serializeOps(e.Ops),
	}
	b, err := canonicalOpJSON(f)
	if err != nil {
		return nil, "", fmt.Errorf("migrate: encode edge: %w", err)
	}
	// A trailing newline is a git-friendliness convention (matches the design
	// fixtures); LoadEdge strips it before parsing.
	return append(b, '\n'), edgeFileName(e.ID(), e.Slug), nil
}

// revString renders a revision, or "" for a genesis (zero) revision.
func revString(r rev.Revision) string {
	if r.IsZero() {
		return ""
	}
	return r.String()
}

// serializeOps projects a slice of self-contained ops to their edge-file entries.
func serializeOps(ops []SelfContainedOp) []OpJSON {
	out := make([]OpJSON, len(ops))
	for i, o := range ops {
		out[i] = o.Serialize()
	}
	return out
}

// LoadEdge reads and fully verifies an edge file at path. archived records
// whether it came from archive/.
func (p *ChainProject) LoadEdge(path string, archived bool) (Edge, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Edge{}, fmt.Errorf("migrate: reading edge %q: %w", path, err)
	}
	var f edgeFileJSON
	if err := json.Unmarshal(raw, &f); err != nil {
		return Edge{}, fmt.Errorf("migrate: parsing edge %q: %w", path, err)
	}
	if f.FormatVersion != rev.FormatVersion {
		return Edge{}, fmt.Errorf("migrate: edge %q format_version %d, want %d", path, f.FormatVersion, rev.FormatVersion)
	}
	if f.Codec != enc.CodecVersion {
		return Edge{}, fmt.Errorf("migrate: edge %q codec epoch %d, want %d", path, f.Codec, enc.CodecVersion)
	}
	class := rev.ModelClass(f.Class)
	if !validModelClass(class) {
		return Edge{}, fmt.Errorf("migrate: edge %q has unknown model class %q", path, f.Class)
	}
	parent, err := rev.ParseRevision(f.Parent)
	if err != nil {
		return Edge{}, fmt.Errorf("migrate: edge %q parent: %w", path, err)
	}
	target, err := rev.ParseRevision(f.Target)
	if err != nil {
		return Edge{}, fmt.Errorf("migrate: edge %q target: %w", path, err)
	}
	// Class carriage integrity (L7): the class tagged inside each endpoint string
	// must equal the edge's declared class.
	if !parent.IsZero() && parent.Class() != class {
		return Edge{}, fmt.Errorf("migrate: edge %q parent class %q != declared class %q", path, parent.Class(), class)
	}
	if target.Class() != class {
		return Edge{}, fmt.Errorf("migrate: edge %q target class %q != declared class %q", path, target.Class(), class)
	}
	ops := make([]SelfContainedOp, len(f.Ops))
	for i, oj := range f.Ops {
		op, err := ParseOp(p.store, oj) // resolves the op payload against the store
		if err != nil {
			return Edge{}, fmt.Errorf("migrate: edge %q op %d: %w", path, i, err)
		}
		if err := VerifyDown(p.store, op); err != nil { // A3: down cache re-derives from up
			return Edge{}, fmt.Errorf("migrate: edge %q op %d: %w", path, i, err)
		}
		ops[i] = op
	}
	e := Edge{Parent: parent, Target: target, Slug: f.Slug, Class: class, Ops: ops, File: path, Archived: archived}
	// The filename must carry the reconstructed edge's content-hash prefix.
	base := filepath.Base(path)
	want := edgeFileName(e.ID(), f.Slug)
	if base != want {
		return Edge{}, fmt.Errorf("migrate: edge file %q does not match its content-derived name %q (edge content-hash prefix mismatch — tampered or corrupt)", base, want)
	}
	return e, nil
}

// readEdgeCodec reads just the codec epoch field from an edge file, for the
// epoch-homogeneity check to name the offending edge without re-parsing ops.
func readEdgeCodec(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("migrate: reading edge %q: %w", path, err)
	}
	var f struct {
		Codec int `json:"codec"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, fmt.Errorf("migrate: parsing edge %q: %w", path, err)
	}
	return f.Codec, nil
}

// LoadLiveEdges reads and verifies every edge in migrations/chain/.
func (p *ChainProject) LoadLiveEdges() ([]Edge, error) {
	return p.loadEdgeDir(p.edgesPath(), false)
}

// LoadArchivedEdges reads and verifies every edge in migrations/archive/.
func (p *ChainProject) LoadArchivedEdges() ([]Edge, error) {
	return p.loadEdgeDir(p.archivePath(), true)
}

// LoadAllEdges returns live edges followed by archived edges (the path-finder's
// archive-inclusive traversal domain).
func (p *ChainProject) LoadAllEdges() ([]Edge, error) {
	live, err := p.LoadLiveEdges()
	if err != nil {
		return nil, err
	}
	arch, err := p.LoadArchivedEdges()
	if err != nil {
		return nil, err
	}
	return append(live, arch...), nil
}

func (p *ChainProject) loadEdgeDir(dir string, archived bool) ([]Edge, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("migrate: listing %q: %w", dir, err)
	}
	// Deterministic order (by filename) so callers see a stable edge sequence.
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "edge-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	out := make([]Edge, 0, len(names))
	for _, n := range names {
		e, err := p.LoadEdge(filepath.Join(dir, n), archived)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}
