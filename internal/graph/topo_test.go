package graph

import (
	"testing"
)

type node struct {
	name string
	deps []string
}

var (
	getName = func(n node) string { return n.name }
	getDeps = func(n node) []string { return n.deps }
)

func names(nodes []node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.name
	}
	return out
}

func namesFromGroups(groups [][]node) [][]string {
	out := make([][]string, len(groups))
	for i, g := range groups {
		out[i] = names(g)
	}
	return out
}

func assertOrder(t *testing.T, sorted []node, expected []string) {
	t.Helper()
	got := names(sorted)
	if len(got) != len(expected) {
		t.Fatalf("length mismatch: got %v, want %v", got, expected)
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Fatalf("position %d: got %q, want %q (full: %v)", i, got[i], expected[i], got)
		}
	}
}

func assertNoCycles(t *testing.T, cycles [][]node) {
	t.Helper()
	if len(cycles) != 0 {
		t.Fatalf("expected no cycles, got %v", namesFromGroups(cycles))
	}
}

func TestTopoSort_Empty(t *testing.T) {
	sorted, cycles := TopoSort([]node{}, getName, getDeps)
	if len(sorted) != 0 {
		t.Fatalf("expected empty sorted, got %v", names(sorted))
	}
	assertNoCycles(t, cycles)
}

func TestTopoSort_SingleItem(t *testing.T) {
	items := []node{{name: "A"}}
	sorted, cycles := TopoSort(items, getName, getDeps)
	assertOrder(t, sorted, []string{"A"})
	assertNoCycles(t, cycles)
}

func TestTopoSort_LinearChain(t *testing.T) {
	// C depends on B, B depends on A.
	items := []node{
		{name: "C", deps: []string{"B"}},
		{name: "B", deps: []string{"A"}},
		{name: "A"},
	}
	sorted, cycles := TopoSort(items, getName, getDeps)
	assertOrder(t, sorted, []string{"A", "B", "C"})
	assertNoCycles(t, cycles)
}

func TestTopoSort_Diamond(t *testing.T) {
	// D depends on B and C; B depends on A; C depends on A.
	items := []node{
		{name: "A"},
		{name: "B", deps: []string{"A"}},
		{name: "C", deps: []string{"A"}},
		{name: "D", deps: []string{"B", "C"}},
	}
	sorted, cycles := TopoSort(items, getName, getDeps)
	assertOrder(t, sorted, []string{"A", "B", "C", "D"})
	assertNoCycles(t, cycles)
}

func TestTopoSort_Cycle(t *testing.T) {
	// X depends on Y, Y depends on X.
	items := []node{
		{name: "X", deps: []string{"Y"}},
		{name: "Y", deps: []string{"X"}},
	}
	sorted, cycles := TopoSort(items, getName, getDeps)
	// Both appear in sorted.
	if len(sorted) != 2 {
		t.Fatalf("expected 2 sorted items, got %d: %v", len(sorted), names(sorted))
	}
	// Both appear in cycles.
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle group, got %d", len(cycles))
	}
	if len(cycles[0]) != 2 {
		t.Fatalf("expected 2 items in cycle group, got %d: %v", len(cycles[0]), names(cycles[0]))
	}
}

func TestTopoSort_SelfDependency(t *testing.T) {
	items := []node{
		{name: "A", deps: []string{"A"}},
	}
	sorted, cycles := TopoSort(items, getName, getDeps)
	assertOrder(t, sorted, []string{"A"})
	assertNoCycles(t, cycles)
}

func TestTopoSort_ExternalDependency(t *testing.T) {
	items := []node{
		{name: "A", deps: []string{"Z"}},
	}
	sorted, cycles := TopoSort(items, getName, getDeps)
	assertOrder(t, sorted, []string{"A"})
	assertNoCycles(t, cycles)
}

func TestTopoSort_PreservesInputOrder(t *testing.T) {
	// All items independent -- should preserve input order.
	items := []node{
		{name: "C"},
		{name: "A"},
		{name: "B"},
	}
	sorted, cycles := TopoSort(items, getName, getDeps)
	assertOrder(t, sorted, []string{"C", "A", "B"})
	assertNoCycles(t, cycles)
}

func TestTopoSort_CycleMembersInSorted(t *testing.T) {
	// Cycle members must appear in sorted output (not lost).
	items := []node{
		{name: "A", deps: []string{"B"}},
		{name: "B", deps: []string{"C"}},
		{name: "C", deps: []string{"A"}},
	}
	sorted, cycles := TopoSort(items, getName, getDeps)
	if len(sorted) != 3 {
		t.Fatalf("expected 3 sorted items, got %d: %v", len(sorted), names(sorted))
	}
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle group, got %d", len(cycles))
	}
	if len(cycles[0]) != 3 {
		t.Fatalf("expected 3 items in cycle group, got %d", len(cycles[0]))
	}
}

func TestTopoSort_PartialCycle(t *testing.T) {
	// A has no deps, B and C form a cycle.
	items := []node{
		{name: "A"},
		{name: "B", deps: []string{"C"}},
		{name: "C", deps: []string{"B"}},
	}
	sorted, cycles := TopoSort(items, getName, getDeps)
	if len(sorted) != 3 {
		t.Fatalf("expected 3 sorted items, got %d: %v", len(sorted), names(sorted))
	}
	// A must be first (no deps, sorted normally).
	if sorted[0].name != "A" {
		t.Fatalf("expected A first, got %q", sorted[0].name)
	}
	// B and C in cycle.
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle group, got %d", len(cycles))
	}
	if len(cycles[0]) != 2 {
		t.Fatalf("expected 2 items in cycle group, got %d", len(cycles[0]))
	}
}
