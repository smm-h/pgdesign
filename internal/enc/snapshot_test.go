package enc

import (
	"bytes"
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/modelgen"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"pgregory.net/rapid"
)

// smSchema builds a canonical model containing one state-machine type whose
// "ship" transition carries the given comment, and (optionally) whose registry
// TypeDef.Source is relabeled. It goes through the real Build pipeline so the
// StateMachines collection is populated exactly as production produces it.
func smSchema(t *testing.T, transitionComment, source string) *model.Schema {
	reg := semtype.NewBuiltinRegistry()
	ut := semtype.UserTypeDef{
		Name: "order_status",
		Kind: "state_machine",
		States: []semtype.UserSMState{
			{Name: "pending"},
			{Name: "shipped"},
			{Name: "delivered", Terminal: true},
		},
		Transitions: []semtype.UserSMTransition{
			{Name: "ship", From: []string{"pending"}, To: "shipped", Comment: transitionComment},
			{Name: "deliver", From: []string{"shipped"}, To: "delivered"},
		},
		InitialState: "pending",
		Comment:      "order lifecycle",
	}
	if d := reg.LoadUserTypes([]semtype.UserTypeDef{ut}); d.HasErrors() {
		t.Fatalf("LoadUserTypes: %v", d.Errors())
	}
	if source != "" {
		td, err := reg.Resolve("order_status")
		if err != nil {
			t.Fatal(err)
		}
		td.Source = source
	}
	comment := "orders"
	raw := &parse.RawSchema{
		Meta:  parse.RawMeta{Schema: "public", Version: 16},
		Types: []parse.RawType{{Name: "order_status", Kind: "state_machine"}},
		Tables: []parse.RawTable{{
			Name:    "orders",
			Comment: &comment,
			PK:      []string{"id"},
			Columns: []parse.RawColumn{
				{Name: "id", Type: "id"},
				{Name: "status", Type: "order_status"},
			},
		}},
	}
	s, diags := model.Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("Build: %v", diags.Errors())
	}
	return s
}

