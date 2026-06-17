package fd

import (
	"reflect"
	"sort"
	"testing"
)

func TestClosure_SingleChain(t *testing.T) {
	// A→B, B→C, C→D: closure of {A} should be {A,B,C,D}
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
		{Determinant: []string{"B"}, Dependent: []string{"C"}},
		{Determinant: []string{"C"}, Dependent: []string{"D"}},
	}
	got := Closure([]string{"A"}, fds)
	want := []string{"A", "B", "C", "D"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Closure({A}) = %v, want %v", got, want)
	}
}

func TestClosure_CompositeKey(t *testing.T) {
	// A→C, BC→D: closure of {A,B} should be {A,B,C,D}
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"C"}},
		{Determinant: []string{"B", "C"}, Dependent: []string{"D"}},
	}
	got := Closure([]string{"A", "B"}, fds)
	want := []string{"A", "B", "C", "D"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Closure({A,B}) = %v, want %v", got, want)
	}
}

func TestClosure_NoFDs(t *testing.T) {
	got := Closure([]string{"A", "B"}, nil)
	want := []string{"A", "B"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Closure({A,B}, nil) = %v, want %v", got, want)
	}
}

func TestClosure_MultipleDependents(t *testing.T) {
	// A→{B,C}: closure of {A} should be {A,B,C}
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B", "C"}},
	}
	got := Closure([]string{"A"}, fds)
	want := []string{"A", "B", "C"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Closure({A}) = %v, want %v", got, want)
	}
}

func TestMinimalCover_RemovesRedundant(t *testing.T) {
	// A→B, B→C, A→C: the FD A→C is redundant (derivable via A→B, B→C)
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
		{Determinant: []string{"B"}, Dependent: []string{"C"}},
		{Determinant: []string{"A"}, Dependent: []string{"C"}},
	}
	got := MinimalCover(fds)
	want := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
		{Determinant: []string{"B"}, Dependent: []string{"C"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MinimalCover = %v, want %v", got, want)
	}
}

func TestMinimalCover_RemovesExtraneousLHS(t *testing.T) {
	// A→B, AB→C should simplify to A→B, A→C (since A→B makes B extraneous in AB→C)
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
		{Determinant: []string{"A", "B"}, Dependent: []string{"C"}},
	}
	got := MinimalCover(fds)
	want := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
		{Determinant: []string{"A"}, Dependent: []string{"C"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MinimalCover = %v, want %v", got, want)
	}
}

func TestMinimalCover_DecomposesRHS(t *testing.T) {
	// A→{B,C} should decompose into A→B, A→C
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B", "C"}},
	}
	got := MinimalCover(fds)
	want := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
		{Determinant: []string{"A"}, Dependent: []string{"C"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MinimalCover = %v, want %v", got, want)
	}
}

