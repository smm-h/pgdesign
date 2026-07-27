package diff

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// tableWith wraps a single table in a schema for diff tests.
func schemaWith(t model.Table) *model.Schema {
	return &model.Schema{Tables: []model.Table{t}}
}

// TestMissedDriftDefaultCaseSensitive is the RED-then-GREEN test for the
// missed-drift class: diff's old normalizeDefault was ToLower(TrimSpace(...)),
// which identified the semantically DISTINCT literal defaults 'Active' and
// 'active' as equal — silently MISSING real drift. Under N, literal defaults
// compare case-sensitively, so the drift is reported.
func TestMissedDriftDefaultCaseSensitive(t *testing.T) {
	desired := schemaWith(model.Table{
		Name: "users", Schema: "public",
		Columns: []model.Column{
			{Name: "status", PGType: typeinfo.T("text"), NotNull: true, Default: model.StrPtr("Active")},
		},
	})
	actual := schemaWith(model.Table{
		Name: "users", Schema: "public",
		Columns: []model.Column{
			{Name: "status", PGType: typeinfo.T("text"), NotNull: true, Default: model.StrPtr("active")},
		},
	})

	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("MISSED DRIFT: 'Active' vs 'active' literal defaults must NOT be equal")
	}
	if len(d.TablesChanged) != 1 || len(d.TablesChanged[0].ColumnsChanged) != 1 ||
		d.TablesChanged[0].ColumnsChanged[0].DefaultChanged == nil {
		t.Fatalf("expected a DefaultChanged for status, got: %s", d.Summary())
	}
}

// TestFalseDriftCheckExpr is the RED-then-GREEN test for the false-drift class:
// the differ compared CHECK expressions by RAW STRING against PG-rewritten
// forms, reporting drift for catalog-independent spelling differences (extra
// parens, whitespace, != vs <>). Under N these converge and no drift is
// reported.
func TestFalseDriftCheckExpr(t *testing.T) {
	desired := schemaWith(model.Table{
		Name: "products", Schema: "public",
		Columns: []model.Column{{Name: "price", PGType: typeinfo.T("int4"), NotNull: true}},
		Checks:  []model.CheckConstraint{{Name: "price_pos", Expr: "price > 0"}},
	})
	actual := schemaWith(model.Table{
		Name: "products", Schema: "public",
		Columns: []model.Column{{Name: "price", PGType: typeinfo.T("int4"), NotNull: true}},
		Checks:  []model.CheckConstraint{{Name: "price_pos", Expr: "( price  >  0 )"}},
	})

	if d := Diff(desired, actual); !d.IsEmpty() {
		t.Fatalf("FALSE DRIFT: equivalent CHECK spellings reported as changed: %s", d.Summary())
	}
}

// TestFalseDriftPolicyExpr covers the policy USING/WITH CHECK false-drift class
// (!= vs <> is catalog-independent and N-convergent).
func TestFalseDriftPolicyExpr(t *testing.T) {
	mk := func(using string) model.Table {
		return model.Table{
			Name: "docs", Schema: "public",
			Columns: []model.Column{{Name: "state", PGType: typeinfo.T("text"), NotNull: true}},
			Policies: []model.Policy{{
				Name: "p", Operation: "SELECT", Using: using,
			}},
		}
	}
	desired := schemaWith(mk("state <> 'archived'"))
	actual := schemaWith(mk("state != 'archived'"))

	if d := Diff(desired, actual); !d.IsEmpty() {
		t.Fatalf("FALSE DRIFT: equivalent policy USING spellings reported as changed: %s", d.Summary())
	}
}

// TestFalseDriftIndexPredicate covers the partial-index predicate false-drift
// class (IN vs = ANY(ARRAY[...]) is catalog-independent and N-convergent).
func TestFalseDriftIndexPredicate(t *testing.T) {
	mk := func(where string) model.Table {
		return model.Table{
			Name: "events", Schema: "public",
			Columns: []model.Column{{Name: "kind", PGType: typeinfo.T("int4"), NotNull: true}},
			Indexes: []model.Index{{
				Name: "events_kind_idx", Columns: []string{"kind"}, Where: where,
			}},
		}
	}
	desired := schemaWith(mk("kind IN (1, 2, 3)"))
	actual := schemaWith(mk("kind = ANY(ARRAY[1, 2, 3])"))

	if d := Diff(desired, actual); !d.IsEmpty() {
		t.Fatalf("FALSE DRIFT: equivalent index predicate spellings reported as changed: %s", d.Summary())
	}
}

// TestNamedatalenTruncationMatch is the RED-then-GREEN fixture for the
// NAMEDATALEN name-matching class: a desired content-derived constraint name
// can exceed 63 bytes, while the same constraint introspected from a live DB
// comes back NAMEDATALEN-truncated (63 bytes). Exact name matching sees them as
// distinct (a false drop+add); truncation-aware matching pairs them.
func TestNamedatalenTruncationMatch(t *testing.T) {
	longName := "products_price_must_be_strictly_positive_and_within_reasonable_bounds_check" // > 63 bytes
	if len(longName) <= 63 {
		t.Fatalf("test fixture name must exceed 63 bytes, got %d", len(longName))
	}
	truncated := longName[:63]

	desired := schemaWith(model.Table{
		Name: "products", Schema: "public",
		Columns: []model.Column{{Name: "price", PGType: typeinfo.T("int4"), NotNull: true}},
		Checks:  []model.CheckConstraint{{Name: longName, Expr: "price > 0"}},
	})
	actual := schemaWith(model.Table{
		Name: "products", Schema: "public",
		Columns: []model.Column{{Name: "price", PGType: typeinfo.T("int4"), NotNull: true}},
		Checks:  []model.CheckConstraint{{Name: truncated, Expr: "price > 0"}},
	})

	if d := Diff(desired, actual); !d.IsEmpty() {
		t.Fatalf("NAMEDATALEN FALSE DRIFT: long name and its 63-byte truncation not matched: %s", d.Summary())
	}
}