// encodeSMObject encodes the schema's single state-machine object (KindSMType).
func encodeSMObject(t *testing.T, s *model.Schema) []byte {
	t.Helper()
	if len(s.StateMachines) != 1 {
		t.Fatalf("expected exactly 1 state machine, got %d", len(s.StateMachines))
	}
	b, err := EncodeStateMachine(s.StateMachines[0])
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestNestedTransitionCommentsFlipIdentity: a change to a nested SM transition
// comment changes the canonical bytes — now through the MODEL path (the
// first-class model.StateMachine object), not the registry snapshot. The verify
// block requires this.
func TestNestedTransitionCommentsFlipIdentity(t *testing.T) {
	testenv.Isolate(t)
	a := encodeSMObject(t, smSchema(t, "ship it", ""))
	b := encodeSMObject(t, smSchema(t, "dispatch it", ""))
	if bytes.Equal(a, b) {
		t.Fatalf("changing a nested transition comment did NOT flip the sm_type bytes:\n%s", a)
	}
}

// TestSourceRelabelingDoesNotFlipIdentity: TypeDef.Source has no model home, so
// relabeling it cannot change the canonical bytes of the state-machine object.
// The verify block requires this.
func TestSourceRelabelingDoesNotFlipIdentity(t *testing.T) {
	testenv.Isolate(t)
	a := encodeSMObject(t, smSchema(t, "ship it", "user"))
	b := encodeSMObject(t, smSchema(t, "ship it", "extended"))
	if !bytes.Equal(a, b) {
		t.Fatalf("relabeling Source flipped sm_type bytes (Source must have no model home):\n%s\n%s", a, b)
	}
}

// TestRegistrySnapshotEmptyForAllModels: the registry snapshot contributes
// nothing to identity for EVERY model — flat models AND state-machine-bearing
// ones. This is the unconditional escape-hatch invariant: all identity-bearing
// registry state now has a model home, so if this test ever found residue in
// the snapshot, that state would be added to the model, not to identity via the
// snapshot.
func TestRegistrySnapshotEmptyForAllModels(t *testing.T) {
	testenv.Isolate(t)
	// Flat models over the generator.
	rapid.Check(t, func(rt *rapid.T) {
		raws := modelgen.Draw(rt, modelgen.DefaultConfig())
		reg := semtype.NewBuiltinRegistry()
		for _, raw := range raws {
			if uts := parse.CollectUserTypes(raw); len(uts) > 0 {
				if d := reg.LoadUserTypes(uts); d.HasErrors() {
					rt.Fatalf("LoadUserTypes: %v", d.Errors())
				}
			}
		}
		if !RegistrySnapshotEmpty(reg) {
			b, _ := EncodeRegistrySnapshot(reg)
			rt.Fatalf("registry snapshot not empty for a flat model: %s", b)
		}
	})

	// A state-machine-bearing registry: the snapshot must STILL be empty, since
	// SM identity now flows through model.StateMachine (KindSMType), not the
	// snapshot.
	s := smSchema(t, "ship it", "user")
	reg := semtype.NewBuiltinRegistry()
	ut := semtype.UserTypeDef{
		Name:         "order_status",
		Kind:         "state_machine",
		States:       []semtype.UserSMState{{Name: "pending"}, {Name: "shipped", Terminal: true}},
		Transitions:  []semtype.UserSMTransition{{Name: "ship", From: []string{"pending"}, To: "shipped"}},
		InitialState: "pending",
	}
	if d := reg.LoadUserTypes([]semtype.UserTypeDef{ut}); d.HasErrors() {
		t.Fatalf("LoadUserTypes: %v", d.Errors())
	}
	if !RegistrySnapshotEmpty(reg) {
		b, _ := EncodeRegistrySnapshot(reg)
		t.Fatalf("registry snapshot not empty for an SM-bearing registry: %s", b)
	}
	// And the SM identity IS present in the model.
	if len(s.StateMachines) != 1 {
		t.Fatalf("SM identity missing from the model: StateMachines=%d", len(s.StateMachines))
	}
}

// TestStateMachineDecodeRoundTrip: an SM-bearing schema survives
// EncodeObjects -> DecodeObjects -> re-EncodeObjects byte-identically, and the
// sm_type object is a first-class manifest entry. This is decode-totality for
// SM-bearing schemas — the property that was broken when SM identity flowed
// through the un-decodable registry snapshot.
func TestStateMachineDecodeRoundTrip(t *testing.T) {
	testenv.Isolate(t)
	s := smSchema(t, "ship it", "user")
	objs1, err := EncodeObjects(s)
	if err != nil {
		t.Fatalf("EncodeObjects: %v", err)
	}
	if _, ok := objs1[KeyForStateMachine(s.StateMachines[0])]; !ok {
		t.Fatalf("no sm_type manifest key was produced; keys: %v", keysOf(objs1))
	}
	decoded, err := DecodeObjects(objs1)
	if err != nil {
		t.Fatalf("DecodeObjects: %v", err)
	}
	if len(decoded.StateMachines) != 1 {
		t.Fatalf("state machine lost on decode: got %d", len(decoded.StateMachines))
	}
	objs2, err := EncodeObjects(decoded)
	if err != nil {
		t.Fatalf("re-EncodeObjects: %v", err)
	}
	if len(objs1) != len(objs2) {
		t.Fatalf("object count changed on round-trip: %d -> %d", len(objs1), len(objs2))
	}
	for k, b1 := range objs1 {
		b2, ok := objs2[k]
		if !ok {
			t.Fatalf("key %s lost on round-trip", k)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("key %s bytes differ on round-trip:\n%s\n%s", k, b1, b2)
		}
	}
}

func keysOf(objs map[Key][]byte) []string {
	out := make([]string, 0, len(objs))
	for k := range objs {
		out = append(out, k.String())
	}
	return out
}

// TestBuiltinRegexChangeFlipsIdentity: the builtin email type carries a CHECK
// regex; when used by a column it materializes as a model Domain. A change to
// the builtin regex therefore flips the domain's canonical bytes — identity
// tracks builtin changes through the MODEL collection, with no special case.
func TestBuiltinRegexChangeFlipsIdentity(t *testing.T) {
	testenv.Isolate(t)
	comment := "a user"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public", Version: 16},
		Tables: []parse.RawTable{{
			Name:    "users",
			Comment: &comment,
			PK:      []string{"id"},
			Columns: []parse.RawColumn{
				{Name: "id", Type: "id"},
				{Name: "email", Type: "email"},
			},
		}},
	}

	encodeEmailDomain := func(mutateRegex bool) []byte {
		reg := semtype.NewBuiltinRegistry()
		if mutateRegex {
			td, err := reg.Resolve("email")
			if err != nil {
				t.Fatal(err)
			}
			td.Check = "VALUE ~ '^changed@regex$'"
		}
		s, diags := model.Build(raw, reg)
		if diags.HasErrors() {
			t.Fatalf("Build: %v", diags.Errors())
		}
		var emailDomain *model.Domain
		for i := range s.Domains {
			if s.Domains[i].Name == "email" {
				emailDomain = &s.Domains[i]
			}
		}
		if emailDomain == nil {
			t.Fatalf("email domain did not materialize; domains: %+v", s.Domains)
		}
		b, err := EncodeDomain(*emailDomain)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	original := encodeEmailDomain(false)
	mutated := encodeEmailDomain(true)
	if bytes.Equal(original, mutated) {
		t.Fatalf("changing the builtin email regex did NOT flip identity:\n%s", original)
	}
}
