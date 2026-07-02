package parse

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/semtype"
)

func TestCollectUserTypes_Empty(t *testing.T) {
	raw := &RawSchema{}
	got := CollectUserTypes(raw)
	if len(got) != 0 {
		t.Fatalf("expected 0 user types for empty schema, got %d", len(got))
	}
}

func TestCollectUserTypes_Scalar(t *testing.T) {
	notNull := true
	defVal := "0"
	defExpr := "now()"
	check := "VALUE > 0"
	unique := true
	arr := false
	comment := "a scalar"

	raw := &RawSchema{
		Types: []RawType{{
			Name:        "positive_int",
			Kind:        "scalar",
			BaseType:    "integer",
			NotNull:     &notNull,
			Default:     &defVal,
			DefaultExpr: &defExpr,
			Check:       &check,
			Unique:      &unique,
			Array:       &arr,
			Comment:     &comment,
		}},
	}

	got := CollectUserTypes(raw)
	if len(got) != 1 {
		t.Fatalf("expected 1 user type, got %d", len(got))
	}

	ut := got[0]
	if ut.Name != "positive_int" {
		t.Errorf("Name = %q, want %q", ut.Name, "positive_int")
	}
	if ut.Kind != "scalar" {
		t.Errorf("Kind = %q, want %q", ut.Kind, "scalar")
	}
	if ut.Base != "integer" {
		t.Errorf("Base = %q, want %q", ut.Base, "integer")
	}
	if ut.NotNull == nil || !*ut.NotNull {
		t.Error("NotNull should be true")
	}
	if ut.Default == nil || *ut.Default != "0" {
		t.Error("Default should be '0'")
	}
	if ut.DefaultExpr != "now()" {
		t.Errorf("DefaultExpr = %q, want %q", ut.DefaultExpr, "now()")
	}
	if ut.Check != "VALUE > 0" {
		t.Errorf("Check = %q, want %q", ut.Check, "VALUE > 0")
	}
	if !ut.Unique {
		t.Error("Unique should be true")
	}
	if ut.Array {
		t.Error("Array should be false")
	}
	if ut.Comment != "a scalar" {
		t.Errorf("Comment = %q, want %q", ut.Comment, "a scalar")
	}
}

func TestCollectUserTypes_Enum(t *testing.T) {
	raw := &RawSchema{
		Types: []RawType{{
			Name:   "status",
			Kind:   "enum",
			Values: []string{"active", "inactive", "pending"},
		}},
	}

	got := CollectUserTypes(raw)
	if len(got) != 1 {
		t.Fatalf("expected 1 user type, got %d", len(got))
	}

	ut := got[0]
	if ut.Kind != "enum" {
		t.Errorf("Kind = %q, want %q", ut.Kind, "enum")
	}
	if len(ut.Values) != 3 {
		t.Fatalf("expected 3 enum values, got %d", len(ut.Values))
	}
	expected := []string{"active", "inactive", "pending"}
	for i, v := range expected {
		if ut.Values[i] != v {
			t.Errorf("Values[%d] = %q, want %q", i, ut.Values[i], v)
		}
	}
}

func TestCollectUserTypes_Composite(t *testing.T) {
	raw := &RawSchema{
		Types: []RawType{{
			Name: "address",
			Kind: "composite",
			Fields: []RawCompositeField{
				{Name: "street", Type: "text"},
				{Name: "city", Type: "text"},
				{Name: "zip", Type: "varchar"},
			},
		}},
	}

	got := CollectUserTypes(raw)
	if len(got) != 1 {
		t.Fatalf("expected 1 user type, got %d", len(got))
	}

	ut := got[0]
	if ut.Kind != "composite" {
		t.Errorf("Kind = %q, want %q", ut.Kind, "composite")
	}
	expectedFields := []semtype.CompositeField{
		{Name: "street", PGType: "text"},
		{Name: "city", PGType: "text"},
		{Name: "zip", PGType: "varchar"},
	}
	if len(ut.Fields) != len(expectedFields) {
		t.Fatalf("expected %d fields, got %d", len(expectedFields), len(ut.Fields))
	}
	for i, f := range expectedFields {
		if ut.Fields[i] != f {
			t.Errorf("Fields[%d] = %+v, want %+v", i, ut.Fields[i], f)
		}
	}
}

