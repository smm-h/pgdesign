package diff

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/risk"
)

func TestEmptyDiff(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", NotNull: true},
				{Name: "name", PGType: "text", NotNull: false},
			}},
		},
	}
	// Same schema on both sides.
	d := Diff(schema, schema)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff, got: %s", d.Summary())
	}
	if d.Summary() != "no changes" {
		t.Errorf("expected 'no changes', got: %q", d.Summary())
	}
}

func TestTableAdded(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public"},
			{Name: "posts", Schema: "public"},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public"},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.TablesAdded) != 1 || d.TablesAdded[0] != "posts" {
		t.Errorf("expected posts added, got: %v", d.TablesAdded)
	}
	if len(d.TablesRemoved) != 0 {
		t.Errorf("expected no tables removed, got: %v", d.TablesRemoved)
	}
}

func TestTableRemoved(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public"},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public"},
			{Name: "posts", Schema: "public"},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.TablesRemoved) != 1 || d.TablesRemoved[0] != "posts" {
		t.Errorf("expected posts removed, got: %v", d.TablesRemoved)
	}
}

func TestColumnAdded(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", NotNull: true},
				{Name: "email", PGType: "text", NotNull: true},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", NotNull: true},
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if len(td.ColumnsAdded) != 1 || td.ColumnsAdded[0].Name != "email" {
		t.Errorf("expected email column added, got: %v", td.ColumnsAdded)
	}
}

func TestColumnRemoved(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", NotNull: true},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", NotNull: true},
				{Name: "email", PGType: "text", NotNull: true},
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if len(td.ColumnsRemoved) != 1 || td.ColumnsRemoved[0] != "email" {
		t.Errorf("expected email column removed, got: %v", td.ColumnsRemoved)
	}
}

func TestColumnTypeChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", NotNull: true},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if len(td.ColumnsChanged) != 1 {
		t.Fatalf("expected 1 column changed, got %d", len(td.ColumnsChanged))
	}
	cc := td.ColumnsChanged[0]
	if cc.Name != "id" {
		t.Errorf("expected column 'id', got %q", cc.Name)
	}
	if cc.TypeChanged == nil {
		t.Fatal("expected TypeChanged to be set")
	}
	if cc.TypeChanged[0] != "integer" || cc.TypeChanged[1] != "bigint" {
		t.Errorf("expected [integer, bigint], got [%s, %s]", cc.TypeChanged[0], cc.TypeChanged[1])
	}
	// int -> bigint is widening, so risk should be Caution (not Dangerous).
	if cc.Risk.RiskLevel != risk.Caution {
		t.Errorf("expected Caution risk for int->bigint widening, got %s", cc.Risk.RiskLevel)
	}
}

func TestColumnTypeChangedNarrowing(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", NotNull: true},
			}},
		},
	}
	d := Diff(desired, actual)
	cc := d.TablesChanged[0].ColumnsChanged[0]
	// bigint -> integer is narrowing, so risk should be Dangerous.
	if cc.Risk.RiskLevel != risk.Dangerous {
		t.Errorf("expected Dangerous risk for bigint->integer narrowing, got %s", cc.Risk.RiskLevel)
	}
}

func TestNullableChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "name", PGType: "text", NotNull: true},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "name", PGType: "text", NotNull: false},
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	cc := d.TablesChanged[0].ColumnsChanged[0]
	if cc.NullableChanged == nil {
		t.Fatal("expected NullableChanged to be set")
	}
	// nullable -> NOT NULL = Caution
	if cc.Risk.RiskLevel != risk.Caution {
		t.Errorf("expected Caution for nullable->NOT NULL, got %s", cc.Risk.RiskLevel)
	}
}

func TestNullableChangedDropNotNull(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "name", PGType: "text", NotNull: false},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "name", PGType: "text", NotNull: true},
			}},
		},
	}
	d := Diff(desired, actual)
	cc := d.TablesChanged[0].ColumnsChanged[0]
	// NOT NULL -> nullable = Safe
	if cc.Risk.RiskLevel != risk.Safe {
		t.Errorf("expected Safe for NOT NULL->nullable, got %s", cc.Risk.RiskLevel)
	}
}

