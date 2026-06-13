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
		got := IsWidening(tt.old, tt.new)
		if got != tt.want {
			t.Errorf("IsWidening(%q, %q) = %v, want %v", tt.old, tt.new, got, tt.want)
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

func TestGeneratedChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "public", Columns: []model.Column{
				{Name: "total", PGType: "numeric", Generated: "STORED"},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "public", Columns: []model.Column{
				{Name: "total", PGType: "numeric", Generated: ""},
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	cc := d.TablesChanged[0].ColumnsChanged[0]
	if cc.GeneratedChanged == nil {
		t.Fatal("expected GeneratedChanged to be set")
	}
	if cc.GeneratedChanged[0] != "" || cc.GeneratedChanged[1] != "STORED" {
		t.Errorf("expected ['', 'STORED'], got [%q, %q]", cc.GeneratedChanged[0], cc.GeneratedChanged[1])
	}
}

func TestIdentityChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", Identity: "BY DEFAULT"},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", Identity: "ALWAYS"},
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	cc := d.TablesChanged[0].ColumnsChanged[0]
	if cc.IdentityChanged == nil {
		t.Fatal("expected IdentityChanged to be set")
	}
	if cc.IdentityChanged[0] != "ALWAYS" || cc.IdentityChanged[1] != "BY DEFAULT" {
		t.Errorf("expected ['ALWAYS', 'BY DEFAULT'], got [%q, %q]", cc.IdentityChanged[0], cc.IdentityChanged[1])
	}
}

func TestGeneratedAndIdentityUnchanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", Identity: "ALWAYS", Generated: "STORED"},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "id", PGType: "bigint", Identity: "ALWAYS", Generated: "STORED"},
			}},
		},
	}
	d := Diff(desired, actual)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff when generated and identity are unchanged, got: %s", d.Summary())
	}
}

func TestEnumValueAppendedAtEnd(t *testing.T) {
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

	// Backward compat: ValuesAdded still populated.
	if len(ec.ValuesAdded) != 1 || ec.ValuesAdded[0] != "suspended" {
		t.Errorf("expected ValuesAdded=[suspended], got %v", ec.ValuesAdded)
	}

	// Position-aware: appended at end.
	if len(ec.ValuesAddedAtEnd) != 1 || ec.ValuesAddedAtEnd[0] != "suspended" {
		t.Errorf("expected ValuesAddedAtEnd=[suspended], got %v", ec.ValuesAddedAtEnd)
	}
	if len(ec.ValuesInserted) != 0 {
		t.Errorf("expected no ValuesInserted, got %v", ec.ValuesInserted)
	}
	if ec.Reordered {
		t.Error("expected Reordered=false")
	}
}

func TestEnumValueInsertedInMiddle(t *testing.T) {
	desired := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active", "pending", "inactive"}},
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

	// Backward compat.
	if len(ec.ValuesAdded) != 1 || ec.ValuesAdded[0] != "pending" {
		t.Errorf("expected ValuesAdded=[pending], got %v", ec.ValuesAdded)
	}

	// Position-aware: inserted in middle.
	if len(ec.ValuesAddedAtEnd) != 0 {
		t.Errorf("expected no ValuesAddedAtEnd, got %v", ec.ValuesAddedAtEnd)
	}
	if len(ec.ValuesInserted) != 1 {
		t.Fatalf("expected 1 ValuesInserted, got %d", len(ec.ValuesInserted))
	}
	ins := ec.ValuesInserted[0]
	if ins.Value != "pending" {
		t.Errorf("expected inserted Value=pending, got %q", ins.Value)
	}
	if ins.After != "active" {
		t.Errorf("expected After=active, got %q", ins.After)
	}
	if ec.Reordered {
		t.Error("expected Reordered=false")
	}
}

func TestEnumValueInsertedBeforeFirst(t *testing.T) {
	desired := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"draft", "active", "inactive"}},
		},
	}
	actual := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active", "inactive"}},
		},
	}
	d := Diff(desired, actual)
	ec := d.EnumsChanged[0]

	if len(ec.ValuesInserted) != 1 {
		t.Fatalf("expected 1 ValuesInserted, got %d", len(ec.ValuesInserted))
	}
	ins := ec.ValuesInserted[0]
	if ins.Value != "draft" {
		t.Errorf("expected inserted Value=draft, got %q", ins.Value)
	}
	if ins.After != "" {
		t.Errorf("expected After=\"\" (before first), got %q", ins.After)
	}
}

