package diff

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

func fastPathSample() *model.Schema {
	s := &model.Schema{
		Name: "shop",
		Tables: []model.Table{
			{
				Name:    "users",
				Comment: "users",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}
	s.Canonicalize()
	return s
}

// TestDiffAgainstItselfEmpty PINS diff(a,a) = empty (roadmap 1.4 / L10
// corollary).
func TestDiffAgainstItselfEmpty(t *testing.T) {
	testenv.Isolate(t)
	s := fastPathSample()
	if d := Diff(s, s); !d.IsEmpty() {
		t.Fatalf("diff(a,a) not empty: %s", d.Summary())
	}
}

// TestChangedObjectKeysFastPath: the fast path returns no changed keys for
// identical models and names the changed object when one differs.
func TestChangedObjectKeysFastPath(t *testing.T) {
	testenv.Isolate(t)
	a := fastPathSample()
	ck, err := ChangedObjectKeys(a, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(ck) != 0 {
		t.Fatalf("ChangedObjectKeys(a,a) should be empty, got %v", ck)
	}

	// Change the table comment -> the table object id changes.
	b := fastPathSample()
	b.Tables[0].Comment = "different"
	b.Canonicalize()
	ck, err = ChangedObjectKeys(a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := enc.Key{Kind: enc.KindTable, Name: "users"}
	found := false
	for _, k := range ck {
		if k == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected changed key %s, got %v", want, ck)
	}
}
