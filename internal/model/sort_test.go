package model

import "testing"

func TestSortedFKs(t *testing.T) {
	input := []FK{{Name: "fk_c"}, {Name: "fk_a"}, {Name: "fk_b"}}
	got := SortedFKs(input)

	want := []string{"fk_a", "fk_b", "fk_c"}
	if len(got) != len(want) {
		t.Fatalf("got %d FKs, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("got[%d].Name = %q, want %q", i, got[i].Name, name)
		}
	}

	// Input must not be mutated.
	if input[0].Name != "fk_c" || input[1].Name != "fk_a" || input[2].Name != "fk_b" {
		t.Errorf("input slice was mutated: %v", input)
	}
}

func TestSortedFKsEmpty(t *testing.T) {
	got := SortedFKs(nil)
	if len(got) != 0 {
		t.Errorf("SortedFKs(nil) = %v, want empty", got)
	}
	got = SortedFKs([]FK{})
	if len(got) != 0 {
		t.Errorf("SortedFKs([]) = %v, want empty", got)
	}
}

func TestSortedIndexes(t *testing.T) {
	input := []Index{{Name: "idx_z"}, {Name: "idx_a"}, {Name: "idx_m"}}
	got := SortedIndexes(input)

	want := []string{"idx_a", "idx_m", "idx_z"}
	if len(got) != len(want) {
		t.Fatalf("got %d indexes, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("got[%d].Name = %q, want %q", i, got[i].Name, name)
		}
	}

	// Input must not be mutated.
	if input[0].Name != "idx_z" || input[1].Name != "idx_a" || input[2].Name != "idx_m" {
		t.Errorf("input slice was mutated: %v", input)
	}
}

func TestSortedIndexesEmpty(t *testing.T) {
	got := SortedIndexes(nil)
	if len(got) != 0 {
		t.Errorf("SortedIndexes(nil) = %v, want empty", got)
	}
}
