package diff

import (
	"github.com/smm-h/pgdesign/internal/testenv"
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
	testenv.Isolate(t)
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
	testenv.Isolate(t)
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

// TestIndexEqualUnquotedIdentifierCaseFolded is the mixed-case unquoted-identifier
// rider (1.5 audit): PostgreSQL folds unquoted identifiers to lowercase, so a
// desired key column `Email` and the introspected `email` name the SAME column
// and must NOT false-drift. A raw exact compare (the pre-fix behavior) reported
// a spurious change.
func TestIndexEqualUnquotedIdentifierCaseFolded(t *testing.T) {
	testenv.Isolate(t)
	desired := &model.Index{Name: "idx", Columns: []string{"Email"}}
	actual := &model.Index{Name: "idx", Columns: []string{"email"}}
	if !indexEqual(desired, actual) {
		t.Errorf("unquoted key columns %q and %q fold to the same identifier but indexEqual reported a change",
			desired.Columns[0], actual.Columns[0])
	}
}

// TestIndexEqualQuotedIdentifierCaseSensitive confirms QUOTED identifiers stay
// case-sensitive: a quoted "Email" is a genuinely distinct column from email
// and must NOT be folded together. Quoting routes the comparison through the
// expression path, which preserves case.
func TestIndexEqualQuotedIdentifierCaseSensitive(t *testing.T) {
	testenv.Isolate(t)
	desired := &model.Index{Name: "idx", Columns: []string{`"Email"`}}
	actual := &model.Index{Name: "idx", Columns: []string{`"email"`}}
	if indexEqual(desired, actual) {
		t.Errorf("quoted key columns %q and %q are case-sensitively distinct but indexEqual folded them together",
			desired.Columns[0], actual.Columns[0])
	}
}

// TestExclusionEqualExpressionColumn is the same hole for exclusion-constraint
// element columns, which are also index key columns and can be expressions.
func TestExclusionEqualExpressionColumn(t *testing.T) {
	testenv.Isolate(t)
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
