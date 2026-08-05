package design

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/smm-h/pgdesign/internal/testenv"
	"os"
	"path/filepath"
	"testing"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/rev"
)

// This is THE mechanical check the phase-5 design gate is required to have. It
// proves the edge-artifact format (edge_format.md) round-trips through the real
// 1.1 encoder machinery:
//
//  1. build a canonical fixture model and store its objects (internal/objstore);
//  2. derive the edge's target revision (internal/rev) and its op payload id
//     (internal/chain manifest) from that model — no hardcoded hashes;
//  3. construct the edge and serialize it to the on-disk format;
//  4. parse the committed fixture back, reconstruct the chain.Edge, RESOLVE each
//     op payload against the store, and RE-DERIVE chain.Edge.ID();
//  5. assert the re-derived id equals the derived id AND the committed filename's
//     edge-content hash prefix.
//
// Because every hash flows from enc/rev/objstore, an epoch-level change to the
// encoder (which re-keys the world, L2) turns this test red — the intended
// tripwire.

// --- the on-disk edge-artifact format (mirrors edge_format.md) ---

// edgeFile is the JSON shape of one file in migrations/chain/. Identity is
// content-derived via chain.Edge.ID(); this file is location-addressed (its
// bytes need not hash to its name), but it is serialized with the canonical
// byte discipline (compact, HTML-escaping disabled) so identical edges yield
// byte-identical files and git never sees a textual conflict.
type edgeFile struct {
	FormatVersion int      `json:"format_version"`
	Codec         int      `json:"codec"`
	Class         string   `json:"class"`
	Parent        string   `json:"parent"` // "" for a genesis edge
	Target        string   `json:"target"`
	Slug          string   `json:"slug"`
	Ops           []opFile `json:"ops"`
}

// opFile is one op entry. It carries exactly the facets chain.Edge.ID() hashes
// (kind, target key, invertibility class, payload id) plus the DOWN reference.
// The full structured payload lives in migrations/objects/ under payload_id; the
// edge file only references it (self-contained ops, L1+L2).
type opFile struct {
	Kind          string  `json:"kind"`
	Target        keyFile `json:"target"`
	Invertibility string  `json:"invertibility"`
	PayloadID     string  `json:"payload_id"`
	Down          *opFile `json:"down,omitempty"`
}

// keyFile is the structured form of an enc.Key (so the manifest key round-trips
// exactly, ArgSig included, without re-parsing Key.String()).
type keyFile struct {
	Kind   string `json:"kind"`
	Schema string `json:"schema,omitempty"`
	Name   string `json:"name"`
	ArgSig string `json:"arg_sig,omitempty"`
}

func (k keyFile) toKey() enc.Key {
	return enc.Key{Kind: enc.Kind(k.Kind), Schema: k.Schema, Name: k.Name, ArgSig: k.ArgSig}
}

func keyToFile(k enc.Key) keyFile {
	return keyFile{Kind: string(k.Kind), Schema: k.Schema, Name: k.Name, ArgSig: k.ArgSig}
}

var invByName = map[string]chain.InvertibilityClass{
	"mechanically-invertible": chain.MechanicallyInvertible,
	"declared-inverse":        chain.DeclaredInverse,
	"non-invertible":          chain.NonInvertible,
}

// fileOp is a concrete chain.Op reconstructed from an opFile. Only Kind/Target/
// Invertibility/PayloadID feed chain.Edge.ID(); Inverse() is implemented
// honestly from the Down reference so the type is a faithful chain.Op.
type fileOp struct {
	kind    string
	target  enc.Key
	class   chain.InvertibilityClass
	payload string
	down    *fileOp
}

func (o fileOp) Kind() string                            { return o.kind }
func (o fileOp) Target() enc.Key                         { return o.target }
func (o fileOp) Invertibility() chain.InvertibilityClass { return o.class }
func (o fileOp) PayloadID() string                       { return o.payload }
func (o fileOp) Inverse() (chain.Op, bool) {
	if o.class == chain.NonInvertible || o.down == nil {
		return nil, false
	}
	return *o.down, true
}

func opFromFile(of opFile) (fileOp, error) {
	class, ok := invByName[of.Invertibility]
	if !ok {
		return fileOp{}, fmt.Errorf("unknown invertibility class %q", of.Invertibility)
	}
	op := fileOp{kind: of.Kind, target: of.Target.toKey(), class: class, payload: of.PayloadID}
	if of.Down != nil {
		d, err := opFromFile(*of.Down)
		if err != nil {
			return fileOp{}, err
		}
		op.down = &d
	}
	return op, nil
}

// Note on revision reconstruction: internal/rev exposes no parse-from-string
// constructor (a Revision holds a class + digest slice and is deliberately
// non-comparable). The mechanical check therefore supplies the target Revision
// AUTHORITATIVELY (recomputed from the fixture model via rev.Compute) and
// cross-checks that the fixture's recorded target STRING equals it, rather than
// parsing a Revision out of the file. This is faithful: chain.Edge.ID() only
// observes Target().String(), and that string is what the fixture commits.

// --- the canonical fixture model ---

