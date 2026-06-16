package diff

import (
	"testing"
)

type item struct {
	id   string
	data int
}

func itemKey(i item) string { return i.id }

func TestMatchObjects_Empty(t *testing.T) {
	added, removed, matched := matchObjects([]item{}, []item{}, itemKey)
	if len(added) != 0 {
		t.Errorf("expected no added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected no removed, got %d", len(removed))
	}
	if len(matched) != 0 {
		t.Errorf("expected no matched, got %d", len(matched))
	}
}

func TestMatchObjects_AllAdded(t *testing.T) {
	desired := []item{{id: "A", data: 1}, {id: "B", data: 2}}
	added, removed, matched := matchObjects(desired, []item{}, itemKey)
	if len(added) != 2 {
		t.Fatalf("expected 2 added, got %d", len(added))
	}
	if added[0].id != "A" || added[1].id != "B" {
		t.Errorf("added = %v, want [A, B]", added)
	}
	if len(removed) != 0 {
		t.Errorf("expected no removed, got %d", len(removed))
	}
	if len(matched) != 0 {
		t.Errorf("expected no matched, got %d", len(matched))
	}
}

func TestMatchObjects_AllRemoved(t *testing.T) {
	actual := []item{{id: "A", data: 1}, {id: "B", data: 2}}
	added, removed, matched := matchObjects([]item{}, actual, itemKey)
	if len(added) != 0 {
		t.Errorf("expected no added, got %d", len(added))
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed, got %d", len(removed))
	}
	if removed[0].id != "A" || removed[1].id != "B" {
		t.Errorf("removed = %v, want [A, B]", removed)
	}
	if len(matched) != 0 {
		t.Errorf("expected no matched, got %d", len(matched))
	}
}

func TestMatchObjects_AllMatched(t *testing.T) {
	desired := []item{{id: "A", data: 1}, {id: "B", data: 2}}
	actual := []item{{id: "A", data: 10}, {id: "B", data: 20}}
	added, removed, matched := matchObjects(desired, actual, itemKey)
	if len(added) != 0 {
		t.Errorf("expected no added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected no removed, got %d", len(removed))
	}
	if len(matched) != 2 {
		t.Fatalf("expected 2 matched, got %d", len(matched))
	}
	if matched[0].Desired.id != "A" || matched[0].Actual.data != 10 {
		t.Errorf("matched[0] = %+v, want Desired.id=A, Actual.data=10", matched[0])
	}
	if matched[1].Desired.id != "B" || matched[1].Actual.data != 20 {
		t.Errorf("matched[1] = %+v, want Desired.id=B, Actual.data=20", matched[1])
	}
}

func TestMatchObjects_Mixed(t *testing.T) {
	desired := []item{{id: "A", data: 1}, {id: "B", data: 2}, {id: "C", data: 3}}
	actual := []item{{id: "B", data: 20}, {id: "C", data: 30}, {id: "D", data: 40}}
	added, removed, matched := matchObjects(desired, actual, itemKey)
	if len(added) != 1 || added[0].id != "A" {
		t.Errorf("added = %v, want [A]", added)
	}
	if len(removed) != 1 || removed[0].id != "D" {
		t.Errorf("removed = %v, want [D]", removed)
	}
	if len(matched) != 2 {
		t.Fatalf("expected 2 matched, got %d", len(matched))
	}
	if matched[0].Desired.id != "B" || matched[1].Desired.id != "C" {
		t.Errorf("matched = %+v, want [B, C]", matched)
	}
}

func TestMatchObjects_PreservesDesiredOrder(t *testing.T) {
	desired := []item{{id: "C"}, {id: "A"}, {id: "D"}}
	actual := []item{{id: "Z"}}
	added, _, _ := matchObjects(desired, actual, itemKey)
	if len(added) != 3 {
		t.Fatalf("expected 3 added, got %d", len(added))
	}
	if added[0].id != "C" || added[1].id != "A" || added[2].id != "D" {
		t.Errorf("added order = [%s, %s, %s], want [C, A, D]", added[0].id, added[1].id, added[2].id)
	}
}

func TestMatchObjects_PreservesActualOrderForRemoved(t *testing.T) {
	actual := []item{{id: "C"}, {id: "A"}, {id: "D"}}
	desired := []item{{id: "Z"}}
	_, removed, _ := matchObjects(desired, actual, itemKey)
	if len(removed) != 3 {
		t.Fatalf("expected 3 removed, got %d", len(removed))
	}
	if removed[0].id != "C" || removed[1].id != "A" || removed[2].id != "D" {
		t.Errorf("removed order = [%s, %s, %s], want [C, A, D]", removed[0].id, removed[1].id, removed[2].id)
	}
}

func TestMatchObjects_DuplicateKeys(t *testing.T) {
	// If actual has duplicate keys, last one wins in map.
	desired := []item{{id: "A", data: 1}}
	actual := []item{{id: "A", data: 10}, {id: "A", data: 20}}
	_, _, matched := matchObjects(desired, actual, itemKey)
	if len(matched) != 1 {
		t.Fatalf("expected 1 matched, got %d", len(matched))
	}
	// Last entry in actual with key "A" should win.
	if matched[0].Actual.data != 20 {
		t.Errorf("matched[0].Actual.data = %d, want 20 (last entry wins)", matched[0].Actual.data)
	}
}
