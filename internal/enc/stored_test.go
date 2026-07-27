package enc

import (
	"bytes"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// TestStoredNotEncodedForNonGeneratedColumn is the regression for the 1.4
// Column.Stored identity-noise finding: Stored is only semantic for GENERATED
// columns. A user CAN set stored=true on a plain column (no validate rule
// forbids it), which had zero DDL effect yet flipped the revision. The encoder
// must normalize Stored out of identity when Generated is empty, so two
// otherwise-identical columns encode to the same bytes regardless of Stored.
func TestStoredNotEncodedForNonGeneratedColumn(t *testing.T) {
	mkTable := func(stored bool) model.Table {
		return model.Table{
			Name:    "users",
			Comment: "u",
			Columns: []model.Column{{
				Name:    "id",
				PGType:  typeinfo.T("int4"),
				NotNull: true,
				// Generated intentionally empty: a plain column.
				Stored: stored,
			}},
			PK: []string{"id"},
		}
	}

	withStored, err := EncodeTable(mkTable(true))
	if err != nil {
		t.Fatalf("EncodeTable(stored=true): %v", err)
	}
	withoutStored, err := EncodeTable(mkTable(false))
	if err != nil {
		t.Fatalf("EncodeTable(stored=false): %v", err)
	}
	if !bytes.Equal(withStored, withoutStored) {
		t.Fatalf("Stored on a non-generated column entered identity (bytes differ):\nstored=true:  %s\nstored=false: %s", withStored, withoutStored)
	}
}

// TestStoredEncodedForGeneratedColumn confirms the normalization is scoped: on a
// GENERATED column, STORED vs VIRTUAL is semantic and MUST still be encoded.
func TestStoredEncodedForGeneratedColumn(t *testing.T) {
	mkTable := func(stored bool) model.Table {
		return model.Table{
			Name:    "users",
			Comment: "u",
			Columns: []model.Column{{
				Name:      "total",
				PGType:    typeinfo.T("int4"),
				NotNull:   true,
				Generated: "a + b",
				Stored:    stored,
			}},
			PK: []string{"total"},
		}
	}
	storedForm, err := EncodeTable(mkTable(true))
	if err != nil {
		t.Fatalf("EncodeTable(stored=true): %v", err)
	}
	virtualForm, err := EncodeTable(mkTable(false))
	if err != nil {
		t.Fatalf("EncodeTable(stored=false): %v", err)
	}
	if bytes.Equal(storedForm, virtualForm) {
		t.Fatal("STORED vs VIRTUAL on a generated column must be encoded (bytes should differ)")
	}
}
