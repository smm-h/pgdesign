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
// OpSimulator is TOTAL (roadmap 5.1b): it simulates EVERY self-contained op
// kind. WHOLE-OBJECT creates/replaces set the target key to the payload's def
// id; whole-object drops remove the key; NESTED-MODIFIER ops (columns, checks,
// FKs, indexes, triggers, policies, rls, enum/domain modifiers) map the OWNING
// object's key to the payload's POST-STATE def id (the amendment's adopted
// resolution: the op carries the owner's post-state id, so endpoint simulation
// is a key->id assignment, no IR-application engine); a rename_table deletes the
// old key and inserts the new; a schema-meta op maps the schema key to the
// post-state schema-meta form id; dml/raw pseudo-targets and refresh are
// manifest no-ops. An op kind outside the inventory is a hard error, never a
// silent fake.

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

// Simulate maps a from-manifest to a to-manifest by applying each op. It is
// TOTAL over the self-contained inventory (roadmap 5.1b): whole-object
// creates/replaces set the target key to the op's def-id; whole-object drops
// remove it; nested-modifier ops map the owning key to the payload's post-state
// id; rename_table swaps keys; schema-meta maps the schema key; DML/raw
// pseudo-targets and refresh are no-ops. An op kind outside the inventory is a
// hard error, never a silent fake.
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

// applyOp mutates the manifest for one op, per its simulation category.
func (s opSimulator) applyOp(m chain.Manifest, op SelfContainedOp) error {
	// Pseudo-target (dml/raw) ops are manifest no-ops (A2).
	if op.target.Kind == enc.KindDML || op.target.Kind == enc.KindRaw {
		return nil
	}
	cat, ok := categoryForKind(op.kind)
	if !ok {
		return fmt.Errorf("migrate: op simulator: op kind %q is not in the self-contained inventory", op.kind)
	}
	body, err := loadBody(s.store, op.payload)
	if err != nil {
		return err
	}
	switch cat {
	case catWholeCreate, catSchemaMeta:
		if body.DefID == "" {
			return fmt.Errorf("migrate: op simulator: %q payload has no def id", op.kind)
		}
		m[op.target] = body.DefID
		return nil

	case catWholeDrop:
		delete(m, op.target)
		return nil

	case catNestedModifier:
		// The owning object's post-state id is the amendment's carried delta. When
		// it is absent (the owner is dropped later in the same edge), leave the key
		// untouched — a subsequent drop removes it and the endpoint still matches.
		if body.PostDefID != "" {
			m[op.target] = body.PostDefID
		}
		return nil

	case catRenameTable:
		if body.OldTable != "" {
			schema, name := splitQualifiedName(body.OldTable)
			delete(m, enc.Key{Kind: enc.KindTable, Schema: schema, Name: name})
		}
		if body.PostDefID != "" {
			m[op.target] = body.PostDefID
		}
		return nil

	case catManifestNoop, catPseudo:
		return nil

	default:
		return fmt.Errorf("migrate: op simulator: unhandled category for op kind %q", op.kind)
	}
}

// ensure opSimulator satisfies the kernel interface.
var _ chain.OpSimulator = opSimulator{}