func TestFKAdded(t *testing.T) {
	fk := model.FK{
		Name:       "fk_user_id",
		Columns:    []string{"user_id"},
		RefTable:   "users",
		RefColumns: []string{"id"},
		OnDelete:   "CASCADE",
	}
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "posts", Schema: "public", FKs: []model.FK{fk}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "posts", Schema: "public"},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if len(td.FKsAdded) != 1 || td.FKsAdded[0].Name != "fk_user_id" {
		t.Errorf("expected fk_user_id added, got: %v", td.FKsAdded)
	}
}

func TestFKRemoved(t *testing.T) {
	fk := model.FK{
		Name:       "fk_user_id",
		Columns:    []string{"user_id"},
		RefTable:   "users",
		RefColumns: []string{"id"},
	}
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "posts", Schema: "public"},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "posts", Schema: "public", FKs: []model.FK{fk}},
		},
	}
	d := Diff(desired, actual)
	td := d.TablesChanged[0]
	if len(td.FKsRemoved) != 1 || td.FKsRemoved[0] != "fk_user_id" {
		t.Errorf("expected fk_user_id removed, got: %v", td.FKsRemoved)
	}
}

func TestEnumValuesAdded(t *testing.T) {
	desired := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active", "inactive", "suspended"}},
		},
	}
	actual := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active", "inactive"}},
		},
	}
	d := Diff(desired, actual)
	if len(d.EnumsChanged) != 1 {
		t.Fatalf("expected 1 enum changed, got %d", len(d.EnumsChanged))
	}
	ec := d.EnumsChanged[0]
	if len(ec.ValuesAdded) != 1 || ec.ValuesAdded[0] != "suspended" {
		t.Errorf("expected 'suspended' added, got: %v", ec.ValuesAdded)
	}
	if len(ec.ValuesRemoved) != 0 {
		t.Errorf("expected no values removed, got: %v", ec.ValuesRemoved)
	}
}

func TestEnumValuesRemoved(t *testing.T) {
	desired := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active"}},
		},
	}
	actual := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active", "inactive"}},
		},
	}
	d := Diff(desired, actual)
	if len(d.EnumsChanged) != 1 {
		t.Fatalf("expected 1 enum changed, got %d", len(d.EnumsChanged))
	}
	ec := d.EnumsChanged[0]
	if len(ec.ValuesRemoved) != 1 || ec.ValuesRemoved[0] != "inactive" {
		t.Errorf("expected 'inactive' removed, got: %v", ec.ValuesRemoved)
	}
}

func TestSummaryOutput(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "email", PGType: "text"},
			}},
			{Name: "posts", Schema: "public"},
		},
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active", "inactive", "suspended"}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "integer"},
			}},
			{Name: "old_table", Schema: "public"},
		},
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active", "inactive"}},
		},
	}
	d := Diff(desired, actual)
	summary := d.Summary()

	// Should mention tables added, removed, changed, and enums changed.
	if !strings.Contains(summary, "table(s) added") {
		t.Errorf("summary should mention tables added: %q", summary)
	}
	if !strings.Contains(summary, "table(s) removed") {
		t.Errorf("summary should mention tables removed: %q", summary)
	}
	if !strings.Contains(summary, "table(s) changed") {
		t.Errorf("summary should mention tables changed: %q", summary)
	}
	if !strings.Contains(summary, "column(s) modified") {
		t.Errorf("summary should mention columns modified: %q", summary)
	}
	if !strings.Contains(summary, "enum(s) changed") {
		t.Errorf("summary should mention enums changed: %q", summary)
	}
}

func TestExtensionsAdded(t *testing.T) {
	desired := &model.Schema{
		Extensions: []string{"uuid-ossp", "pgcrypto"},
	}
	actual := &model.Schema{
		Extensions: []string{"uuid-ossp"},
	}
	d := Diff(desired, actual)
	if len(d.ExtensionsAdded) != 1 || d.ExtensionsAdded[0] != "pgcrypto" {
		t.Errorf("expected pgcrypto added, got: %v", d.ExtensionsAdded)
	}
}

func TestExtensionsRemoved(t *testing.T) {
	desired := &model.Schema{
		Extensions: []string{"uuid-ossp"},
	}
	actual := &model.Schema{
		Extensions: []string{"uuid-ossp", "pgcrypto"},
	}
	d := Diff(desired, actual)
	if len(d.ExtensionsRemoved) != 1 || d.ExtensionsRemoved[0] != "pgcrypto" {
		t.Errorf("expected pgcrypto removed, got: %v", d.ExtensionsRemoved)
	}
}

func TestDefaultChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "status", PGType: "text", Default: "active"},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "status", PGType: "text", Default: "inactive"},
			}},
		},
	}
	d := Diff(desired, actual)
	cc := d.TablesChanged[0].ColumnsChanged[0]
	if cc.DefaultChanged == nil {
		t.Fatal("expected DefaultChanged to be set")
	}
	if cc.DefaultChanged[0] != "inactive" || cc.DefaultChanged[1] != "active" {
		t.Errorf("expected [inactive, active], got [%s, %s]", cc.DefaultChanged[0], cc.DefaultChanged[1])
	}
	// Default changes are safe.
	if cc.Risk.RiskLevel != risk.Safe {
		t.Errorf("expected Safe for default change, got %s", cc.Risk.RiskLevel)
	}
}

func TestCommentChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Comment: "new comment"},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Comment: "old comment"},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if td.CommentChanged == nil {
		t.Fatal("expected CommentChanged to be set")
	}
	if td.CommentChanged[0] != "old comment" || td.CommentChanged[1] != "new comment" {
		t.Errorf("expected [old comment, new comment], got %v", td.CommentChanged)
	}
}

func TestPKChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", PK: []string{"id", "tenant_id"}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", PK: []string{"id"}},
		},
	}
	d := Diff(desired, actual)
	td := d.TablesChanged[0]
	if td.PKChanged == nil {
		t.Fatal("expected PKChanged to be set")
	}
	if !sliceEqual(td.PKChanged[0], []string{"id"}) || !sliceEqual(td.PKChanged[1], []string{"id", "tenant_id"}) {
		t.Errorf("unexpected PKChanged: %v", td.PKChanged)
	}
}

func TestIsWidening(t *testing.T) {
	tests := []struct {
		old, new string
		want     bool
	}{
		{"integer", "bigint", true},
		{"int", "bigint", true},
		{"int4", "int8", true},
		{"smallint", "integer", true},
		{"smallint", "bigint", true},
		{"varchar(50)", "varchar(100)", true},
		{"character varying(50)", "character varying(100)", true},
		{"varchar(50)", "text", true},
		{"real", "double precision", true},
		// Not widening:
		{"bigint", "integer", false},
		{"varchar(100)", "varchar(50)", false},
		{"text", "varchar(50)", false},
		{"integer", "text", false},
	}
	for _, tt := range tests {
		got := isWidening(tt.old, tt.new)
		if got != tt.want {
			t.Errorf("isWidening(%q, %q) = %v, want %v", tt.old, tt.new, got, tt.want)
		}
	}
}

func TestIndexAdded(t *testing.T) {
	idx := model.Index{
		Name:    "idx_users_email",
		Columns: []string{"email"},
		Method:  "btree",
	}
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Indexes: []model.Index{idx}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public"},
		},
	}
	d := Diff(desired, actual)
	td := d.TablesChanged[0]
	if len(td.IndexesAdded) != 1 || td.IndexesAdded[0].Name != "idx_users_email" {
		t.Errorf("expected idx_users_email added, got: %v", td.IndexesAdded)
	}
}

func TestIndexRemoved(t *testing.T) {
	idx := model.Index{
		Name:    "idx_users_email",
		Columns: []string{"email"},
		Method:  "btree",
	}
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public"},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Indexes: []model.Index{idx}},
		},
	}
	d := Diff(desired, actual)
	td := d.TablesChanged[0]
	if len(td.IndexesRemoved) != 1 || td.IndexesRemoved[0] != "idx_users_email" {
		t.Errorf("expected idx_users_email removed, got: %v", td.IndexesRemoved)
	}
}

func TestSchemaQualifiedTableKey(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "auth"},
		},
	}
	actual := &model.Schema{}
	d := Diff(desired, actual)
	if len(d.TablesAdded) != 1 || d.TablesAdded[0] != "auth.users" {
		t.Errorf("expected 'auth.users', got: %v", d.TablesAdded)
	}
}

func TestEnumAdded(t *testing.T) {
	desired := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active", "inactive"}},
		},
	}
	actual := &model.Schema{}
	d := Diff(desired, actual)
	if len(d.EnumsAdded) != 1 || d.EnumsAdded[0] != "status" {
		t.Errorf("expected status enum added, got: %v", d.EnumsAdded)
	}
}

