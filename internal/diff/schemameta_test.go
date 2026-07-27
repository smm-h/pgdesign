package diff

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// TestDiffReportsPGVersionChange pins the reverse-conformance obligation for
// pg_version: it is part of a model's revision, so a change must produce a
// non-empty diff. Historically the differ was blind to it (Part III).
func TestDiffReportsPGVersionChange(t *testing.T) {
	a := &model.Schema{Name: "s", PGVersion: 16}
	b := &model.Schema{Name: "s", PGVersion: 17}
	d := Diff(a, b)
	if d.IsEmpty() {
		t.Fatal("diff blind to pg_version change: expected non-empty diff")
	}
	if d.PGVersionChanged == nil {
		t.Fatal("expected PGVersionChanged to be set")
	}
	// [old(actual), new(desired)]; Diff(desired, actual) => a is desired, b is actual.
	if *d.PGVersionChanged != [2]int{17, 16} {
		t.Errorf("PGVersionChanged = %v, want [17 16]", *d.PGVersionChanged)
	}
}

// TestDiffReportsGroupsChange pins the reverse-conformance obligation for the
// table-group map.
func TestDiffReportsGroupsChange(t *testing.T) {
	a := &model.Schema{Name: "s", Groups: map[string][]string{"core": {"users"}}}
	b := &model.Schema{Name: "s", Groups: map[string][]string{"core": {"users", "orders"}}}
	d := Diff(a, b)
	if d.IsEmpty() {
		t.Fatal("diff blind to groups change: expected non-empty diff")
	}
	if !d.GroupsChanged {
		t.Fatal("expected GroupsChanged to be true")
	}
}

// TestDiffGroupsOrderInsensitive confirms group membership is compared as a set
// (group table lists are a canonical-only collection): a mere reordering is not
// a change.
func TestDiffGroupsOrderInsensitive(t *testing.T) {
	a := &model.Schema{Name: "s", Groups: map[string][]string{"core": {"users", "orders"}}}
	b := &model.Schema{Name: "s", Groups: map[string][]string{"core": {"orders", "users"}}}
	d := Diff(a, b)
	if d.GroupsChanged {
		t.Error("group table-list reordering should not be a change (set comparison)")
	}
}

// TestDiffPGVersionAndGroupsEqualIsEmpty confirms identical schema-meta fields
// produce no schema-meta diff (no spurious drift).
func TestDiffPGVersionAndGroupsEqualIsEmpty(t *testing.T) {
	a := &model.Schema{Name: "s", PGVersion: 16, Groups: map[string][]string{"core": {"users"}}}
	b := &model.Schema{Name: "s", PGVersion: 16, Groups: map[string][]string{"core": {"users"}}}
	d := Diff(a, b)
	if !d.IsEmpty() {
		t.Errorf("identical schema-meta produced a diff: %s", d.Summary())
	}
}

// mkSemColumnSchema builds a one-table schema whose single column carries the
// given semantic type name over an identical int4 PGType. Two such schemas are
// DDL-identical (a pure-alias scalar type materializes no domain) yet have
// distinct revisions when the semantic names differ.
func mkSemColumnSchema(semName string) *model.Schema {
	return &model.Schema{
		Name: "s",
		Tables: []model.Table{{
			Name:    "users",
			Comment: "u",
			Columns: []model.Column{{
				Name:             "id",
				PGType:           typeinfo.T("int4"),
				NotNull:          true,
				SemanticTypeName: semName,
			}},
			PK: []string{"id"},
		}},
	}
}

// TestDiffModelToModelComparesSemanticTypeName pins the reverse-conformance
// obligation for pure-alias scalar types: two registry-present models that are
// DDL-identical but declare different semantic type names have distinct
// revisions and must therefore produce a non-empty diff.
func TestDiffModelToModelComparesSemanticTypeName(t *testing.T) {
	a := mkSemColumnSchema("account_id") // desired
	b := mkSemColumnSchema("int")        // actual
	d := Diff(a, b)
	if d.IsEmpty() {
		t.Fatal("model-to-model diff blind to semantic type name change: expected non-empty diff")
	}
	if len(d.TablesChanged) != 1 || len(d.TablesChanged[0].ColumnsChanged) != 1 {
		t.Fatalf("expected one changed column, got %+v", d.TablesChanged)
	}
	cc := d.TablesChanged[0].ColumnsChanged[0]
	if cc.SemanticTypeNameChanged == nil {
		t.Fatal("expected SemanticTypeNameChanged to be set")
	}
	if *cc.SemanticTypeNameChanged != [2]string{"int", "account_id"} {
		t.Errorf("SemanticTypeNameChanged = %v, want [int account_id]", *cc.SemanticTypeNameChanged)
	}
}

// TestDiffLiveSuppressesSemanticTypeName confirms the introspected diff path
// (DiffLive) does NOT compare semantic type names: an introspected actual
// carries none, so comparing would false-drift every migrate/diff --live run.
func TestDiffLiveSuppressesSemanticTypeName(t *testing.T) {
	desired := mkSemColumnSchema("account_id")
	actual := mkSemColumnSchema("") // introspected: no semantic names
	d := DiffLive(desired, actual, nil)
	if !d.IsEmpty() {
		t.Fatalf("DiffLive false-drifted on semantic type name: %s", d.Summary())
	}
}