func TestCandidateKeys_CompositeAndAlternate(t *testing.T) {
	// R(A,B,C,D), FDs: AB→CD, C→A
	// Keys should be {A,B} and {B,C}
	allAttrs := []string{"A", "B", "C", "D"}
	fds := []FuncDep{
		{Determinant: []string{"A", "B"}, Dependent: []string{"C", "D"}},
		{Determinant: []string{"C"}, Dependent: []string{"A"}},
	}
	got := CandidateKeys(allAttrs, fds)
	want := [][]string{{"A", "B"}, {"B", "C"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CandidateKeys = %v, want %v", got, want)
	}
}

func TestCandidateKeys_SingleKey(t *testing.T) {
	// R(A,B,C), FDs: A→B, A→C — only key is {A}
	allAttrs := []string{"A", "B", "C"}
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
		{Determinant: []string{"A"}, Dependent: []string{"C"}},
	}
	got := CandidateKeys(allAttrs, fds)
	want := [][]string{{"A"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CandidateKeys = %v, want %v", got, want)
	}
}

func TestCandidateKeys_AllAttrsAreKey(t *testing.T) {
	// R(A,B,C) with no FDs — the only candidate key is {A,B,C}
	allAttrs := []string{"A", "B", "C"}
	got := CandidateKeys(allAttrs, nil)
	want := [][]string{{"A", "B", "C"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CandidateKeys = %v, want %v", got, want)
	}
}

func TestIsSuperkey_True(t *testing.T) {
	allAttrs := []string{"A", "B", "C", "D"}
	fds := []FuncDep{
		{Determinant: []string{"A", "B"}, Dependent: []string{"C", "D"}},
	}
	if !IsSuperkey([]string{"A", "B"}, allAttrs, fds) {
		t.Error("expected {A,B} to be a superkey")
	}
}

func TestIsSuperkey_False(t *testing.T) {
	allAttrs := []string{"A", "B", "C", "D"}
	fds := []FuncDep{
		{Determinant: []string{"A", "B"}, Dependent: []string{"C", "D"}},
	}
	if IsSuperkey([]string{"A"}, allAttrs, fds) {
		t.Error("expected {A} to not be a superkey")
	}
}

func TestIsSuperkey_SupersetOfKey(t *testing.T) {
	allAttrs := []string{"A", "B", "C", "D"}
	fds := []FuncDep{
		{Determinant: []string{"A", "B"}, Dependent: []string{"C", "D"}},
	}
	if !IsSuperkey([]string{"A", "B", "C"}, allAttrs, fds) {
		t.Error("expected {A,B,C} to be a superkey (superset of key {A,B})")
	}
}

func TestIsPrime_True(t *testing.T) {
	keys := [][]string{{"A", "B"}, {"B", "C"}}
	if !IsPrime("A", keys) {
		t.Error("expected A to be prime (in key {A,B})")
	}
	if !IsPrime("B", keys) {
		t.Error("expected B to be prime (in both keys)")
	}
	if !IsPrime("C", keys) {
		t.Error("expected C to be prime (in key {B,C})")
	}
}

func TestIsPrime_False(t *testing.T) {
	keys := [][]string{{"A", "B"}, {"B", "C"}}
	if IsPrime("D", keys) {
		t.Error("expected D to not be prime")
	}
}

func TestIsSubset(t *testing.T) {
	if !isSubset([]string{"A", "B"}, []string{"A", "B", "C"}) {
		t.Error("{A,B} should be subset of {A,B,C}")
	}
	if isSubset([]string{"A", "D"}, []string{"A", "B", "C"}) {
		t.Error("{A,D} should not be subset of {A,B,C}")
	}
	if !isSubset(nil, []string{"A"}) {
		t.Error("empty set should be subset of anything")
	}
}

func TestSetUnion(t *testing.T) {
	got := setUnion([]string{"A", "C"}, []string{"B", "C"})
	want := []string{"A", "B", "C"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("setUnion = %v, want %v", got, want)
	}
}

func TestSetEquals(t *testing.T) {
	if !setEquals([]string{"A", "B"}, []string{"A", "B"}) {
		t.Error("equal sets should be equal")
	}
	if setEquals([]string{"A", "B"}, []string{"A", "C"}) {
		t.Error("different sets should not be equal")
	}
	if setEquals([]string{"A"}, []string{"A", "B"}) {
		t.Error("different-length sets should not be equal")
	}
}

func TestClosure_IsDeterministic(t *testing.T) {
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"C", "B"}},
		{Determinant: []string{"B"}, Dependent: []string{"D"}},
	}
	for i := 0; i < 10; i++ {
		got := Closure([]string{"A"}, fds)
		if !sort.StringsAreSorted(got) {
			t.Fatalf("closure result not sorted: %v", got)
		}
	}
}

// --- BCNFDecompose tests ---

func TestBCNFDecompose_AlreadyBCNF(t *testing.T) {
	allAttrs := []string{"A", "B", "C"}
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B", "C"}},
	}
	components := BCNFDecompose("tbl", allAttrs, fds)
	if len(components) != 1 {
		t.Fatalf("expected 1 component, got %d: %+v", len(components), components)
	}
	if components[0].Name != "tbl" {
		t.Errorf("expected name 'tbl', got '%s'", components[0].Name)
	}
	if !reflect.DeepEqual(components[0].Attributes, []string{"A", "B", "C"}) {
		t.Errorf("expected attributes [A B C], got %v", components[0].Attributes)
	}
}

func TestBCNFDecompose_SimpleViolation(t *testing.T) {
	allAttrs := []string{"A", "B", "C"}
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
		{Determinant: []string{"B"}, Dependent: []string{"C"}},
	}
	components := BCNFDecompose("tbl", allAttrs, fds)
	if len(components) < 2 {
		t.Fatalf("expected at least 2 components, got %d: %+v", len(components), components)
	}
	// Verify all original attributes are covered
	allCovered := make(map[string]bool)
	for _, comp := range components {
		for _, attr := range comp.Attributes {
			allCovered[attr] = true
		}
	}
	for _, attr := range allAttrs {
		if !allCovered[attr] {
			t.Errorf("attribute %s not covered by any component", attr)
		}
	}
	// Each component should be in BCNF
	for _, comp := range components {
		for _, fd := range comp.FDs {
			if !IsSuperkey(fd.Determinant, comp.Attributes, comp.FDs) {
				// Check if trivial (dependent is subset of determinant)
				trivial := true
				for _, d := range fd.Dependent {
					found := false
					for _, det := range fd.Determinant {
						if d == det {
							found = true
							break
						}
					}
					if !found {
						trivial = false
						break
					}
				}
				if !trivial {
					t.Errorf("component %s has BCNF violation: %v -> %v", comp.Name, fd.Determinant, fd.Dependent)
				}
			}
		}
	}
}

