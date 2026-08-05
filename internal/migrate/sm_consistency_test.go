package migrate

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
)

// smTableDesired returns twoTableDesired plus a state-machine type. The SM type
// materializes a first-class KindSMType manifest object; endpoint simulation must
// have an op that produces the sm_type manifest key or VerifyChainConsistency
// fails (the KindSMType consistency gap, roadmap 5.10 rider).
func smTableDesired() *model.Schema {
	s := twoTableDesired()
	s.StateMachines = []model.StateMachine{{
		Name:         "ticket_state",
		Schema:       "public",
		States:       []model.SMState{{Name: "open"}, {Name: "closed"}},
		Transitions:  []model.SMTransition{{Name: "close", From: []string{"open"}, To: "closed"}},
		InitialState: "open",
	}}
	s.Canonicalize()
	return s
}

// TestChainConsistencyWithStateMachine pins the rider: a genesis edge whose
// desired model carries a state-machine type must be endpoint-consistent — the
// sm_type manifest key is produced by a lowered sm_type op.
func TestChainConsistencyWithStateMachine(t *testing.T) {
	testenv.Isolate(t)
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := smTableDesired()
	d := diff.Diff(desired, &model.Schema{Name: desired.Name, PGVersion: desired.PGVersion})
	if d.IsEmpty() {
		t.Fatal("diff against empty must be non-empty")
	}
	m, _ := GenerateMigration(d, desired, "0.1.0", extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, m, desired, nil, rev.Revision{}, rev.RegistryPresent, "genesis-sm"); err != nil {
		t.Fatalf("GenerateEdge: %v", err)
	}
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("VerifyChainConsistency on an SM-bearing genesis edge: %v", err)
	}
}

// TestChainConsistencyWithStateMachineChange pins the change path: an edge that
// modifies an existing SM type's identity (a new transition) lowers to a
// create_sm_type op that re-maps the sm_type key, keeping the edge consistent.
func TestChainConsistencyWithStateMachineChange(t *testing.T) {
	testenv.Isolate(t)
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prev := smTableDesired()
	pd := diff.Diff(prev, &model.Schema{Name: prev.Name, PGVersion: prev.PGVersion})
	pm, _ := GenerateMigration(pd, prev, "0.1.0", extregistry.NewBuiltinRegistry())
	parentRev, err := rev.Compute(prev, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateEdge(p, pm, prev, nil, rev.Revision{}, rev.RegistryPresent, "genesis-sm"); err != nil {
		t.Fatalf("genesis GenerateEdge: %v", err)
	}

	// Change the SM: add a reopen transition (identity-affecting change).
	desired := smTableDesired()
	desired.StateMachines[0].Transitions = append(desired.StateMachines[0].Transitions,
		model.SMTransition{Name: "reopen", From: []string{"closed"}, To: "open"})
	desired.Canonicalize()

	d := diff.Diff(desired, prev)
	if len(d.StateMachinesChanged) != 1 {
		t.Fatalf("expected 1 StateMachinesChanged, got %v", d.StateMachinesChanged)
	}
	m, _ := GenerateMigration(d, desired, "0.2.0", extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, m, desired, prev, parentRev, rev.RegistryPresent, "sm-change"); err != nil {
		t.Fatalf("change GenerateEdge: %v", err)
	}
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("VerifyChainConsistency after SM change: %v", err)
	}
}

// TestDiffStateMachinesFields pins the diff-level added/removed/changed reporting.
func TestDiffStateMachinesFields(t *testing.T) {
	testenv.Isolate(t)
	withSM := smTableDesired()
	without := twoTableDesired()

	added := diff.Diff(withSM, without)
	if len(added.StateMachinesAdded) != 1 || added.StateMachinesAdded[0] != "public.ticket_state" {
		t.Fatalf("added: got %v", added.StateMachinesAdded)
	}
	removed := diff.Diff(without, withSM)
	if len(removed.StateMachinesRemoved) != 1 || removed.StateMachinesRemoved[0] != "public.ticket_state" {
		t.Fatalf("removed: got %v", removed.StateMachinesRemoved)
	}
	same := diff.Diff(smTableDesired(), smTableDesired())
	if len(same.StateMachinesChanged) != 0 {
		t.Fatalf("identical SM must not report a change, got %v", same.StateMachinesChanged)
	}
}
