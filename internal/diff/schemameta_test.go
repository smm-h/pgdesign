package diff

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
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