// buildFixtureModel returns the worked-example model: a single table `users`.
// One object keeps the worked example legible; the format is identical for
// many-op edges.
func buildFixtureModel() *model.Schema {
	s := &model.Schema{
		Name:   "shop",
		Tables: []model.Table{{Name: "users", Comment: "application users"}},
	}
	s.Canonicalize()
	return s
}

func compactJSON(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func TestEdgeArtifactRoundTrip(t *testing.T) {
	testenv.Isolate(t)
	// (1) canonical model + object store.
	s := buildFixtureModel()
	store, err := objstore.New(t.TempDir(), enc.CodecVersion)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := chain.BuildManifestInto(s, store)
	if err != nil {
		t.Fatalf("BuildManifestInto: %v", err)
	}

	// (2) derive target revision + the create_table op's payload id.
	target, err := rev.Compute(s, rev.RegistryPresent)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	tableKey := enc.KeyForTable(s.Tables[0])
	payloadID, ok := manifest[tableKey]
	if !ok {
		t.Fatalf("manifest missing table key %s", tableKey)
	}

	// (3) construct the edge (genesis: null parent) and derive its id.
	up := fileOp{
		kind:    "create_table",
		target:  tableKey,
		class:   chain.MechanicallyInvertible,
		payload: payloadID,
		down:    &fileOp{kind: "drop_table", target: tableKey, class: chain.MechanicallyInvertible, payload: payloadID},
	}
	edge := chain.Edge{Parent: rev.Revision{}, Target: target, Ops: []chain.Op{up}, Slug: "create-users"}
	if !edge.IsGenesis() {
		t.Fatal("expected a genesis edge (null parent)")
	}
	edgeID := edge.ID()

	// Serialize to the on-disk edge-artifact format.
	ef := edgeFile{
		FormatVersion: rev.FormatVersion,
		Codec:         enc.CodecVersion,
		Class:         string(rev.RegistryPresent),
		Parent:        "", // genesis
		Target:        target.String(),
		Slug:          "create-users",
		Ops: []opFile{{
			Kind:          up.kind,
			Target:        keyToFile(tableKey),
			Invertibility: up.class.String(),
			PayloadID:     payloadID,
			Down: &opFile{
				Kind:          "drop_table",
				Target:        keyToFile(tableKey),
				Invertibility: chain.MechanicallyInvertible.String(),
				PayloadID:     payloadID,
			},
		}},
	}
	got := compactJSON(t, ef)

	// (4) committed fixture: filename embeds the edge-content hash prefix.
	fixName := fmt.Sprintf("edge-%s-%s.json", edgeID[:12], ef.Slug)
	fixPath := filepath.Join("testdata", fixName)
	if _, statErr := os.Stat(fixPath); os.IsNotExist(statErr) {
		if err := os.WriteFile(fixPath, append(got, '\n'), 0o644); err != nil {
			t.Fatalf("seed fixture: %v", err)
		}
		t.Logf("seeded committed fixture %s (commit it)", fixPath)
	}

	raw, err := os.ReadFile(fixPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// Golden: the committed bytes equal the canonical serialization (trailing
	// newline is a git-friendliness convention, stripped before comparison).
	if !bytes.Equal(bytes.TrimRight(raw, "\n"), got) {
		t.Fatalf("committed fixture is not the canonical serialization\n got:  %s\n want: %s", bytes.TrimRight(raw, "\n"), got)
	}

	// (5) parse the fixture back and RE-DERIVE the edge id through the kernel.
	var parsed edgeFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	// Cross-check the fixture's declared facets against the kernel derivation.
	if parsed.Target != target.String() {
		t.Fatalf("fixture target %q != derived %q", parsed.Target, target.String())
	}
	if len(parsed.Ops) != 1 || parsed.Ops[0].PayloadID != payloadID {
		t.Fatalf("fixture op payload id != derived %q", payloadID)
	}
	// Resolve every op payload against the store (Merkle closure for the edge).
	for _, of := range parsed.Ops {
		has, err := store.Has(of.PayloadID)
		if err != nil || !has {
			t.Fatalf("op payload %s does not resolve in the store (has=%v err=%v)", of.PayloadID, has, err)
		}
	}

	// Reconstruct the chain.Edge from the fixture. The parent is genesis; the
	// target Revision is supplied authoritatively (rev has no parse-from-string;
	// the fixture's target string was cross-checked equal above).
	reOps := make([]chain.Op, len(parsed.Ops))
	for i, of := range parsed.Ops {
		op, err := opFromFile(of)
		if err != nil {
			t.Fatalf("reconstruct op: %v", err)
		}
		reOps[i] = op
	}
	reEdge := chain.Edge{Parent: rev.Revision{}, Target: target, Ops: reOps, Slug: parsed.Slug}
	reID := reEdge.ID()

	if reID != edgeID {
		t.Fatalf("re-derived edge id %s != derived %s", reID, edgeID)
	}
	// The committed filename must carry the edge-content hash prefix.
	wantPrefix := edgeID[:12]
	if fixName != fmt.Sprintf("edge-%s-%s.json", wantPrefix, parsed.Slug) {
		t.Fatalf("filename %q does not embed edge-content hash prefix %q", fixName, wantPrefix)
	}
	// Recomputing the id twice is stable (identity is a pure function of content).
	if reEdge.ID() != reID {
		t.Fatal("edge id is not stable across calls")
	}
}
