package codegen

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// determinismSchema returns a schema with enough enum columns and CHECK
// constraint columns that map iteration order differences become visible
// in generated output.
func determinismSchema() *model.Schema {
	return &model.Schema{
		Name: "determinism",
		Enums: []model.Enum{
			{Name: "role", Values: []string{"admin", "user", "guest"}},
			{Name: "plan", Values: []string{"free", "pro", "enterprise"}},
			{Name: "region", Values: []string{"eu", "us", "apac"}},
		},
		Tables: []model.Table{
			{
				Name: "accounts",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("bigint"), NotNull: true},
					{Name: "name", PGType: typeinfo.MustParse("text"), NotNull: true},
					{Name: "email", PGType: typeinfo.MustParse("text"), NotNull: true},
					{Name: "slug", PGType: typeinfo.MustParse("varchar"), NotNull: true},
					{Name: "role", PGType: typeinfo.MustParse("role"), NotNull: true, TypeKind: "enum"},
					{Name: "plan", PGType: typeinfo.MustParse("plan"), NotNull: true, TypeKind: "enum"},
					{Name: "region", PGType: typeinfo.MustParse("region"), NotNull: true, TypeKind: "enum"},
					{Name: "age", PGType: typeinfo.MustParse("integer"), NotNull: true},
					{Name: "score", PGType: typeinfo.MustParse("integer"), NotNull: true},
				},
				PK: []string{"id"},
				Checks: []model.CheckConstraint{
					{Name: "ck_accounts_age_range", Expr: "age >= 0 AND age <= 150"},
					{Name: "ck_accounts_score_min", Expr: "score >= 0"},
					{Name: "ck_accounts_email_fmt", Expr: "email LIKE '%@%'"},
					{Name: "ck_accounts_slug_len", Expr: "LENGTH(slug) <= 100"},
				},
			},
		},
	}
}

// TestConstraintsGenerators_Deterministic verifies that every constraints-mode
// generator produces byte-identical output across repeated runs on the same
// schema. Map iteration order in Go is randomized, so any generator that
// ranges over ConstraintSet maps directly will flake here.
func TestConstraintsGenerators_Deterministic(t *testing.T) {
	generators := map[string]Generator{
		"go":     &GoConstraintsGenerator{},
		"python": &PythonConstraintsGenerator{},
		"java":   &JavaConstraintsGenerator{},
		"kotlin": &KotlinConstraintsGenerator{},
		"ts":     &TSConstraintsGenerator{},
		"zig":    &ZigConstraintsGenerator{},
	}

	schema := determinismSchema()

	for lang, gen := range generators {
		t.Run(lang, func(t *testing.T) {
			first, diags := gen.Generate(schema)
			if hasErrorDiag(diags) {
				t.Fatalf("unexpected error diagnostics: %v", diags)
			}
			for i := 1; i < 10; i++ {
				out, diags := gen.Generate(schema)
				if hasErrorDiag(diags) {
					t.Fatalf("run %d: unexpected error diagnostics: %v", i, diags)
				}
				if !bytes.Equal(out, first) {
					t.Fatalf("run %d produced different output than run 0:\n%s",
						i, diffHint(first, out))
				}
			}
		})
	}
}

func hasErrorDiag(diags []diagnostic.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == diagnostic.Error {
			return true
		}
	}
	return false
}

// diffHint returns a short description of the first byte position where two
// outputs diverge, with surrounding context from both.
func diffHint(a, b []byte) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	pos := n
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			pos = i
			break
		}
	}
	start := pos - 80
	if start < 0 {
		start = 0
	}
	endA := pos + 80
	if endA > len(a) {
		endA = len(a)
	}
	endB := pos + 80
	if endB > len(b) {
		endB = len(b)
	}
	return fmt.Sprintf("first divergence at byte %d\n--- run 0 ---\n...%s...\n--- run N ---\n...%s...",
		pos, a[start:endA], b[start:endB])
}
