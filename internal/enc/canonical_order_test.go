package enc

import (
	"bytes"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

// TestViewDependsOnIsCanonical: a view's DependsOn is a dependency SET, so two
// ≈_syn-equal views that declare the same dependencies in different orders must
// encode to identical bytes. Canonicalize sorts the set; the encoder emits it
// verbatim, so convergence depends on the sort.
func TestViewDependsOnIsCanonical(t *testing.T) {
	mk := func(deps []string) *model.Schema {
		s := &model.Schema{
			Name: "public",
			Views: []model.View{
				{Name: "v", Schema: "public", Query: "SELECT 1", Comment: "a view", DependsOn: deps},
			},
		}
		s.Canonicalize()
		return s
	}
	a, err := EncodeView(mk([]string{"a_dep", "b_dep", "c_dep"}).Views[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeView(mk([]string{"c_dep", "a_dep", "b_dep"}).Views[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("view DependsOn order not canonicalized (must be a sorted set):\n%s\n%s", a, b)
	}
}

// TestMaterializedViewDependsOnIsCanonical: same as views — the matview
// DependsOn set must converge under permutation.
func TestMaterializedViewDependsOnIsCanonical(t *testing.T) {
	mk := func(deps []string) *model.Schema {
		s := &model.Schema{
			Name: "public",
			MaterializedViews: []model.MaterializedView{
				{Name: "mv", Schema: "public", Query: "SELECT 1", Comment: "a mv", DependsOn: deps},
			},
		}
		s.Canonicalize()
		return s
	}
	a, err := EncodeMaterializedView(mk([]string{"a_dep", "b_dep", "c_dep"}).MaterializedViews[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeMaterializedView(mk([]string{"c_dep", "b_dep", "a_dep"}).MaterializedViews[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("matview DependsOn order not canonicalized (must be a sorted set):\n%s\n%s", a, b)
	}
}

// TestFunctionDependsOnIsCanonical: same as views — the function DependsOn set
// must converge under permutation.
func TestFunctionDependsOnIsCanonical(t *testing.T) {
	mk := func(deps []string) *model.Schema {
		s := &model.Schema{
			Name: "public",
			Functions: []model.Function{
				{Name: "f", Schema: "public", Language: "sql", ReturnType: "int4",
					Body: "SELECT 1", Comment: "a fn", DependsOn: deps},
			},
		}
		s.Canonicalize()
		return s
	}
	a, err := EncodeFunction(mk([]string{"a_dep", "b_dep", "c_dep"}).Functions[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeFunction(mk([]string{"b_dep", "c_dep", "a_dep"}).Functions[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("function DependsOn order not canonicalized (must be a sorted set):\n%s\n%s", a, b)
	}
}
