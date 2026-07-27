package diff

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

// TestIndexEqualExpressionKeyColumn is the red-green regression for the
// expression key-column comparison hole: an index whose key column is an
// EXPRESSION (e.g. lower(email)) must compare via ≈_syn (sqlparse.ExprEqual),
// not by raw string equality. Before the fix, indexEqual compared key columns
// with raw sliceEqual, so two ≈_syn-equal spellings of the same expression key
// (function-case, whitespace) false-drifted as a change.
func TestIndexEqualExpressionKeyColumn(t *testing.T) {
	desired := &model.Index{
		Name:    "idx_users_email_lower",
		Columns: []string{"LOWER(email)"},
	}
	actual := &model.Index{
		Name:    "idx_users_email_lower",
		Columns: []string{"lower(email)"},
	}
	if !indexEqual(desired, actual) {
		t.Errorf("expression key columns %q and %q are ≈_syn-equal but indexEqual reported a change",
			desired.Columns[0], actual.Columns[0])
	}
}

// TestIndexEqualPlainKeyColumnExact confirms plain identifier key columns still
// compare EXACTLY — a genuine rename of a plain column is a real change and must
// not be masked by routing through the expression normalizer.
func TestIndexEqualPlainKeyColumnExact(t *testing.T) {
	a := &model.Index{Name: "idx", Columns: []string{"email"}}
	b := &model.Index{Name: "idx", Columns: []string{"username"}}
	if indexEqual(a, b) {
		t.Error("distinct plain key columns should not be equal")
	}
	same := &model.Index{Name: "idx", Columns: []string{"email"}}
	if !indexEqual(a, same) {
		t.Error("identical plain key columns should be equal")
	}
}

// TestExclusionEqualExpressionColumn is the same hole for exclusion-constraint
// element columns, which are also index key columns and can be expressions.
func TestExclusionEqualExpressionColumn(t *testing.T) {
	desired := model.ExclusionConstraint{
		Name:   "ex_room_during",
		Method: "gist",
		Elements: []model.ExclusionElement{
			{Column: "LOWER(room)", Operator: "="},
		},
	}
	actual := model.ExclusionConstraint{
		Name:   "ex_room_during",
		Method: "gist",
		Elements: []model.ExclusionElement{
			{Column: "lower(room)", Operator: "="},
		},
	}
	if !exclusionEqual(desired, actual) {
		t.Errorf("expression exclusion element columns %q and %q are ≈_syn-equal but exclusionEqual reported a change",
			desired.Elements[0].Column, actual.Elements[0].Column)
	}
}
