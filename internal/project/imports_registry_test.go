package project

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/semtype"
)

// TestRegisterImportedTypes_Collision verifies an imported type name that collides
// with a local (non-builtin) type is a hard error (E243).
func TestRegisterImportedTypes_Collision(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	// Local enum "status".
	localDiags := reg.LoadUserTypes([]semtype.UserTypeDef{{Name: "status", Kind: "enum", Values: []string{"a", "b"}}})
	if localDiags.HasErrors() {
		t.Fatalf("loading local type failed: %v", localDiags)
	}
	surface := &model.Schema{
		Enums: []model.Enum{{Name: "status", Schema: "app", Values: []string{"x", "y"}}},
	}
	diags := RegisterImportedTypes(reg, surface)
	found := false
	for _, d := range diags {
		if d.Code == "E243" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected E243 collision, got: %v", diags)
	}
}

// TestRegisterImportedTypes_EnumUsable verifies a non-colliding imported enum is
// registered so local columns can resolve it.
func TestRegisterImportedTypes_EnumUsable(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	surface := &model.Schema{
		Enums: []model.Enum{{Name: "framework_role", Schema: "app", Values: []string{"admin", "user"}}},
	}
	diags := RegisterImportedTypes(reg, surface)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors registering imported enum: %v", diags)
	}
	if _, err := reg.Resolve("framework_role"); err != nil {
		t.Fatalf("imported enum not usable in local columns: %v", err)
	}
}
