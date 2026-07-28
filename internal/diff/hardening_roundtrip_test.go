package diff

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// roundtrip hardening fixtures (roadmap 5.10 hardening item c): each pins that a
// pure introspect/desired representation difference does NOT false-drift.

func hardeningTable(policies []model.Policy, maint *model.MaintenanceConfig) model.Table {
	return model.Table{
		Name:    "t",
		Schema:  "public",
		Comment: "t",
		PK:      []string{"id"},
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
		},
		Policies:    policies,
		Maintenance: maint,
	}
}

// TestPermissiveDefaultDoesNotDrift: a desired policy with explicit PERMISSIVE
// and an introspected one with an empty type (introspect leaves permissive empty)
// are the same policy.
func TestPermissiveDefaultDoesNotDrift(t *testing.T) {
	desired := &model.Schema{Name: "public", PGVersion: 16, Tables: []model.Table{
		hardeningTable([]model.Policy{{Name: "p", Type: "PERMISSIVE", Operation: "SELECT", Using: "true"}}, nil),
	}}
	actual := &model.Schema{Name: "public", PGVersion: 16, Tables: []model.Table{
		hardeningTable([]model.Policy{{Name: "p", Type: "", Operation: "SELECT", Using: "true"}}, nil),
	}}
	d := DiffLive(desired, actual, nil)
	if !d.IsEmpty() {
		t.Fatalf("PERMISSIVE-default policy must not drift: %s", d.Summary())
	}
}

// TestPartmanIntervalSpellingDoesNotDrift: '1 month' vs the PG-normalized
// '1 mon' are the same interval.
func TestPartmanIntervalSpellingDoesNotDrift(t *testing.T) {
	desired := &model.Schema{Name: "public", PGVersion: 16, Tables: []model.Table{
		hardeningTable(nil, &model.MaintenanceConfig{Interval: "1 month", Retention: "3 months"}),
	}}
	actual := &model.Schema{Name: "public", PGVersion: 16, Tables: []model.Table{
		hardeningTable(nil, &model.MaintenanceConfig{Interval: "1 mon", Retention: "3 mons"}),
	}}
	d := DiffLive(desired, actual, nil)
	if !d.IsEmpty() {
		t.Fatalf("partman interval spelling must not drift: %s", d.Summary())
	}
}

// TestPartmanChildExcludedNotRemoved: a partman-managed child present in the
// introspected schema but absent from desired is excluded, not reported removed.
func TestPartmanChildExcludedNotRemoved(t *testing.T) {
	desired := &model.Schema{Name: "public", PGVersion: 16, Tables: []model.Table{
		hardeningTable(nil, nil),
	}}
	child := hardeningTable(nil, nil)
	child.Name = "t_p2026"
	child.PartmanManaged = true
	child.PartmanParent = "public.t"
	actual := &model.Schema{Name: "public", PGVersion: 16, Tables: []model.Table{
		hardeningTable(nil, nil), child,
	}}
	d := DiffLive(desired, actual, nil)
	if !d.IsEmpty() {
		t.Fatalf("partman child must be excluded, not removed: removed=%v", d.TablesRemoved)
	}
}

// TestNormalizeInterval pins the unit-spelling canonicalization directly.
func TestNormalizeInterval(t *testing.T) {
	cases := [][2]string{
		{"1 month", "1 mon"},
		{"2 months", "2 mons"},
		{"1 year", "1 yr"},
		{"7 days", "7 d"},
		{"1 week", "1 weeks"},
	}
	for _, c := range cases {
		if normalizeInterval(c[0]) != normalizeInterval(c[1]) {
			t.Errorf("normalizeInterval(%q)=%q != normalizeInterval(%q)=%q",
				c[0], normalizeInterval(c[0]), c[1], normalizeInterval(c[1]))
		}
	}
}
