package enc

import (
	"bytes"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
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

// TestPartitionChildrenAreCanonical: partition children are a CANONICAL-ONLY set
// (bound-distinguished), so two tables whose child partitions are declared in
// different orders must encode identically. Canonicalize sorts children by name
// recursively.
func TestPartitionChildrenAreCanonical(t *testing.T) {
	mk := func(children []model.PartitionSpec) *model.Schema {
		s := &model.Schema{
			Name: "public",
			Tables: []model.Table{
				{
					Name: "events", Schema: "public", Comment: "partitioned",
					Columns: []model.Column{
						{Name: "id", PGType: typeinfo.Type{Base: "int4"}, NotNull: true},
						{Name: "ts", PGType: typeinfo.Type{Base: "timestamptz"}, NotNull: true},
					},
					PK: []string{"id", "ts"},
					Partitioning: &model.PartitionSpec{
						Strategy: "RANGE",
						Columns:  []string{"ts"},
						Children: children,
					},
				},
			},
		}
		s.Canonicalize()
		return s
	}
	childA := model.PartitionSpec{Name: "events_2024_01", Bound: "FROM ('2024-01-01') TO ('2024-02-01')"}
	childB := model.PartitionSpec{Name: "events_2024_02", Bound: "FROM ('2024-02-01') TO ('2024-03-01')"}
	childC := model.PartitionSpec{Name: "events_2024_03", Bound: "FROM ('2024-03-01') TO ('2024-04-01')"}

	a, err := EncodeTable(mk([]model.PartitionSpec{childA, childB, childC}).Tables[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeTable(mk([]model.PartitionSpec{childC, childA, childB}).Tables[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("partition children order not canonicalized (must be a sorted set):\n%s\n%s", a, b)
	}
}