func TestBCNFDecompose_LosesDependency(t *testing.T) {
	allAttrs := []string{"A", "B", "C"}
	fds := []FuncDep{
		{Determinant: []string{"A", "B"}, Dependent: []string{"C"}},
		{Determinant: []string{"C"}, Dependent: []string{"B"}},
	}
	components := BCNFDecompose("tbl", allAttrs, fds)
	if len(components) < 2 {
		t.Fatalf("expected at least 2 components, got %d: %+v", len(components), components)
	}
	// Check that dependency preservation is lost
	preserved, lost := PreservesDependencies(fds, components)
	if preserved {
		t.Error("expected some FDs to be lost in BCNF decomposition of AB->C, C->B")
	}
	// AB->C should be lost: C->B is preserved in {B,C}, but AB->C
	// spans the split (A and B are in different components).
	foundLost := false
	for _, l := range lost {
		if len(l.Determinant) == 2 && l.Determinant[0] == "A" && l.Determinant[1] == "B" {
			for _, d := range l.Dependent {
				if d == "C" {
					foundLost = true
				}
			}
		}
	}
	if !foundLost {
		t.Errorf("expected AB->C to be lost, lost FDs: %+v", lost)
	}
}

// --- IsLosslessJoin tests ---

func TestIsLosslessJoin_Valid(t *testing.T) {
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
		{Determinant: []string{"B"}, Dependent: []string{"C"}},
	}
	r1 := []string{"B", "C"}
	r2 := []string{"A", "B"}
	allAttrs := []string{"A", "B", "C"}
	if !IsLosslessJoin(r1, r2, allAttrs, fds) {
		t.Error("expected lossless join for R1={B,C} R2={A,B} with B->C")
	}
}

func TestIsLosslessJoin_Invalid(t *testing.T) {
	fds := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
	}
	r1 := []string{"A", "B"}
	r2 := []string{"B", "C"}
	allAttrs := []string{"A", "B", "C"}
	if IsLosslessJoin(r1, r2, allAttrs, fds) {
		t.Error("expected lossy join for R1={A,B} R2={B,C} with only A->B")
	}
}

// --- PreservesDependencies tests ---

func TestPreservesDependencies_AllPreserved(t *testing.T) {
	original := []FuncDep{
		{Determinant: []string{"A"}, Dependent: []string{"B"}},
		{Determinant: []string{"B"}, Dependent: []string{"C"}},
	}
	components := []Component{
		{Name: "t1", Attributes: []string{"A", "B"}, FDs: []FuncDep{{Determinant: []string{"A"}, Dependent: []string{"B"}}}},
		{Name: "t2", Attributes: []string{"B", "C"}, FDs: []FuncDep{{Determinant: []string{"B"}, Dependent: []string{"C"}}}},
	}
	preserved, lost := PreservesDependencies(original, components)
	if !preserved {
		t.Errorf("expected all FDs preserved, lost: %+v", lost)
	}
}

func TestPreservesDependencies_LostFDs(t *testing.T) {
	original := []FuncDep{
		{Determinant: []string{"A", "B"}, Dependent: []string{"C"}},
		{Determinant: []string{"C"}, Dependent: []string{"B"}},
	}
	// Simulate a BCNF decomposition that splits into {B,C} and {A,C}
	// AB->C needs both A and B which are split across components
	components := []Component{
		{Name: "t1", Attributes: []string{"B", "C"}, FDs: []FuncDep{{Determinant: []string{"C"}, Dependent: []string{"B"}}}},
		{Name: "t2", Attributes: []string{"A", "C"}, FDs: []FuncDep{}},
	}
	preserved, lost := PreservesDependencies(original, components)
	if preserved {
		t.Error("expected some FDs to be lost")
	}
	if len(lost) == 0 {
		t.Fatal("expected non-empty lost FDs list")
	}
	// AB->C should be lost since A and B are never together in a component
	foundABC := false
	for _, l := range lost {
		detKey := joinAttrs(l.Determinant)
		if detKey == "A,B" {
			foundABC = true
		}
	}
	if !foundABC {
		t.Errorf("expected AB->C to be lost, got lost: %+v", lost)
	}
}

// --- Unexported helper tests ---

func TestSetDifference(t *testing.T) {
	got := setDifference([]string{"A", "B", "C"}, []string{"B"})
	want := []string{"A", "C"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("setDifference = %v, want %v", got, want)
	}
}

func TestSetIntersection(t *testing.T) {
	got := setIntersection([]string{"A", "B", "C"}, []string{"B", "C", "D"})
	want := []string{"B", "C"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("setIntersection = %v, want %v", got, want)
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{42, "42"},
		{123, "123"},
	}
	for _, tt := range tests {
		got := itoa(tt.input)
		if got != tt.want {
			t.Errorf("itoa(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