func TestEnumRemoved(t *testing.T) {
	desired := &model.Schema{}
	actual := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active", "inactive"}},
		},
	}
	d := Diff(desired, actual)
	if len(d.EnumsRemoved) != 1 || d.EnumsRemoved[0] != "status" {
		t.Errorf("expected status enum removed, got: %v", d.EnumsRemoved)
	}
}

func TestFKChanged(t *testing.T) {
	oldFK := model.FK{
		Name:       "fk_user_id",
		Columns:    []string{"user_id"},
		RefTable:   "users",
		RefColumns: []string{"id"},
		OnDelete:   "RESTRICT",
	}
	newFK := model.FK{
		Name:       "fk_user_id",
		Columns:    []string{"user_id"},
		RefTable:   "users",
		RefColumns: []string{"id"},
		OnDelete:   "CASCADE",
	}
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "posts", Schema: "public", FKs: []model.FK{newFK}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "posts", Schema: "public", FKs: []model.FK{oldFK}},
		},
	}
	d := Diff(desired, actual)
	td := d.TablesChanged[0]
	if len(td.FKsChanged) != 1 {
		t.Fatalf("expected 1 FK changed, got %d", len(td.FKsChanged))
	}
	fc := td.FKsChanged[0]
	if fc.Old.OnDelete != "RESTRICT" || fc.New.OnDelete != "CASCADE" {
		t.Errorf("expected OnDelete RESTRICT->CASCADE, got %s->%s", fc.Old.OnDelete, fc.New.OnDelete)
	}
}

func TestIndexChanged(t *testing.T) {
	oldIdx := model.Index{
		Name:    "idx_users_email",
		Columns: []string{"email"},
		Method:  "btree",
	}
	newIdx := model.Index{
		Name:    "idx_users_email",
		Columns: []string{"email"},
		Method:  "hash",
	}
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Indexes: []model.Index{newIdx}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Indexes: []model.Index{oldIdx}},
		},
	}
	d := Diff(desired, actual)
	td := d.TablesChanged[0]
	if len(td.IndexesChanged) != 1 {
		t.Fatalf("expected 1 index changed, got %d", len(td.IndexesChanged))
	}
	ic := td.IndexesChanged[0]
	if ic.Old.Method != "btree" || ic.New.Method != "hash" {
		t.Errorf("expected method btree->hash, got %s->%s", ic.Old.Method, ic.New.Method)
	}
}

func TestColumnCommentChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", Comment: "Primary key"},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", Comment: ""},
			}},
		},
	}
	d := Diff(desired, actual)
	cc := d.TablesChanged[0].ColumnsChanged[0]
	if cc.CommentChanged == nil {
		t.Fatal("expected CommentChanged to be set")
	}
	if cc.CommentChanged[0] != "" || cc.CommentChanged[1] != "Primary key" {
		t.Errorf("expected ['', 'Primary key'], got [%q, %q]", cc.CommentChanged[0], cc.CommentChanged[1])
	}
}

func TestVarcharWidening(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "name", PGType: "varchar(200)"},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "name", PGType: "varchar(100)"},
			}},
		},
	}
	d := Diff(desired, actual)
	cc := d.TablesChanged[0].ColumnsChanged[0]
	// varchar(100) -> varchar(200) is widening, so Caution.
	if cc.Risk.RiskLevel != risk.Caution {
		t.Errorf("expected Caution for varchar widening, got %s", cc.Risk.RiskLevel)
	}
}

func TestMultipleChangesHighestRisk(t *testing.T) {
	// Both type narrowing (Dangerous) and nullable change (Caution).
	// Should report Dangerous (highest).
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", NotNull: false},
			}},
		},
	}
	d := Diff(desired, actual)
	cc := d.TablesChanged[0].ColumnsChanged[0]
	if cc.Risk.RiskLevel != risk.Dangerous {
		t.Errorf("expected Dangerous (highest risk), got %s", cc.Risk.RiskLevel)
	}
}

func TestOwnerChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Owner: "app_user"},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Owner: "postgres"},
		},
	}
	d := Diff(desired, actual)
	td := d.TablesChanged[0]
	if td.OwnerChanged == nil {
		t.Fatal("expected OwnerChanged to be set")
	}
	if td.OwnerChanged[0] != "postgres" || td.OwnerChanged[1] != "app_user" {
		t.Errorf("expected [postgres, app_user], got %v", td.OwnerChanged)
	}
}
