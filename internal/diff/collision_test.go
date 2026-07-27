package diff

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

// longName builds an identifier of the given length whose first 63 bytes are a
// fixed prefix, so two of them with different suffixes past byte 63 share a
// truncation.
func longName(prefix63 string, suffix string) string {
	if len(prefix63) != 63 {
		panic("prefix must be exactly 63 bytes")
	}
	return prefix63 + suffix
}

// TestCheckTruncationCollisionDetected is the red-green regression for the
// NAMEDATALEN collision guard: two DISTINCT content-derived index names that
// truncate to the same 63 bytes must be a HARD ERROR naming both, not a silent
// ambiguous match.
func TestCheckTruncationCollisionDetected(t *testing.T) {
	prefix := strings.Repeat("a", 63)
	n1 := longName(prefix, "_one")
	n2 := longName(prefix, "_two")
	s := &model.Schema{
		Tables: []model.Table{
			{
				Name: "orders",
				Indexes: []model.Index{
					{Name: n1, Columns: []string{"a"}},
					{Name: n2, Columns: []string{"b"}},
				},
			},
		},
	}
	err := CheckTruncationCollisions(s)
	if err == nil {
		t.Fatal("expected a collision error for two indexes sharing a 63-byte truncation")
	}
	if !strings.Contains(err.Error(), n1) || !strings.Contains(err.Error(), n2) {
		t.Errorf("collision error must name BOTH colliding identifiers; got: %v", err)
	}
}

// TestCheckTruncationCollisionCleanSchema confirms distinct short names, and
// distinct long names that truncate differently, do NOT trip the guard.
func TestCheckTruncationCollisionCleanSchema(t *testing.T) {
	s := &model.Schema{
		Tables: []model.Table{
			{
				Name: "orders",
				Indexes: []model.Index{
					{Name: "idx_orders_a", Columns: []string{"a"}},
					{Name: "idx_orders_b", Columns: []string{"b"}},
				},
				Checks: []model.CheckConstraint{
					{Name: "ck_orders_a", Expr: "a > 0"},
				},
			},
		},
	}
	if err := CheckTruncationCollisions(s); err != nil {
		t.Errorf("clean schema must not trip the guard: %v", err)
	}
}

// TestCheckTruncationCollisionMatView covers the materialized-view index
// collection, which also uses truncation-aware matching.
func TestCheckTruncationCollisionMatView(t *testing.T) {
	prefix := strings.Repeat("m", 63)
	s := &model.Schema{
		MaterializedViews: []model.MaterializedView{
			{
				Name: "mv_report",
				Indexes: []model.Index{
					{Name: longName(prefix, "_x"), Columns: []string{"a"}},
					{Name: longName(prefix, "_y"), Columns: []string{"b"}},
				},
			},
		},
	}
	if err := CheckTruncationCollisions(s); err == nil {
		t.Fatal("expected a collision error for matview indexes sharing a truncation")
	}
}