func TestEnumValuesReordered(t *testing.T) {
	desired := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"inactive", "active"}},
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

	// No additions or removals.
	if len(ec.ValuesAdded) != 0 {
		t.Errorf("expected no ValuesAdded, got %v", ec.ValuesAdded)
	}
	if len(ec.ValuesRemoved) != 0 {
		t.Errorf("expected no ValuesRemoved, got %v", ec.ValuesRemoved)
	}
	if !ec.Reordered {
		t.Error("expected Reordered=true")
	}
}

func TestEnumValueRemovedStillPopulated(t *testing.T) {
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
	ec := d.EnumsChanged[0]

	if len(ec.ValuesRemoved) != 1 || ec.ValuesRemoved[0] != "inactive" {
		t.Errorf("expected ValuesRemoved=[inactive], got %v", ec.ValuesRemoved)
	}
	if len(ec.ValuesAddedAtEnd) != 0 {
		t.Errorf("expected no ValuesAddedAtEnd, got %v", ec.ValuesAddedAtEnd)
	}
	if len(ec.ValuesInserted) != 0 {
		t.Errorf("expected no ValuesInserted, got %v", ec.ValuesInserted)
	}
	if ec.Reordered {
		t.Error("expected Reordered=false")
	}
}

func TestEnumMixedInsertAndAppend(t *testing.T) {
	// Old: [a, c]
	// New: [a, b, c, d]
	// "b" is inserted (after "a"), "d" is appended at end.
	desired := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"a", "b", "c", "d"}},
		},
	}
	actual := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"a", "c"}},
		},
	}
	d := Diff(desired, actual)
	ec := d.EnumsChanged[0]

	if len(ec.ValuesInserted) != 1 {
		t.Fatalf("expected 1 ValuesInserted, got %d", len(ec.ValuesInserted))
	}
	if ec.ValuesInserted[0].Value != "b" || ec.ValuesInserted[0].After != "a" {
		t.Errorf("expected inserted b after a, got %+v", ec.ValuesInserted[0])
	}
	if len(ec.ValuesAddedAtEnd) != 1 || ec.ValuesAddedAtEnd[0] != "d" {
		t.Errorf("expected ValuesAddedAtEnd=[d], got %v", ec.ValuesAddedAtEnd)
	}
	if ec.Reordered {
		t.Error("expected Reordered=false")
	}
}

func TestEnumReorderedWithAdditions(t *testing.T) {
	// Old: [a, b, c]
	// New: [c, b, a, d]
	// Reordered (a,b,c -> c,b,a) and d appended.
	desired := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"c", "b", "a", "d"}},
		},
	}
	actual := &model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"a", "b", "c"}},
		},
	}
	d := Diff(desired, actual)
	ec := d.EnumsChanged[0]

	if !ec.Reordered {
		t.Error("expected Reordered=true")
	}
	if len(ec.ValuesAdded) != 1 || ec.ValuesAdded[0] != "d" {
		t.Errorf("expected ValuesAdded=[d], got %v", ec.ValuesAdded)
	}
}

