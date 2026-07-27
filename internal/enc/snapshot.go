package enc

import "github.com/smm-h/pgdesign/internal/semtype"

// The registry snapshot serializes the semtype registry residue: the type
// information that has NO representation in the model-level collections.
//
// Type identity flows through the MODEL collections wherever it can: enum
// values land in model.Enums, composite fields in model.CompositeTypes, and
// builtin-derived scalar CHECKs materialize into model.Domains (so a change to
// the builtin email regex flips identity via Domain.Check with no special
// case). What remains WITHOUT a model home is the state-machine TRANSITION
// GRAPH — its transitions, per-transition Requires and comments, initial state,
// and enforce-trigger flag. The schema-side model.Schema.StateMachineTransitions
// is a derived duplicate that 1.5 excludes and that carries no transition
// comments at all, so the transition residue is snapshotted here.
//
// Consequently the snapshot is EMPTY (no state machines) for models built from
// the increment-A generator and for any type-free schema — verified by
// TestRegistrySnapshotEmptyForFlatModels. If a future check ever finds
// semantic registry state that is missing from the model collections, the fix
// is to add that state to the model, not to widen identity through this
// snapshot.
//
// Field policy: semantic content plus ALL comments; TypeDef.Source is EXCLUDED
// so relabeling a type's Source does not flip identity.

type smStateForm struct {
	Name     string `json:"name"`
	Terminal bool   `json:"terminal,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

type smTransitionForm struct {
	Name     string            `json:"name"`
	From     []string          `json:"from"`
	To       string            `json:"to"`
	Requires map[string]string `json:"requires,omitempty"`
	Comment  string            `json:"comment,omitempty"`
}

type smTypeForm struct {
	Name           string             `json:"name"`
	States         []smStateForm      `json:"states"`
	Transitions    []smTransitionForm `json:"transitions,omitempty"`
	InitialState   string             `json:"initial_state"`
	EnforceTrigger bool               `json:"enforce_trigger,omitempty"`
	Comment        string             `json:"comment,omitempty"`
}

type registrySnapshotForm struct {
	Codec         int          `json:"codec"`
	Kind          Kind         `json:"kind"`
	StateMachines []smTypeForm `json:"state_machines"`
}

func registrySnapshotToForm(reg *semtype.Registry) registrySnapshotForm {
	form := registrySnapshotForm{Codec: CodecVersion, Kind: KindRegistrySnap, StateMachines: []smTypeForm{}}
	if reg == nil {
		return form
	}
	// StateMachineTypes returns SM TypeDefs sorted by name — a canonical
	// collection order.
	for _, td := range reg.StateMachineTypes() {
		states := make([]smStateForm, len(td.States))
		for i, s := range td.States {
			// State order is SEMANTIC (it becomes the enum label order).
			states[i] = smStateForm{Name: s.Name, Terminal: s.Terminal, Comment: s.Comment}
		}
		trs := make([]smTransitionForm, len(td.Transitions))
		for i, tr := range td.Transitions {
			// From is a SET; sort. Requires map keys are sorted by encoding/json.
			trs[i] = smTransitionForm{
				Name:     tr.Name,
				From:     sortedCopy(tr.From),
				To:       tr.To,
				Requires: tr.Requires,
				Comment:  tr.Comment,
			}
		}
		form.StateMachines = append(form.StateMachines, smTypeForm{
			Name:           td.Name,
			States:         states,
			Transitions:    trs,
			InitialState:   td.InitialState,
			EnforceTrigger: td.EnforceTrigger,
			Comment:        td.Comment,
		})
	}
	return form
}

// EncodeRegistrySnapshot returns the canonical bytes for the semtype registry
// residue (see the package comment above). For a type-free or state-machine-free
// registry the snapshot's state_machines list is empty.
func EncodeRegistrySnapshot(reg *semtype.Registry) ([]byte, error) {
	return canonicalJSON(registrySnapshotToForm(reg))
}

// RegistrySnapshotEmpty reports whether the registry snapshot contributes
// nothing to identity (no state-machine residue). This is the predicate
// TestRegistrySnapshotEmptyForFlatModels asserts over generated models.
func RegistrySnapshotEmpty(reg *semtype.Registry) bool {
	return len(registrySnapshotToForm(reg).StateMachines) == 0
}
