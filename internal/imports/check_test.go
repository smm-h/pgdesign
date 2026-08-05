package imports

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

func diagHasCode(diags diagnostic.Diagnostics, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// lockAlias vendors the framework surface for the given referenced tables into a
// fresh project dir and writes the lockfile, returning the project dir.
func lockAlias(t *testing.T, refTables []string) string {
	t.Helper()
	projectDir := t.TempDir()
	fw := frameworkModel()
	surface, err := ExtractSurface(fw, refTables, "app")
	if err != nil {
		t.Fatal(err)
	}
	entries, sh, sem, err := Vendor(surface, AliasDir(projectDir, "framework"))
	if err != nil {
		t.Fatal(err)
	}
	exts, pgv := InferRequirements(fw)
	lf := &Lockfile{
		Alias: "framework", URL: "file:///fw", Ref: "v1", Commit: "abc",
		Schema: "app", SurfaceHash: sh, SemanticHash: sem,
		PGVersion: pgv, Extensions: exts, Objects: entries,
	}
	if err := WriteLockfile(projectDir, lf); err != nil {
		t.Fatal(err)
	}
	return projectDir
}

// consumerWithFK builds a consumer model whose orders table has an FK through
// alias "framework" to users, with the given local column type and ref columns.
func consumerWithFK(localType string, refColumns []string) *model.Schema {
	orders := model.Table{
		Schema:  "public",
		Name:    "orders",
		Comment: "consumer orders",
		Columns: []model.Column{
			col("id", "int8"),
			{Name: "user_id", PGType: typeinfo.Type{Base: localType}, NotNull: true},
		},
		PK: []string{"id"},
		FKs: []model.FK{{
			Name: "fk_orders_user", Columns: []string{"user_id"},
			RefSchema: "app", RefTable: "users", RefAlias: "framework",
			RefColumns: refColumns, OnDelete: "CASCADE",
		}},
	}
	return &model.Schema{Tables: []model.Table{orders}}
}

func TestCheck_CleanNoDrift(t *testing.T) {
	testenv.Isolate(t)
	projectDir := lockAlias(t, []string{"users"})
	consumer := consumerWithFK("int8", []string{"id"})
	diags := Check(projectDir, "framework", consumer)
	if diags.HasErrors() {
		t.Fatalf("expected no drift, got: %v", diags)
	}
}

func TestCheck_DriftedColumnType(t *testing.T) {
	testenv.Isolate(t)
	projectDir := lockAlias(t, []string{"users"})
	// Local user_id is int4 but the imported users.id is int8 -> junction drift.
	consumer := consumerWithFK("int4", []string{"id"})
	diags := Check(projectDir, "framework", consumer)
	if !diagHasCode(diags, "E237") {
		t.Fatalf("expected E237 junction type drift, got: %v", diags)
	}
	// The error must name the exact local column and FK.
	found := false
	for _, d := range diags {
		if d.Code == "E237" && d.Column == "user_id" && d.Table == "orders" {
			found = true
		}
	}
	if !found {
		t.Errorf("E237 did not pinpoint orders.user_id: %v", diags)
	}
}

func TestCheck_MissingReferencedColumn(t *testing.T) {
	testenv.Isolate(t)
	projectDir := lockAlias(t, []string{"users"})
	consumer := consumerWithFK("int8", []string{"nonexistent"})
	diags := Check(projectDir, "framework", consumer)
	if !diagHasCode(diags, "E236") {
		t.Fatalf("expected E236 for missing referenced column, got: %v", diags)
	}
}

func TestCheck_MissingReferencedTable(t *testing.T) {
	testenv.Isolate(t)
	// Lock a surface that does NOT include users, but the consumer references it.
	projectDir := t.TempDir()
	fw := frameworkModel()
	surface, _ := ExtractSurface(fw, []string{}, "app") // empty surface
	entries, sh, sem, _ := Vendor(surface, AliasDir(projectDir, "framework"))
	exts, pgv := InferRequirements(fw)
	_ = WriteLockfile(projectDir, &Lockfile{
		Alias: "framework", URL: "u", Ref: "v1", Commit: "c", Schema: "app",
		SurfaceHash: sh, SemanticHash: sem, PGVersion: pgv, Extensions: exts, Objects: entries,
	})
	consumer := consumerWithFK("int8", []string{"id"})
	diags := Check(projectDir, "framework", consumer)
	if !diagHasCode(diags, "E236") {
		t.Fatalf("expected E236 for imported table not in surface, got: %v", diags)
	}
}

// TestCheck_UnreferencedFrameworkChangeSilent: re-locking after an unreferenced
// framework table changes produces the identical surface (the unreferenced table
// is never vendored), so a clean check still passes.
func TestCheck_UnreferencedFrameworkChangeSilent(t *testing.T) {
	testenv.Isolate(t)
	dir1 := lockAlias(t, []string{"users"})
	lf1, _ := ReadLockfile(dir1, "framework")

	// Framework changes ONLY audit_log (unreferenced). Re-extract + hash.
	fw2 := frameworkModel()
	for i := range fw2.Tables {
		if fw2.Tables[i].Name == "audit_log" {
			fw2.Tables[i].Columns = append(fw2.Tables[i].Columns, col("extra", "text"))
		}
	}
	surface2, _ := ExtractSurface(fw2, []string{"users"}, "app")
	_, sh2, sem2, _ := Vendor(surface2, t.TempDir())

	if sh2 != lf1.SurfaceHash || sem2 != lf1.SemanticHash {
		t.Errorf("unreferenced framework change altered the surface: surface %s vs %s, semantic %s vs %s",
			sh2, lf1.SurfaceHash, sem2, lf1.SemanticHash)
	}
}

// TestCheck_TamperedStoreDetected: deleting a vendored object makes an id
// unresolvable -> integrity error.
func TestCheck_TamperedStoreDetected(t *testing.T) {
	testenv.Isolate(t)
	projectDir := lockAlias(t, []string{"users"})
	lf, _ := ReadLockfile(projectDir, "framework")
	// Corrupt the lockfile: point an object at a nonexistent id.
	lf.Objects[0].ID = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := WriteLockfile(projectDir, lf); err != nil {
		t.Fatal(err)
	}
	consumer := consumerWithFK("int8", []string{"id"})
	diags := Check(projectDir, "framework", consumer)
	if !diagHasCode(diags, "E233") {
		t.Fatalf("expected E233 integrity error for missing object, got: %v", diags)
	}
}