func TestEnumFormatTerminalAppended(t *testing.T) {
	d := &SchemaDiff{
		EnumsChanged: []EnumDiff{
			{
				Name:             "status",
				ValuesAdded:      []string{"suspended"},
				ValuesAddedAtEnd: []string{"suspended"},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, "safe, appended") {
		t.Errorf("expected 'safe, appended' in output, got:\n%s", out)
	}
}

func TestEnumFormatTerminalInserted(t *testing.T) {
	d := &SchemaDiff{
		EnumsChanged: []EnumDiff{
			{
				Name:        "status",
				ValuesAdded: []string{"pending"},
				ValuesInserted: []EnumValueInsert{
					{Value: "pending", After: "active"},
				},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, "requires BEFORE/AFTER") {
		t.Errorf("expected 'requires BEFORE/AFTER' in output, got:\n%s", out)
	}
	if !strings.Contains(out, `after "active"`) {
		t.Errorf("expected 'after \"active\"' in output, got:\n%s", out)
	}
}

func TestEnumFormatTerminalReordered(t *testing.T) {
	d := &SchemaDiff{
		EnumsChanged: []EnumDiff{
			{
				Name:      "status",
				Reordered: true,
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, "values reordered (dangerous)") {
		t.Errorf("expected 'values reordered (dangerous)' in output, got:\n%s", out)
	}
}

func TestPartitioningGained(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public", Partitioning: &model.PartitionSpec{
				Strategy: "range",
				Column:   "created_at",
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public"},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if td.PartitioningChanged == nil {
		t.Fatal("expected PartitioningChanged to be set")
	}
	pd := td.PartitioningChanged
	if pd.StrategyChanged == nil {
		t.Fatal("expected StrategyChanged to be set")
	}
	if pd.StrategyChanged[0] != "" || pd.StrategyChanged[1] != "range" {
		t.Errorf("expected ['', 'range'], got [%q, %q]", pd.StrategyChanged[0], pd.StrategyChanged[1])
	}
	if pd.KeyChanged == nil {
		t.Fatal("expected KeyChanged to be set")
	}
	if pd.KeyChanged[0] != "" || pd.KeyChanged[1] != "created_at" {
		t.Errorf("expected ['', 'created_at'], got [%q, %q]", pd.KeyChanged[0], pd.KeyChanged[1])
	}
}

func TestPartitioningLost(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public"},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public", Partitioning: &model.PartitionSpec{
				Strategy: "range",
				Column:   "created_at",
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	pd := d.TablesChanged[0].PartitioningChanged
	if pd == nil {
		t.Fatal("expected PartitioningChanged to be set")
	}
	if pd.StrategyChanged == nil {
		t.Fatal("expected StrategyChanged to be set")
	}
	if pd.StrategyChanged[0] != "range" || pd.StrategyChanged[1] != "" {
		t.Errorf("expected ['range', ''], got [%q, %q]", pd.StrategyChanged[0], pd.StrategyChanged[1])
	}
}

func TestPartitioningStrategyChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public", Partitioning: &model.PartitionSpec{
				Strategy: "hash",
				Column:   "id",
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public", Partitioning: &model.PartitionSpec{
				Strategy: "range",
				Column:   "id",
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	pd := d.TablesChanged[0].PartitioningChanged
	if pd == nil {
		t.Fatal("expected PartitioningChanged to be set")
	}
	if pd.StrategyChanged == nil {
		t.Fatal("expected StrategyChanged to be set")
	}
	if pd.StrategyChanged[0] != "range" || pd.StrategyChanged[1] != "hash" {
		t.Errorf("expected ['range', 'hash'], got [%q, %q]", pd.StrategyChanged[0], pd.StrategyChanged[1])
	}
	// Key unchanged, so KeyChanged should be nil.
	if pd.KeyChanged != nil {
		t.Errorf("expected KeyChanged to be nil, got %v", pd.KeyChanged)
	}
}

func TestPartitionChildrenAdded(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public", Partitioning: &model.PartitionSpec{
				Strategy: "range",
				Column:   "created_at",
				Children: []model.PartitionSpec{
					{Strategy: "events_2024", Column: "2024-01-01"},
					{Strategy: "events_2025", Column: "2025-01-01"},
				},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public", Partitioning: &model.PartitionSpec{
				Strategy: "range",
				Column:   "created_at",
				Children: []model.PartitionSpec{
					{Strategy: "events_2024", Column: "2024-01-01"},
				},
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	pd := d.TablesChanged[0].PartitioningChanged
	if pd == nil {
		t.Fatal("expected PartitioningChanged to be set")
	}
	// Strategy and key unchanged.
	if pd.StrategyChanged != nil {
		t.Errorf("expected StrategyChanged to be nil, got %v", pd.StrategyChanged)
	}
	if pd.KeyChanged != nil {
		t.Errorf("expected KeyChanged to be nil, got %v", pd.KeyChanged)
	}
	if len(pd.ChildrenAdded) != 1 || pd.ChildrenAdded[0] != "events_2025:2025-01-01" {
		t.Errorf("expected ['events_2025:2025-01-01'] added, got: %v", pd.ChildrenAdded)
	}
	if len(pd.ChildrenRemoved) != 0 {
		t.Errorf("expected no children removed, got: %v", pd.ChildrenRemoved)
	}
}

func TestPartitioningUnchanged(t *testing.T) {
	spec := &model.PartitionSpec{
		Strategy: "range",
		Column:   "created_at",
		Children: []model.PartitionSpec{
			{Strategy: "events_2024", Column: "2024-01-01"},
		},
	}
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public", Partitioning: spec},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public", Partitioning: spec},
		},
	}
	d := Diff(desired, actual)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff for identical partitioning, got: %s", d.Summary())
	}
}

func TestPartitioningFormatTerminal(t *testing.T) {
	d := &SchemaDiff{
		TablesChanged: []TableDiff{
			{
				Name: "events",
				PartitioningChanged: &PartitionDiff{
					StrategyChanged: &[2]string{"range", "hash"},
					KeyChanged:      &[2]string{"created_at", "id"},
					ChildrenAdded:   []string{"events_new"},
					ChildrenRemoved: []string{"events_old"},
				},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, `partitioning: "range"`) {
		t.Errorf("expected strategy change in output, got:\n%s", out)
	}
	if !strings.Contains(out, `partition key: "created_at"`) {
		t.Errorf("expected key change in output, got:\n%s", out)
	}
	if !strings.Contains(out, "+ partition: events_new") {
		t.Errorf("expected child added in output, got:\n%s", out)
	}
	if !strings.Contains(out, "- partition: events_old") {
		t.Errorf("expected child removed in output, got:\n%s", out)
	}
}

func TestArrayChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "posts", Schema: "public", Columns: []model.Column{
				{Name: "tags", PGType: "text", Array: true},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "posts", Schema: "public", Columns: []model.Column{
				{Name: "tags", PGType: "text", Array: false},
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	cc := d.TablesChanged[0].ColumnsChanged[0]
	if cc.ArrayChanged == nil {
		t.Fatal("expected ArrayChanged to be set")
	}
	if cc.ArrayChanged[0] != false || cc.ArrayChanged[1] != true {
		t.Errorf("expected [false, true], got [%v, %v]", cc.ArrayChanged[0], cc.ArrayChanged[1])
	}
}

func TestArrayUnchanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "posts", Schema: "public", Columns: []model.Column{
				{Name: "tags", PGType: "text", Array: true},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "posts", Schema: "public", Columns: []model.Column{
				{Name: "tags", PGType: "text", Array: true},
			}},
		},
	}
	d := Diff(desired, actual)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff when array is unchanged, got: %s", d.Summary())
	}
}

