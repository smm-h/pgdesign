package diff

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// TestDiffExcludesImportedTables verifies the fail-closed sweep (roadmap 7.4):
// imported reference tables live in ImportedTables, not Tables, so diff never
// reports them as added/dropped (migrate would otherwise try to create them).
func TestDiffExcludesImportedTables(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "app", Columns: []model.Column{{Name: "id", PGType: typeinfo.MustParse("uuid")}}, PK: []string{"id"}},
		},
		ImportedTables: []model.Table{
			{Name: "users", Schema: "framework", Columns: []model.Column{{Name: "id", PGType: typeinfo.MustParse("uuid")}}, PK: []string{"id"}},
		},
	}
	desired.Canonicalize()
	// Actual live DB has only orders (imported table already exists in its own schema
	// and is not managed here).
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "app", Columns: []model.Column{{Name: "id", PGType: typeinfo.MustParse("uuid")}}, PK: []string{"id"}},
		},
	}
	actual.Canonicalize()

	d := Diff(desired, actual)
	for _, added := range d.TablesAdded {
		if added == "framework.users" || added == "users" {
			t.Errorf("imported table must not be diffed as added: %v", d.TablesAdded)
		}
	}
}
