package migrate

// Store<->chain<->files consistency check (roadmap 5.2).
//
// Three checks, exposed as one function 6.2 and 7.2 invoke:
//
//  1. EPOCH HOMOGENEITY — every edge file (live + archive) and revision file must
//     carry the same codec epoch. A chain mixing epochs is corruption: an
//     UNCONDITIONAL hard error naming both epochs and an offending edge (epochs
//     change only via the event-time procedure, never incrementally).
//  2. MERKLE CLOSURE — every object-id referenced by a revision manifest, and
//     every op payload id in every edge, must resolve in the object store
//     (chain.VerifyClosure over manifests; ParseOp already resolved op payloads
//     at load).
//  3. EDGE-ENDPOINT CONSISTENCY — simulating an edge's ops on its from-manifest
//     must reproduce its to-manifest (chain.VerifyEdgeEndpoint + the OpSimulator
//     here). DML/raw ops are manifest no-ops (amendment A2). This is class-aware:
//     a genesis edge's from-manifest is empty; other edges read their parent's
//     recorded revision manifest.
//
// OpSimulator SCOPE (a stated limitation): it faithfully simulates the
// WHOLE-TOP-LEVEL-OBJECT op families the 5.1 self-contained layer produces
// (create/drop/or_replace/alter of tables, views, matviews, sequences,
// composites, domains, functions), where the op's target manifest key and its
// payload's def-id correspond exactly to a manifest entry. NESTED-MODIFIER ops
// (create_trigger/create_policy/create_partition) change their owning table's
// encoded bytes but do NOT carry the resulting table id, so they are NOT
// manifest-representable at object granularity — the simulator returns a hard
// error for them rather than silently faking the endpoint. This is honest given
// the current op coverage; extending it waits on richer self-contained ops.

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/objstore"
)

// VerifyChainConsistency runs the three consistency checks over an on-disk chain
// project. It is the shared checker roadmap 6.2 and 7.2 invoke. A nil return
// means the store, revision manifests, and edges are mutually consistent.
func VerifyChainConsistency(p *ChainProject) error {
	live, err := p.LoadLiveEdges()
	if err != nil {
		return err
	}
	arch, err := p.LoadArchivedEdges()
	if err != nil {
		return err
	}
	all := append(append([]Edge{}, live...), arch...)

	// (1) epoch homogeneity across edge files. Edge files were already checked
	// against enc.CodecVersion at load (LoadEdge), so a heterogeneous chain cannot
	// even load fully; this re-states the invariant explicitly and names the
	// offender for the checker's contract.
	if err := verifyEpochHomogeneity(p, all); err != nil {
		return err
	}

	sim := opSimulator{store: p.store}
	for _, e := range all {
		if err := verifyEdgeConsistency(p, e, sim); err != nil {
			return err
		}
	}
	return nil
}

// verifyEpochHomogeneity asserts every edge carries enc.CodecVersion (the store's
// epoch). Mixed epochs are an unconditional hard error naming both epochs.
func verifyEpochHomogeneity(p *ChainProject, edges []Edge) error {
	want := p.store.Epoch()
	for _, e := range edges {
		// Re-read the raw codec from the file: LoadEdge already enforced equality,
		// but the checker's contract is to name the offender if they ever diverge.
		got, err := readEdgeCodec(e.File)
		if err != nil {
			return err
		}
		if uint32(got) != want {
			return fmt.Errorf("migrate: mixed-epoch chain: edge %s carries codec epoch %d but the store epoch is %d — corruption (epochs change only via the event-time procedure)", e.ID(), got, want)
		}
	}
	return nil
}