func TestArrayChangedFormatTerminal(t *testing.T) {
	d := &SchemaDiff{
		TablesChanged: []TableDiff{
			{
				Name: "posts",
				ColumnsChanged: []ColumnChange{
					{
						Name:         "tags",
						ArrayChanged: &[2]bool{false, true},
						Risk:         risk.Classification{RiskLevel: risk.Safe},
					},
				},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, "array: false -> true") {
		t.Errorf("expected array change in output, got:\n%s", out)
	}
}

func TestAppendOnlyChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{
				Name:       "events",
				Schema:     "app",
				Columns:    []model.Column{{Name: "id", PGType: "uuid", NotNull: true}},
				PK:         []string{"id"},
				AppendOnly: true,
			},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{
				Name:    "events",
				Schema:  "app",
				Columns: []model.Column{{Name: "id", PGType: "uuid", NotNull: true}},
				PK:      []string{"id"},
			},
		},
	}

	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 changed table, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if td.AppendOnlyChanged == nil {
		t.Fatal("expected AppendOnlyChanged to be set")
	}
	if td.AppendOnlyChanged[0] != false || td.AppendOnlyChanged[1] != true {
		t.Errorf("AppendOnlyChanged = %v, want [false, true]", td.AppendOnlyChanged)
	}
}

func TestBoolSliceEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []bool
		want bool
	}{
		{
			name: "nil vs nil",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "nil vs all-false",
			a:    nil,
			b:    []bool{false, false},
			want: true,
		},
		{
			name: "identical with true",
			a:    []bool{true, false},
			b:    []bool{true, false},
			want: true,
		},
		{
			name: "different values",
			a:    []bool{true},
			b:    []bool{false},
			want: false,
		},
		{
			name: "true vs nil",
			a:    []bool{true},
			b:    nil,
			want: false,
		},
		{
			name: "different lengths trailing false",
			a:    []bool{true},
			b:    []bool{true, false, false},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := boolSliceEqual(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("boolSliceEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
