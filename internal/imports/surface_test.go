package imports

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

func col(name, base string) model.Column {
	return model.Column{Name: name, PGType: typeinfo.Type{Base: base}, NotNull: true}
}

// frameworkModel is a mini framework schema: a users table (with an enum-typed
// column) plus the enum type and an unreferenced audit_log table.
func frameworkModel() *model.Schema {
	users := model.Table{
		Schema:  "public",
		Name:    "users",
		Comment: "framework users",
		Columns: []model.Column{
			col("id", "int8"),
			{Name: "status", PGType: typeinfo.Type{Base: "user_status"}, NotNull: true},
			{Name: "created_at", PGType: typeinfo.Type{Base: "timestamptz"}, Default: strptr("now()")},
		},
		PK: []string{"id"},
	}
	audit := model.Table{
		Schema:  "public",
		Name:    "audit_log",
		Comment: "unreferenced",
		Columns: []model.Column{col("id", "int8"), col("note", "text")},
		PK:      []string{"id"},
	}
	return &model.Schema{
		PGVersion:  15,
		Extensions: []string{"pgcrypto"},
		Tables:     []model.Table{users, audit},
		Enums:      []model.Enum{{Schema: "public", Name: "user_status", Values: []string{"active", "banned"}, Comment: "status"}},
	}
}

func strptr(s string) *string { return &s }

func TestExtractSurface_ReferencedTablesAndTypeClosure(t *testing.T) {
	testenv.Isolate(t)
	fw := frameworkModel()
	surface, err := ExtractSurface(fw, []string{"users"}, "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.Tables) != 1 || surface.Tables[0].Name != "users" {
		t.Fatalf("expected only users in surface, got %+v", surface.Tables)
	}
	if surface.Tables[0].Schema != "app" {
		t.Errorf("surface table not re-stamped to target schema: %q", surface.Tables[0].Schema)
	}
	// The enum used by users.status must be pulled into the closure and re-stamped.
	if len(surface.Enums) != 1 || surface.Enums[0].Name != "user_status" {
		t.Fatalf("expected user_status enum in closure, got %+v", surface.Enums)
	}
	if surface.Enums[0].Schema != "app" {
		t.Errorf("surface enum not re-stamped: %q", surface.Enums[0].Schema)
	}
	// audit_log is unreferenced and must NOT appear.
	for _, tb := range surface.Tables {
		if tb.Name == "audit_log" {
			t.Fatal("unreferenced audit_log leaked into surface")
		}
	}
}

func TestExtractSurface_MissingTableIsError(t *testing.T) {
	testenv.Isolate(t)
	fw := frameworkModel()
	if _, err := ExtractSurface(fw, []string{"nonexistent"}, "app"); err == nil {
		t.Fatal("expected error for referenced table not in framework")
	}
}

func TestVendor_PerObjectIDsStable(t *testing.T) {
	testenv.Isolate(t)
	fw := frameworkModel()
	surface, _ := ExtractSurface(fw, []string{"users"}, "app")

	e1, sh1, sem1, err := Vendor(surface, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e2, sh2, sem2, err := Vendor(surface, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if sh1 != sh2 || sem1 != sem2 {
		t.Errorf("hashes not stable across re-vendor: surface %s/%s semantic %s/%s", sh1, sh2, sem1, sem2)
	}
	if len(e1) != len(e2) {
		t.Fatalf("object count differs: %d vs %d", len(e1), len(e2))
	}
	for i := range e1 {
		if e1[i].Key != e2[i].Key || e1[i].ID != e2[i].ID {
			t.Errorf("object %d differs: %+v vs %+v", i, e1[i], e2[i])
		}
	}
}

// TestVendor_SemanticHashIgnoresEquivalentDefault: a surface whose only change is
// an equivalently-spelled default keeps the SAME semantic hash (N folds the
// redundant parens) while the raw-id surface hash changes. This is exactly why
// the drift channel uses N, not surface ids.
func TestVendor_SemanticHashIgnoresEquivalentDefault(t *testing.T) {
	testenv.Isolate(t)
	fw := frameworkModel()
	base, _ := ExtractSurface(fw, []string{"users"}, "app")

	// Re-spell the created_at default equivalently: "now()" -> "(now())".
	fw2 := frameworkModel()
	for i := range fw2.Tables {
		if fw2.Tables[i].Name == "users" {
			for j := range fw2.Tables[i].Columns {
				if fw2.Tables[i].Columns[j].Name == "created_at" {
					fw2.Tables[i].Columns[j].Default = strptr("(now())")
				}
			}
		}
	}
	respelled, _ := ExtractSurface(fw2, []string{"users"}, "app")

	_, shA, semA, _ := Vendor(base, t.TempDir())
	_, shB, semB, _ := Vendor(respelled, t.TempDir())

	if semA != semB {
		t.Errorf("semantic hash changed on equivalently-spelled default: %s vs %s", semA, semB)
	}
	if shA == shB {
		t.Errorf("surface (raw-id) hash unexpectedly stable across default re-spell; expected it to differ")
	}
}

// TestVendor_SemanticHashChangesOnTypeChange: a real column type change DOES move
// the semantic hash.
func TestVendor_SemanticHashChangesOnTypeChange(t *testing.T) {
	testenv.Isolate(t)
	fw := frameworkModel()
	base, _ := ExtractSurface(fw, []string{"users"}, "app")

	fw2 := frameworkModel()
	for i := range fw2.Tables {
		if fw2.Tables[i].Name == "users" {
			for j := range fw2.Tables[i].Columns {
				if fw2.Tables[i].Columns[j].Name == "id" {
					fw2.Tables[i].Columns[j].PGType = typeinfo.Type{Base: "int4"}
				}
			}
		}
	}
	changed, _ := ExtractSurface(fw2, []string{"users"}, "app")

	_, _, semA, _ := Vendor(base, t.TempDir())
	_, _, semB, _ := Vendor(changed, t.TempDir())
	if semA == semB {
		t.Error("semantic hash did not change on column type change")
	}
}
