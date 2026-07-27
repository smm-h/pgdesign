package enc

import (
	"bytes"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/modelgen"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/typeinfo"
	"pgregory.net/rapid"
)

// smRegistry builds a registry containing one state-machine TypeDef with a
// per-transition comment, so the snapshot has SM residue to encode.
func smRegistry(transitionComment, source string) *semtype.Registry {
	reg := semtype.NewRegistry()
	td := &semtype.TypeDef{
		Name:     "order_status",
		Kind:     semtype.KindStateMachine,
		BaseType: typeinfo.Type{Base: "order_status"},
		States: []semtype.SMStateDef{
			{Name: "pending"},
			{Name: "shipped"},
			{Name: "delivered", Terminal: true},
		},
		Transitions: []semtype.SMTransitionDef{
			{Name: "ship", From: []string{"pending"}, To: "shipped", Comment: transitionComment},
			{Name: "deliver", From: []string{"shipped"}, To: "delivered"},
		},
		InitialState:   "pending",
		EnforceTrigger: true,
		Comment:        "order lifecycle",
		Source:         source,
	}
	if err := reg.Register(td); err != nil {
		panic(err)
	}
	return reg
}

// TestNestedTransitionCommentsFlipIdentity: a change to a nested SM transition
// comment changes the registry-snapshot bytes. The verify block requires this.
func TestNestedTransitionCommentsFlipIdentity(t *testing.T) {
	a, err := EncodeRegistrySnapshot(smRegistry("ship it", "user"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeRegistrySnapshot(smRegistry("dispatch it", "user"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatalf("changing a nested transition comment did NOT flip the snapshot bytes:\n%s", a)
	}
}

// TestSourceRelabelingDoesNotFlipIdentity: TypeDef.Source is excluded, so
// relabeling it leaves the snapshot bytes unchanged. The verify block requires
// this.
func TestSourceRelabelingDoesNotFlipIdentity(t *testing.T) {
	a, err := EncodeRegistrySnapshot(smRegistry("ship it", "user"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeRegistrySnapshot(smRegistry("ship it", "extended"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("relabeling Source flipped snapshot bytes (Source must be excluded):\n%s\n%s", a, b)
	}
}

// TestRegistrySnapshotEmptyForFlatModels: over the increment-A generator, the
// registry has no state-machine types, so the snapshot contributes nothing to
// identity. This is the verification test the roadmap requires: if it ever
// found semantic registry state missing from the model collections, that state
// would be added to the model, not to identity via the snapshot.
func TestRegistrySnapshotEmptyForFlatModels(t *testing.T) {
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
}

// TestBuiltinRegexChangeFlipsIdentity: the builtin email type carries a CHECK
// regex; when used by a column it materializes as a model Domain. A change to
// the builtin regex therefore flips the domain's canonical bytes — identity
// tracks builtin changes through the MODEL collection, with no special case.
func TestBuiltinRegexChangeFlipsIdentity(t *testing.T) {
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