func TestCollectUserTypes_StateMachine(t *testing.T) {
	terminal := true
	nonTerminal := false
	stateComment := "the final state"
	transComment := "move to done"
	initial := "draft"
	enforce := true

	raw := &RawSchema{
		Types: []RawType{{
			Name: "ticket_status",
			Kind: "state_machine",
			States: []RawSMState{
				{Name: "draft", Terminal: &nonTerminal},
				{Name: "active", Terminal: &nonTerminal},
				{Name: "done", Terminal: &terminal, Comment: &stateComment},
			},
			Transitions: []RawSMTransition{
				{Name: "activate", From: []string{"draft"}, To: "active"},
				{
					Name:     "complete",
					From:     []string{"active"},
					To:       "done",
					Requires: map[string]string{"reviewer": "text"},
					Comment:  &transComment,
				},
			},
			InitialState:   &initial,
			EnforceTrigger: &enforce,
		}},
	}

	got := CollectUserTypes(raw)
	if len(got) != 1 {
		t.Fatalf("expected 1 user type, got %d", len(got))
	}

	ut := got[0]
	if ut.Kind != "state_machine" {
		t.Errorf("Kind = %q, want %q", ut.Kind, "state_machine")
	}
	if ut.InitialState != "draft" {
		t.Errorf("InitialState = %q, want %q", ut.InitialState, "draft")
	}
	if ut.EnforceTrigger == nil || !*ut.EnforceTrigger {
		t.Error("EnforceTrigger should be true")
	}

	// States: declaration order must be preserved (it is the enum value order).
	if len(ut.States) != 3 {
		t.Fatalf("expected 3 states, got %d", len(ut.States))
	}
	for i, want := range []string{"draft", "active", "done"} {
		if ut.States[i].Name != want {
			t.Fatalf("States[%d].Name = %q, want %q (declaration order)", i, ut.States[i].Name, want)
		}
	}
	if s := ut.States[2]; !s.Terminal {
		t.Error("state 'done' should be terminal")
	} else if s.Comment != "the final state" {
		t.Errorf("state 'done' Comment = %q, want %q", s.Comment, "the final state")
	}
	if ut.States[0].Terminal {
		t.Error("state 'draft' should not be terminal")
	}

	// Transitions
	if len(ut.Transitions) != 2 {
		t.Fatalf("expected 2 transitions, got %d", len(ut.Transitions))
	}
	tr0 := ut.Transitions[0]
	if tr0.Name != "activate" {
		t.Errorf("Transitions[0].Name = %q, want %q", tr0.Name, "activate")
	}
	if len(tr0.From) != 1 || tr0.From[0] != "draft" {
		t.Errorf("Transitions[0].From = %v, want [draft]", tr0.From)
	}
	if tr0.To != "active" {
		t.Errorf("Transitions[0].To = %q, want %q", tr0.To, "active")
	}

	tr1 := ut.Transitions[1]
	if tr1.Name != "complete" {
		t.Errorf("Transitions[1].Name = %q, want %q", tr1.Name, "complete")
	}
	if tr1.Comment != "move to done" {
		t.Errorf("Transitions[1].Comment = %q, want %q", tr1.Comment, "move to done")
	}
	if tr1.Requires == nil || tr1.Requires["reviewer"] != "text" {
		t.Errorf("Transitions[1].Requires = %v, want map[reviewer:text]", tr1.Requires)
	}
}

func TestCollectUserTypes_Multiple(t *testing.T) {
	raw := &RawSchema{
		Types: []RawType{
			{Name: "a", Kind: "enum", Values: []string{"x"}},
			{Name: "b", Kind: "scalar", BaseType: "text"},
		},
	}

	got := CollectUserTypes(raw)
	if len(got) != 2 {
		t.Fatalf("expected 2 user types, got %d", len(got))
	}
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("unexpected names: %q, %q", got[0].Name, got[1].Name)
	}
}
