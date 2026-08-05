package codegen

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// TestCodegenExcludesImportedTables verifies the fail-closed sweep (roadmap 7.4):
// codegen iterates only owned Tables, so imported reference tables produce no
// generated artifacts (no duplicate types).
func TestCodegenExcludesImportedTables(t *testing.T) {
	testenv.Isolate(t)
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "orders",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid")},
					{Name: "user_id", PGType: typeinfo.MustParse("uuid")},
				},
				PK: []string{"id"},
			},
		},
		ImportedTables: []model.Table{
			{
				Name:   "users",
				Schema: "framework",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("uuid")},
					{Name: "secret_col", PGType: typeinfo.MustParse("text")},
				},
				PK: []string{"id"},
			},
		},
	}
	schema.Canonicalize()

	out, diags := (&GoConstantsGenerator{}).Generate(schema)
	for _, d := range diags {
		if d.Severity.String() == "error" {
			t.Fatalf("unexpected codegen error: %v", d)
		}
	}
	s := string(out)
	if strings.Contains(s, "secret_col") || strings.Contains(s, "\"users\"") {
		t.Errorf("imported table leaked into codegen output:\n%s", s)
	}
	if !strings.Contains(s, "orders") {
		t.Errorf("expected owned table 'orders' in codegen output:\n%s", s)
	}
}