// verifyEdgeConsistency runs closure + endpoint for one edge. The from-manifest is
// empty for a genesis edge, else the parent revision's recorded manifest; the
// to-manifest is the target revision's recorded manifest.
func verifyEdgeConsistency(p *ChainProject, e Edge, sim opSimulator) error {
	to, err := p.ReadRevisionManifest(e.Target)
	if err != nil {
		return fmt.Errorf("migrate: consistency: edge %s to-manifest: %w", e.ID(), err)
	}
	if err := chain.VerifyClosure(to, p.store); err != nil {
		return fmt.Errorf("migrate: consistency: edge %s: %w", e.ID(), err)
	}
	var from chain.Manifest
	if e.IsGenesis() {
		from = chain.Manifest{}
	} else {
		from, err = p.ReadRevisionManifest(e.Parent)
		if err != nil {
			return fmt.Errorf("migrate: consistency: edge %s from-manifest: %w", e.ID(), err)
		}
		if err := chain.VerifyClosure(from, p.store); err != nil {
			return fmt.Errorf("migrate: consistency: edge %s: %w", e.ID(), err)
		}
	}
	// Class-awareness (handoff note): manifest-equal implies revision-equal only
	// same-class. The endpoint manifests are compared structurally, but the edge's
	// endpoints must share the edge's class (LoadEdge already enforced this).
	if !e.IsGenesis() && e.Parent.Class() != e.Class {
		return fmt.Errorf("migrate: consistency: edge %s crosses model classes (%s -> %s)", e.ID(), e.Parent.Class(), e.Target.Class())
	}
	if err := chain.VerifyEdgeEndpoint(e.chainEdge(), from, to, sim); err != nil {
		return fmt.Errorf("migrate: consistency: %w", err)
	}
	return nil
}

// opSimulator implements chain.OpSimulator over the self-contained op families.
type opSimulator struct{ store *objstore.Store }

// Simulate maps a from-manifest to a to-manifest by applying each op. Object
// creates/replaces set the target key to the op's def-id; drops remove it; DML/raw
// are no-ops; nested-modifier ops are a hard error (see the SCOPE note).
func (s opSimulator) Simulate(from chain.Manifest, ops []chain.Op) (chain.Manifest, error) {
	out := make(chain.Manifest, len(from))
	for k, id := range from {
		out[k] = id
	}
	for _, op := range ops {
		sc, ok := op.(SelfContainedOp)
		if !ok {
			return nil, fmt.Errorf("migrate: op simulator requires a SelfContainedOp, got %T", op)
		}
		if err := s.applyOp(out, sc); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// applyOp mutates the manifest for one op.
func (s opSimulator) applyOp(m chain.Manifest, op SelfContainedOp) error {
	// Pseudo-target (dml/raw) ops are manifest no-ops (A2).
	if op.target.Kind == enc.KindDML || op.target.Kind == enc.KindRaw {
		return nil
	}
	body, err := loadBody(s.store, op.payload)
	if err != nil {
		return err
	}
	switch op.kind {
	// Whole-top-level-object creates/replaces: set the target key to the def id.
	case "create_table", "create_view", "create_materialized_view",
		"create_sequence", "create_composite_type", "create_domain",
		"create_function", "create_or_replace_view", "create_or_replace_function",
		"alter_sequence":
		if body.DefID == "" {
			return fmt.Errorf("migrate: op simulator: %q payload has no def id", op.kind)
		}
		m[op.target] = body.DefID
		return nil
	// Whole-object drops: remove the key.
	case "drop_table", "drop_view", "drop_materialized_view", "drop_sequence",
		"drop_composite_type", "drop_domain", "drop_function":
		delete(m, op.target)
		return nil
	// Nested-modifier ops change their owning table's bytes but do not carry the
	// resulting table id — not manifest-representable at object granularity.
	case "create_trigger", "create_policy", "create_partition",
		"drop_trigger", "drop_policy":
		return fmt.Errorf("migrate: op simulator: op %q (target %s) modifies its owning object without carrying the resulting object id — endpoint simulation is not defined for nested-modifier ops at the current op coverage", op.kind, op.target.String())
	default:
		return fmt.Errorf("migrate: op simulator: unhandled op kind %q", op.kind)
	}
}

// ensure opSimulator satisfies the kernel interface.
var _ chain.OpSimulator = opSimulator{}
