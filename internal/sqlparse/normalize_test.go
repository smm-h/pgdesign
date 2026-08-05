package sqlparse

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"
)

// TestNormalizeExpr_DeparseFreeFoldings pins the classes the empirical deparse
// survey reports as normalizing FOR FREE (no explicit fold needed): quoting,
// != <-> <>, whitespace/parens, function case, IS NULL forms.
func TestNormalizeExpr_DeparseFreeFoldings(t *testing.T) {
	testenv.Isolate(t)
	pairs := [][2]string{
		{"x != 5", "x <> 5"},
		{"price > 0", "( price  >  0 )"},
		{"a AND b", "((a) AND (b))"},
		{"x IS NULL", "x ISNULL"},
	}
	for _, p := range pairs {
		if !ExprEqual(p[0], p[1]) {
			t.Errorf("expected ≈_syn-equal: %q vs %q -> %q vs %q",
				p[0], p[1], NormalizeExpr(p[0]), NormalizeExpr(p[1]))
		}
	}
}

// TestNormalizeExpr_CastAliasFolding pins the cast-type-name alias folding:
// x::integer and x::int4 must converge (they diverge under raw deparse).
func TestNormalizeExpr_CastAliasFolding(t *testing.T) {
	testenv.Isolate(t)
	cases := [][2]string{
		{"x::integer", "x::int4"},
		{"x::bigint", "x::int8"},
		{"x::boolean", "x::bool"},
		{"n::smallint", "n::int2"},
	}
	for _, c := range cases {
		if !ExprEqual(c[0], c[1]) {
			t.Errorf("cast alias should converge: %q (%q) vs %q (%q)",
				c[0], NormalizeExpr(c[0]), c[1], NormalizeExpr(c[1]))
		}
	}
	// Schema-qualified user types must NOT be rewritten.
	got := NormalizeExpr("x::myschema.mytype")
	if !strings.Contains(got, "myschema.mytype") {
		t.Errorf("schema-qualified user type mangled: %q", got)
	}
}

// TestNormalizeExpr_FoldingSymmetry is the required verify item: the IN-form
// and the = ANY(ARRAY[...]) form of the same predicate normalize IDENTICALLY,
// and equality holds FROM EITHER SIDE (the fold is symmetric by construction
// because both sides run through N).
func TestNormalizeExpr_FoldingSymmetry(t *testing.T) {
	testenv.Isolate(t)
	cases := [][2]string{
		{"x IN (1, 2, 3)", "x = ANY(ARRAY[1, 2, 3])"},
		{"status IN ('a', 'b')", "status = ANY(ARRAY['a', 'b'])"},
		{"id IN (1)", "id = ANY(ARRAY[1])"},
	}
	for _, c := range cases {
		na, nb := NormalizeExpr(c[0]), NormalizeExpr(c[1])
		if na != nb {
			t.Errorf("IN/ANY forms diverge: %q -> %q ; %q -> %q", c[0], na, c[1], nb)
		}
		// Symmetry: equality independent of argument order.
		if ExprEqual(c[0], c[1]) != ExprEqual(c[1], c[0]) {
			t.Errorf("ExprEqual not symmetric for %q / %q", c[0], c[1])
		}
	}
}

// TestNormalizeExpr_Idempotence is the L9 N∘N = N property over a curated
// corpus. (The model-side generated-corpus idempotence test lives in
// golden_test.go; this one guards the primitive directly.)
func TestNormalizeExpr_Idempotence(t *testing.T) {
	testenv.Isolate(t)
	corpus := []string{
		"price > 0",
		"x <> 5",
		"x::integer",
		"x = ANY(ARRAY[1, 2, 3])",
		"status IN ('active', 'archived')",
		"a AND b OR c",
		"length(name) <= 255",
		"created_at >= now() - interval '1 day'",
		"metadata ? 'title'",
		"CASE WHEN x > 0 THEN 'pos' ELSE 'neg' END",
		"coalesce(a, b, 0)",
		"lower(email) = email",
		"(amount).x::numeric(10, 2) > 0",
		"this is not valid )( sql",
		"",
	}
	for _, e := range corpus {
		once := NormalizeExpr(e)
		twice := NormalizeExpr(once)
		if once != twice {
			t.Errorf("N not idempotent for %q: N=%q N∘N=%q", e, once, twice)
		}
	}
}

// TestNormalizeExpr_ParseFailureVerbatim pins the opaque-leaf behavior: an
// unparseable expression is returned trimmed and unchanged, never an error.
func TestNormalizeExpr_ParseFailureVerbatim(t *testing.T) {
	testenv.Isolate(t)
	cases := map[string]string{
		"  garbage )( not sql  ": "garbage )( not sql",
		"":                       "",
		"   ":                    "",
	}
	for in, want := range cases {
		if got := NormalizeExpr(in); got != want {
			t.Errorf("NormalizeExpr(%q) = %q, want %q", in, got, want)
		}
	}
}
