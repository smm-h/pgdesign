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
				{Name: "status", PGType: "text", Default: model.StrPtr("active")},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "users", Schema: "public", Columns: []model.Column{
				{Name: "status", PGType: "text", Default: model.StrPtr("inactive")},
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
				Columns:  []string{"created_at"},
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
				Columns:  []string{"created_at"},
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
				Columns:  []string{"id"},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public", Partitioning: &model.PartitionSpec{
				Strategy: "range",
				Columns:  []string{"id"},
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
				Columns:  []string{"created_at"},
				Children: []model.PartitionSpec{
					{Name: "events_2024", Bound: "2024-01-01"},
					{Name: "events_2025", Bound: "2025-01-01"},
				},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "events", Schema: "public", Partitioning: &model.PartitionSpec{
				Strategy: "range",
				Columns:  []string{"created_at"},
				Children: []model.PartitionSpec{
					{Name: "events_2024", Bound: "2024-01-01"},
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
		Columns:  []string{"created_at"},
		Children: []model.PartitionSpec{
			{Name: "events_2024", Bound: "2024-01-01"},
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

func TestStoredChangedFormatTerminal(t *testing.T) {
	d := &SchemaDiff{
		TablesChanged: []TableDiff{
			{
				Name: "orders",
				ColumnsChanged: []ColumnChange{
					{
						Name:          "computed",
						StoredChanged: &[2]bool{true, false},
						Risk:          risk.Classification{RiskLevel: risk.Dangerous},
					},
				},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, "stored: true -> false") {
		t.Errorf("expected 'stored: true -> false' in output, got:\n%s", out)
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

func TestDiffColumnJSONSchema(t *testing.T) {
	desired := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "data", PGType: "jsonb", NotNull: true, JSONSchema: "new_schema.json"},
				},
				PK: []string{"id"},
			},
		},
	}

	actual := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "data", PGType: "jsonb", NotNull: true, JSONSchema: "old_schema.json"},
				},
				PK: []string{"id"},
			},
		},
	}

	d := Diff(desired, actual)

	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}

	tc := d.TablesChanged[0]
	if len(tc.ColumnsChanged) != 1 {
		t.Fatalf("expected 1 column changed, got %d", len(tc.ColumnsChanged))
	}

	cc := tc.ColumnsChanged[0]
	if cc.JSONSchemaChanged == nil {
		t.Fatal("expected JSONSchemaChanged to be set")
	}
	if cc.JSONSchemaChanged[0] != "old_schema.json" || cc.JSONSchemaChanged[1] != "new_schema.json" {
		t.Errorf("JSONSchemaChanged = %v, want [old_schema.json, new_schema.json]", cc.JSONSchemaChanged)
	}
}

func TestDiffColumnJSONSchemaAdded(t *testing.T) {
	desired := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "data", PGType: "jsonb", NotNull: true, JSONSchema: "schema.json"},
				},
				PK: []string{"id"},
			},
		},
	}

	actual := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "data", PGType: "jsonb", NotNull: true},
				},
				PK: []string{"id"},
			},
		},
	}

	d := Diff(desired, actual)

	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}

	cc := d.TablesChanged[0].ColumnsChanged[0]
	if cc.JSONSchemaChanged == nil {
		t.Fatal("expected JSONSchemaChanged to be set when adding json_schema")
	}
	if cc.JSONSchemaChanged[0] != "" || cc.JSONSchemaChanged[1] != "schema.json" {
		t.Errorf("JSONSchemaChanged = %v, want ['', schema.json]", cc.JSONSchemaChanged)
	}
}

func TestDiffColumnJSONSchemaUnchanged(t *testing.T) {
	schema := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "items",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid", NotNull: true},
					{Name: "data", PGType: "jsonb", NotNull: true, JSONSchema: "same.json"},
				},
				PK: []string{"id"},
			},
		},
	}

	d := Diff(schema, schema)

	if len(d.TablesChanged) != 0 {
		t.Errorf("expected no table changes when json_schema is the same, got %d", len(d.TablesChanged))
	}
}

func TestIndexWithChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:   "t",
			Schema: "public",
			Indexes: []model.Index{{
				Name:    "idx_t",
				Columns: []string{"id"},
				Method:  "btree",
				With:    map[string]string{"fillfactor": "80"},
			}},
		}},
	}
	actual := &model.Schema{
		Tables: []model.Table{{
			Name:   "t",
			Schema: "public",
			Indexes: []model.Index{{
				Name:    "idx_t",
				Columns: []string{"id"},
				Method:  "btree",
				With:    map[string]string{"fillfactor": "90"},
			}},
		}},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if len(td.IndexesChanged) != 1 {
		t.Fatalf("expected 1 index changed, got %d", len(td.IndexesChanged))
	}
	ic := td.IndexesChanged[0]
	if ic.Old.With["fillfactor"] != "90" {
		t.Errorf("expected old fillfactor=90, got %s", ic.Old.With["fillfactor"])
	}
	if ic.New.With["fillfactor"] != "80" {
		t.Errorf("expected new fillfactor=80, got %s", ic.New.With["fillfactor"])
	}
}

func TestIndexWithEqual(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:   "t",
			Schema: "public",
			Indexes: []model.Index{{
				Name:    "idx_t",
				Columns: []string{"id"},
				Method:  "hnsw",
				With:    map[string]string{"m": "16", "ef_construction": "200"},
			}},
		}},
	}
	actual := &model.Schema{
		Tables: []model.Table{{
			Name:   "t",
			Schema: "public",
			Indexes: []model.Index{{
				Name:    "idx_t",
				Columns: []string{"id"},
				Method:  "hnsw",
				With:    map[string]string{"m": "16", "ef_construction": "200"},
			}},
		}},
	}
	d := Diff(desired, actual)
	if len(d.TablesChanged) != 0 {
		t.Fatalf("expected no changes, got %d table(s) changed", len(d.TablesChanged))
	}
}

func TestDiff_ViewAdded(t *testing.T) {
	desired := &model.Schema{
		Views: []model.View{
			{Name: "active_users", Schema: "public", Query: "SELECT * FROM users WHERE active = true"},
		},
	}
	actual := &model.Schema{}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.ViewsAdded) != 1 || d.ViewsAdded[0] != "active_users" {
		t.Errorf("expected active_users added, got: %v", d.ViewsAdded)
	}
}

func TestDiff_ViewRemoved(t *testing.T) {
	desired := &model.Schema{}
	actual := &model.Schema{
		Views: []model.View{
			{Name: "active_users", Schema: "public", Query: "SELECT * FROM users WHERE active = true"},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.ViewsRemoved) != 1 || d.ViewsRemoved[0] != "active_users" {
		t.Errorf("expected active_users removed, got: %v", d.ViewsRemoved)
	}
}

func TestDiff_ViewChanged(t *testing.T) {
	desired := &model.Schema{
		Views: []model.View{
			{Name: "active_users", Schema: "public", Query: "SELECT id, name FROM users WHERE active = true"},
		},
	}
	actual := &model.Schema{
		Views: []model.View{
			{Name: "active_users", Schema: "public", Query: "SELECT * FROM users WHERE active = true"},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.ViewsChanged) != 1 {
		t.Fatalf("expected 1 view changed, got %d", len(d.ViewsChanged))
	}
	vd := d.ViewsChanged[0]
	if vd.Name != "active_users" {
		t.Errorf("expected view name active_users, got %q", vd.Name)
	}
	if vd.QueryChanged == nil {
		t.Fatal("expected QueryChanged to be set")
	}
	if vd.QueryChanged[0] != "SELECT * FROM users WHERE active = true" {
		t.Errorf("expected old query, got %q", vd.QueryChanged[0])
	}
	if vd.QueryChanged[1] != "SELECT id, name FROM users WHERE active = true" {
		t.Errorf("expected new query, got %q", vd.QueryChanged[1])
	}
}

func TestDiff_ViewCommentChanged(t *testing.T) {
	desired := &model.Schema{
		Views: []model.View{
			{Name: "active_users", Schema: "public", Query: "SELECT * FROM users WHERE active = true", Comment: "New comment"},
		},
	}
	actual := &model.Schema{
		Views: []model.View{
			{Name: "active_users", Schema: "public", Query: "SELECT * FROM users WHERE active = true", Comment: "Old comment"},
		},
	}
	d := Diff(desired, actual)
	if len(d.ViewsChanged) != 1 {
		t.Fatalf("expected 1 view changed, got %d", len(d.ViewsChanged))
	}
	vd := d.ViewsChanged[0]
	if vd.CommentChanged == nil {
		t.Fatal("expected CommentChanged to be set")
	}
	if vd.CommentChanged[0] != "Old comment" || vd.CommentChanged[1] != "New comment" {
		t.Errorf("expected [Old comment, New comment], got [%q, %q]", vd.CommentChanged[0], vd.CommentChanged[1])
	}
	// Query unchanged, so QueryChanged should be nil.
	if vd.QueryChanged != nil {
		t.Error("expected QueryChanged to be nil when query is unchanged")
	}
}

func TestDiff_ViewUnchanged(t *testing.T) {
	schema := &model.Schema{
		Views: []model.View{
			{Name: "active_users", Schema: "public", Query: "SELECT * FROM users WHERE active = true", Comment: "Active users view"},
		},
	}
	d := Diff(schema, schema)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff for identical views, got: %s", d.Summary())
	}
}

func TestDiff_MaterializedViewAdded(t *testing.T) {
	desired := &model.Schema{
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "public",
				Query:    "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
				WithData: true,
			},
		},
	}
	actual := &model.Schema{}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.MaterializedViewsAdded) != 1 || d.MaterializedViewsAdded[0] != "monthly_stats" {
		t.Errorf("expected monthly_stats added, got: %v", d.MaterializedViewsAdded)
	}
}

func TestDiff_MaterializedViewRemoved(t *testing.T) {
	desired := &model.Schema{}
	actual := &model.Schema{
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "public",
				Query:    "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
				WithData: true,
			},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.MaterializedViewsRemoved) != 1 || d.MaterializedViewsRemoved[0] != "monthly_stats" {
		t.Errorf("expected monthly_stats removed, got: %v", d.MaterializedViewsRemoved)
	}
}

func TestDiff_MaterializedViewQueryChanged(t *testing.T) {
	desired := &model.Schema{
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "public",
				Query:    "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
				WithData: true,
			},
		},
	}
	actual := &model.Schema{
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "public",
				Query:    "SELECT date_trunc('week', created_at) AS week, count(*) FROM orders GROUP BY 1",
				WithData: true,
			},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.MaterializedViewsChanged) != 1 {
		t.Fatalf("expected 1 materialized view changed, got %d", len(d.MaterializedViewsChanged))
	}
	mvd := d.MaterializedViewsChanged[0]
	if mvd.Name != "monthly_stats" {
		t.Errorf("expected name monthly_stats, got %q", mvd.Name)
	}
	if mvd.QueryChanged == nil {
		t.Fatal("expected QueryChanged to be non-nil")
	}
	if mvd.QueryChanged[0] != "SELECT date_trunc('week', created_at) AS week, count(*) FROM orders GROUP BY 1" {
		t.Errorf("expected old query from actual, got %q", mvd.QueryChanged[0])
	}
	if mvd.QueryChanged[1] != "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1" {
		t.Errorf("expected new query from desired, got %q", mvd.QueryChanged[1])
	}
}

func TestDiff_MaterializedViewWithDataChanged(t *testing.T) {
	query := "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1"
	desired := &model.Schema{
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "public",
				Query:    query,
				WithData: false,
			},
		},
	}
	actual := &model.Schema{
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "public",
				Query:    query,
				WithData: true,
			},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.MaterializedViewsChanged) != 1 {
		t.Fatalf("expected 1 materialized view changed, got %d", len(d.MaterializedViewsChanged))
	}
	mvd := d.MaterializedViewsChanged[0]
	if mvd.WithDataChanged == nil {
		t.Fatal("expected WithDataChanged to be non-nil")
	}
	if mvd.WithDataChanged[0] != true {
		t.Errorf("expected old WithData=true (actual), got %v", mvd.WithDataChanged[0])
	}
	if mvd.WithDataChanged[1] != false {
		t.Errorf("expected new WithData=false (desired), got %v", mvd.WithDataChanged[1])
	}
	if mvd.QueryChanged != nil {
		t.Error("expected QueryChanged to be nil when query is unchanged")
	}
}

func TestDiff_MaterializedViewIndexAdded(t *testing.T) {
	query := "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1"
	desired := &model.Schema{
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "public",
				Query:    query,
				WithData: true,
				Indexes: []model.Index{
					{Name: "idx_monthly_stats_month", Columns: []string{"month"}, Unique: false},
				},
			},
		},
	}
	actual := &model.Schema{
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "public",
				Query:    query,
				WithData: true,
			},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.MaterializedViewsChanged) != 1 {
		t.Fatalf("expected 1 materialized view changed, got %d", len(d.MaterializedViewsChanged))
	}
	mvd := d.MaterializedViewsChanged[0]
	if len(mvd.IndexesAdded) != 1 {
		t.Fatalf("expected 1 index added, got %d", len(mvd.IndexesAdded))
	}
	if mvd.IndexesAdded[0].Name != "idx_monthly_stats_month" {
		t.Errorf("expected index name idx_monthly_stats_month, got %q", mvd.IndexesAdded[0].Name)
	}
	if mvd.QueryChanged != nil {
		t.Error("expected QueryChanged to be nil when query is unchanged")
	}
}

func TestDiff_MaterializedViewNoChange(t *testing.T) {
	schema := &model.Schema{
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "public",
				Query:    "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
				WithData: true,
			},
		},
	}
	d := Diff(schema, schema)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff for identical materialized views, got: %s", d.Summary())
	}
}

func TestDiff_StoredToVirtualTransition(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "orders",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "computed", PGType: "integer", NotNull: true, Generated: "val * 2", Stored: false},
				},
			},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "orders",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "computed", PGType: "integer", NotNull: true, Generated: "val * 2", Stored: true},
				},
			},
		},
	}

	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff for STORED->VIRTUAL transition")
	}

	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}

	tc := d.TablesChanged[0]
	if len(tc.ColumnsChanged) != 1 {
		t.Fatalf("expected 1 column changed, got %d", len(tc.ColumnsChanged))
	}

	cc := tc.ColumnsChanged[0]
	if cc.Name != "computed" {
		t.Errorf("changed column name = %q, want %q", cc.Name, "computed")
	}
	if cc.StoredChanged == nil {
		t.Fatal("StoredChanged is nil, expected [true, false]")
	}
	if cc.StoredChanged[0] != true || cc.StoredChanged[1] != false {
		t.Errorf("StoredChanged = %v, want [true, false]", cc.StoredChanged)
	}
	// Generated should NOT be flagged as changed (same expression).
	if cc.GeneratedChanged != nil {
		t.Errorf("GeneratedChanged should be nil (same expression), got %v", cc.GeneratedChanged)
	}
}

func TestDiff_StoredToVirtualTransition_NonGenerated(t *testing.T) {
	// If columns are not generated, StoredChanged should not fire
	// even if Stored differs (it's meaningless for non-generated columns).
	desired := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "t",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "val", PGType: "integer", NotNull: true, Stored: false},
				},
			},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "t",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "val", PGType: "integer", NotNull: true, Stored: true},
				},
			},
		},
	}

	d := Diff(desired, actual)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff for non-generated columns with different Stored, got: %+v", d)
	}
}

func intPtr(v int) *int { return &v }

func TestColumnCollationChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", Collation: "de_DE"},
				},
			},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", Collation: ""},
				},
			},
		},
	}

	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff for collation change")
	}
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	tc := d.TablesChanged[0]
	if len(tc.ColumnsChanged) != 1 {
		t.Fatalf("expected 1 column changed, got %d", len(tc.ColumnsChanged))
	}
	cc := tc.ColumnsChanged[0]
	if cc.Name != "name" {
		t.Errorf("changed column = %q, want %q", cc.Name, "name")
	}
	if cc.CollationChanged == nil {
		t.Fatal("CollationChanged is nil, expected [empty, de_DE]")
	}
	if cc.CollationChanged[0] != "" || cc.CollationChanged[1] != "de_DE" {
		t.Errorf("CollationChanged = %v, want [\"\", \"de_DE\"]", cc.CollationChanged)
	}
}

func TestColumnCollationUnchanged(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", Collation: "C"},
				},
			},
		},
	}

	d := Diff(schema, schema)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff when collation is unchanged, got: %s", d.Summary())
	}
}

func TestColumnStatisticsChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", Statistics: intPtr(1000)},
				},
			},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text"},
				},
			},
		},
	}

	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff for statistics change")
	}
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	tc := d.TablesChanged[0]
	if len(tc.ColumnsChanged) != 1 {
		t.Fatalf("expected 1 column changed, got %d", len(tc.ColumnsChanged))
	}
	cc := tc.ColumnsChanged[0]
	if cc.StatisticsChanged == nil {
		t.Fatal("StatisticsChanged is nil")
	}
	if cc.StatisticsChanged[0] != nil {
		t.Errorf("StatisticsChanged[0] = %v, want nil", cc.StatisticsChanged[0])
	}
	if cc.StatisticsChanged[1] == nil || *cc.StatisticsChanged[1] != 1000 {
		t.Errorf("StatisticsChanged[1] = %v, want *1000", cc.StatisticsChanged[1])
	}
}

func TestColumnStatisticsReset(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text"},
				},
			},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", Statistics: intPtr(500)},
				},
			},
		},
	}

	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff for statistics reset")
	}
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	tc := d.TablesChanged[0]
	if len(tc.ColumnsChanged) != 1 {
		t.Fatalf("expected 1 column changed, got %d", len(tc.ColumnsChanged))
	}
	cc := tc.ColumnsChanged[0]
	if cc.StatisticsChanged == nil {
		t.Fatal("StatisticsChanged is nil")
	}
	if cc.StatisticsChanged[0] == nil || *cc.StatisticsChanged[0] != 500 {
		t.Errorf("StatisticsChanged[0] = %v, want *500", cc.StatisticsChanged[0])
	}
	if cc.StatisticsChanged[1] != nil {
		t.Errorf("StatisticsChanged[1] = %v, want nil", cc.StatisticsChanged[1])
	}
}

func TestColumnStatisticsUnchanged(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", Statistics: intPtr(1000)},
				},
			},
		},
	}

	d := Diff(schema, schema)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff when statistics are unchanged, got: %s", d.Summary())
	}
}

func TestIndexCollationChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text"},
				},
				Indexes: []model.Index{
					{Name: "idx_users_name", Columns: []string{"name"}, Collations: map[string]string{"name": "C"}},
				},
			},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text"},
				},
				Indexes: []model.Index{
					{Name: "idx_users_name", Columns: []string{"name"}},
				},
			},
		},
	}

	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff for index collation change")
	}
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table changed, got %d", len(d.TablesChanged))
	}
	tc := d.TablesChanged[0]
	if len(tc.IndexesChanged) != 1 {
		t.Fatalf("expected 1 index changed, got %d", len(tc.IndexesChanged))
	}
	ic := tc.IndexesChanged[0]
	if ic.Name != "idx_users_name" {
		t.Errorf("changed index = %q, want %q", ic.Name, "idx_users_name")
	}
}

func TestIndexCollationUnchanged(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text"},
				},
				Indexes: []model.Index{
					{Name: "idx_users_name", Columns: []string{"name"}, Collations: map[string]string{"name": "C"}},
				},
			},
		},
	}

	d := Diff(schema, schema)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff when index collation is unchanged, got: %s", d.Summary())
	}
}

func TestDiff_ExclusionAdded(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:    "bookings",
			Comment: "Room bookings",
			Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
				{Name: "room_id", PGType: "integer", NotNull: true},
				{Name: "during", PGType: "tsrange", NotNull: true},
			},
			Exclusions: []model.ExclusionConstraint{{
				Name:   "no_overlap",
				Method: "gist",
				Elements: []model.ExclusionElement{
					{Column: "room_id", Operator: "="},
					{Column: "during", Operator: "&&"},
				},
			}},
		}},
	}
	actual := &model.Schema{
		Tables: []model.Table{{
			Name:    "bookings",
			Comment: "Room bookings",
			Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
				{Name: "room_id", PGType: "integer", NotNull: true},
				{Name: "during", PGType: "tsrange", NotNull: true},
			},
		}},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 table change, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if len(td.ExclusionsAdded) != 1 {
		t.Fatalf("expected 1 exclusion added, got %d", len(td.ExclusionsAdded))
	}
	if td.ExclusionsAdded[0].Name != "no_overlap" {
		t.Errorf("expected exclusion name 'no_overlap', got %q", td.ExclusionsAdded[0].Name)
	}
}

func TestDiff_ExclusionRemoved(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:    "bookings",
			Comment: "Room bookings",
			Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
				{Name: "room_id", PGType: "integer", NotNull: true},
				{Name: "during", PGType: "tsrange", NotNull: true},
			},
		}},
	}
	actual := &model.Schema{
		Tables: []model.Table{{
			Name:    "bookings",
			Comment: "Room bookings",
			Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
				{Name: "room_id", PGType: "integer", NotNull: true},
				{Name: "during", PGType: "tsrange", NotNull: true},
			},
			Exclusions: []model.ExclusionConstraint{{
				Name:   "no_overlap",
				Method: "gist",
				Elements: []model.ExclusionElement{
					{Column: "room_id", Operator: "="},
					{Column: "during", Operator: "&&"},
				},
			}},
		}},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	td := d.TablesChanged[0]
	if len(td.ExclusionsRemoved) != 1 {
		t.Fatalf("expected 1 exclusion removed, got %d", len(td.ExclusionsRemoved))
	}
	if td.ExclusionsRemoved[0] != "no_overlap" {
		t.Errorf("expected exclusion name 'no_overlap', got %q", td.ExclusionsRemoved[0])
	}
}

func TestDiff_ExclusionChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:    "bookings",
			Comment: "Room bookings",
			Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
				{Name: "room_id", PGType: "integer", NotNull: true},
				{Name: "during", PGType: "tsrange", NotNull: true},
			},
			Exclusions: []model.ExclusionConstraint{{
				Name:   "no_overlap",
				Method: "gist",
				Elements: []model.ExclusionElement{
					{Column: "during", Operator: "&&"},
				},
			}},
		}},
	}
	actual := &model.Schema{
		Tables: []model.Table{{
			Name:    "bookings",
			Comment: "Room bookings",
			Columns: []model.Column{
				{Name: "id", PGType: "integer", NotNull: true},
				{Name: "room_id", PGType: "integer", NotNull: true},
				{Name: "during", PGType: "tsrange", NotNull: true},
			},
			Exclusions: []model.ExclusionConstraint{{
				Name:   "no_overlap",
				Method: "gist",
				Elements: []model.ExclusionElement{
					{Column: "room_id", Operator: "="},
					{Column: "during", Operator: "&&"},
				},
			}},
		}},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	td := d.TablesChanged[0]
	// Changed = removed old + added new
	if len(td.ExclusionsRemoved) != 1 {
		t.Fatalf("expected 1 exclusion removed (old version), got %d", len(td.ExclusionsRemoved))
	}
	if len(td.ExclusionsAdded) != 1 {
		t.Fatalf("expected 1 exclusion added (new version), got %d", len(td.ExclusionsAdded))
	}
}

func TestDiff_UniqueChangedDeferrable(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:    "users",
				Comment: "users table",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "email", PGType: "text", NotNull: true},
				},
				PK: []string{"id"},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_email", Columns: []string{"email"}, Deferrable: true, InitiallyDeferred: true},
				},
			},
		},
	}
	actual := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:    "users",
				Comment: "users table",
				Columns: []model.Column{
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "email", PGType: "text", NotNull: true},
				},
				PK: []string{"id"},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_email", Columns: []string{"email"}},
				},
			},
		},
	}

	d := Diff(desired, actual)
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 changed table, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if len(td.UniquesRemoved) != 1 {
		t.Fatalf("expected 1 unique removed (old version), got %d", len(td.UniquesRemoved))
	}
	if len(td.UniquesAdded) != 1 {
		t.Fatalf("expected 1 unique added (new version), got %d", len(td.UniquesAdded))
	}
	if !td.UniquesAdded[0].Deferrable {
		t.Error("expected added unique to be deferrable")
	}
	if !td.UniquesAdded[0].InitiallyDeferred {
		t.Error("expected added unique to be initially deferred")
	}
}

func TestDiff_SequenceAdded(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Sequences: []model.Sequence{{
			Name:   "order_seq",
			Schema: "public",
			Start:  model.Int64Ptr(100),
		}},
	}
	actual := &model.Schema{
		Name: "public",
	}

	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff when sequence is added")
	}
	if len(d.SequencesAdded) != 1 {
		t.Fatalf("expected 1 sequence added, got %d", len(d.SequencesAdded))
	}
	if d.SequencesAdded[0] != "order_seq" {
		t.Errorf("SequencesAdded[0] = %q, want %q", d.SequencesAdded[0], "order_seq")
	}
}

func TestDiff_SequenceRemoved(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
	}
	actual := &model.Schema{
		Name: "public",
		Sequences: []model.Sequence{{
			Name:   "order_seq",
			Schema: "public",
			Start:  model.Int64Ptr(1),
		}},
	}

	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff when sequence is removed")
	}
	if len(d.SequencesRemoved) != 1 {
		t.Fatalf("expected 1 sequence removed, got %d", len(d.SequencesRemoved))
	}
	if d.SequencesRemoved[0] != "order_seq" {
		t.Errorf("SequencesRemoved[0] = %q, want %q", d.SequencesRemoved[0], "order_seq")
	}
}

func TestDiff_SequenceChanged(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Sequences: []model.Sequence{{
			Name:   "order_seq",
			Schema: "public",
			Start:  model.Int64Ptr(200),
			Cache:  model.Int64Ptr(20),
		}},
	}
	actual := &model.Schema{
		Name: "public",
		Sequences: []model.Sequence{{
			Name:   "order_seq",
			Schema: "public",
			Start:  model.Int64Ptr(100),
			Cache:  model.Int64Ptr(10),
		}},
	}

	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff when sequence parameters changed")
	}
	if len(d.SequencesChanged) != 1 {
		t.Fatalf("expected 1 sequence changed, got %d", len(d.SequencesChanged))
	}
	sc := d.SequencesChanged[0]
	if sc.Name != "order_seq" {
		t.Errorf("changed sequence name = %q, want %q", sc.Name, "order_seq")
	}
	if sc.StartChanged == nil {
		t.Error("expected StartChanged to be non-nil")
	} else {
		if sc.StartChanged[0] == nil || *sc.StartChanged[0] != 100 {
			t.Errorf("StartChanged[0] (actual) = %v, want 100", sc.StartChanged[0])
		}
		if sc.StartChanged[1] == nil || *sc.StartChanged[1] != 200 {
			t.Errorf("StartChanged[1] (desired) = %v, want 200", sc.StartChanged[1])
		}
	}
	if sc.CacheChanged == nil {
		t.Error("expected CacheChanged to be non-nil")
	} else {
		if sc.CacheChanged[0] == nil || *sc.CacheChanged[0] != 10 {
			t.Errorf("CacheChanged[0] (actual) = %v, want 10", sc.CacheChanged[0])
		}
		if sc.CacheChanged[1] == nil || *sc.CacheChanged[1] != 20 {
			t.Errorf("CacheChanged[1] (desired) = %v, want 20", sc.CacheChanged[1])
		}
	}
}

func TestDiff_SequenceUnchanged(t *testing.T) {
	schema := &model.Schema{
		Name: "public",
		Sequences: []model.Sequence{{
			Name:   "order_seq",
			Schema: "public",
			Start:  model.Int64Ptr(100),
			Cache:  model.Int64Ptr(10),
		}},
	}

	d := Diff(schema, schema)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff when sequences are identical, got: %s", d.Summary())
	}
}

func TestCompositeTypeAdded(t *testing.T) {
	desired := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
				{Name: "city", PGType: "text"},
			}},
		},
	}
	actual := &model.Schema{}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.CompositeTypesAdded) != 1 || d.CompositeTypesAdded[0] != "address" {
		t.Errorf("expected address added, got: %v", d.CompositeTypesAdded)
	}
	if !strings.Contains(d.Summary(), "composite type(s) added") {
		t.Errorf("summary should mention composite types added: %q", d.Summary())
	}
}

func TestCompositeTypeRemoved(t *testing.T) {
	desired := &model.Schema{}
	actual := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
			}},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.CompositeTypesRemoved) != 1 || d.CompositeTypesRemoved[0] != "address" {
		t.Errorf("expected address removed, got: %v", d.CompositeTypesRemoved)
	}
}

func TestCompositeTypeFieldAdded(t *testing.T) {
	desired := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
				{Name: "city", PGType: "text"},
				{Name: "zip", PGType: "text"},
			}},
		},
	}
	actual := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
				{Name: "city", PGType: "text"},
			}},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.CompositeTypesChanged) != 1 {
		t.Fatalf("expected 1 composite type changed, got %d", len(d.CompositeTypesChanged))
	}
	ctd := d.CompositeTypesChanged[0]
	if ctd.Name != "address" {
		t.Errorf("expected name address, got %q", ctd.Name)
	}
	if len(ctd.FieldsAdded) != 1 || ctd.FieldsAdded[0].Name != "zip" {
		t.Errorf("expected zip field added, got: %v", ctd.FieldsAdded)
	}
}

func TestCompositeTypeFieldRemoved(t *testing.T) {
	desired := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
			}},
		},
	}
	actual := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
				{Name: "city", PGType: "text"},
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.CompositeTypesChanged) != 1 {
		t.Fatalf("expected 1 composite type changed, got %d", len(d.CompositeTypesChanged))
	}
	ctd := d.CompositeTypesChanged[0]
	if len(ctd.FieldsRemoved) != 1 || ctd.FieldsRemoved[0] != "city" {
		t.Errorf("expected city removed, got: %v", ctd.FieldsRemoved)
	}
}

func TestCompositeTypeFieldTypeChanged(t *testing.T) {
	desired := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "varchar(200)"},
			}},
		},
	}
	actual := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
			}},
		},
	}
	d := Diff(desired, actual)
	if len(d.CompositeTypesChanged) != 1 {
		t.Fatalf("expected 1 composite type changed, got %d", len(d.CompositeTypesChanged))
	}
	ctd := d.CompositeTypesChanged[0]
	if len(ctd.FieldsChanged) != 1 {
		t.Fatalf("expected 1 field changed, got %d", len(ctd.FieldsChanged))
	}
	fc := ctd.FieldsChanged[0]
	if fc.Name != "street" {
		t.Errorf("expected field name street, got %q", fc.Name)
	}
	if fc.TypeChanged == nil {
		t.Fatal("expected TypeChanged to be set")
	}
	if fc.TypeChanged[0] != "text" || fc.TypeChanged[1] != "varchar(200)" {
		t.Errorf("expected [text, varchar(200)], got [%s, %s]", fc.TypeChanged[0], fc.TypeChanged[1])
	}
}

func TestCompositeTypeUnchanged(t *testing.T) {
	schema := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
				{Name: "city", PGType: "text"},
			}},
		},
	}
	d := Diff(schema, schema)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff for identical composite types, got: %s", d.Summary())
	}
}

func TestCompositeTypeCommentChanged(t *testing.T) {
	desired := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
			}, Comment: "New comment"},
		},
	}
	actual := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
			}, Comment: "Old comment"},
		},
	}
	d := Diff(desired, actual)
	if len(d.CompositeTypesChanged) != 1 {
		t.Fatalf("expected 1 composite type changed, got %d", len(d.CompositeTypesChanged))
	}
	ctd := d.CompositeTypesChanged[0]
	if ctd.CommentChanged == nil {
		t.Fatal("expected CommentChanged to be set")
	}
	if ctd.CommentChanged[0] != "Old comment" || ctd.CommentChanged[1] != "New comment" {
		t.Errorf("expected [Old comment, New comment], got [%q, %q]", ctd.CommentChanged[0], ctd.CommentChanged[1])
	}
}

func TestCompositeTypeSchemaQualified(t *testing.T) {
	desired := &model.Schema{
		CompositeTypes: []model.CompositeType{
			{Name: "address", Schema: "custom", Fields: []model.CompositeField{
				{Name: "street", PGType: "text"},
			}},
		},
	}
	actual := &model.Schema{}
	d := Diff(desired, actual)
	if len(d.CompositeTypesAdded) != 1 || d.CompositeTypesAdded[0] != "custom.address" {
		t.Errorf("expected 'custom.address', got: %v", d.CompositeTypesAdded)
	}
}

func TestFunctionAdded(t *testing.T) {
	desired := &model.Schema{
		Functions: []model.Function{
			{Name: "calculate_tax", Schema: "public", Language: "plpgsql", ReturnType: "numeric", Body: "BEGIN RETURN amount * 0.1; END;"},
		},
	}
	actual := &model.Schema{}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.FunctionsAdded) != 1 || d.FunctionsAdded[0] != "calculate_tax" {
		t.Errorf("expected calculate_tax added, got: %v", d.FunctionsAdded)
	}
}

func TestFunctionRemoved(t *testing.T) {
	desired := &model.Schema{}
	actual := &model.Schema{
		Functions: []model.Function{
			{Name: "calculate_tax", Schema: "public", Language: "plpgsql", ReturnType: "numeric", Body: "BEGIN RETURN amount * 0.1; END;"},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.FunctionsRemoved) != 1 || d.FunctionsRemoved[0] != "calculate_tax" {
		t.Errorf("expected calculate_tax removed, got: %v", d.FunctionsRemoved)
	}
}

func TestFunctionBodyChanged(t *testing.T) {
	desired := &model.Schema{
		Functions: []model.Function{
			{Name: "calculate_tax", Schema: "public", Language: "plpgsql", ReturnType: "numeric",
				Args: []model.FunctionArg{{Name: "amount", Type: "numeric"}},
				Body: "BEGIN RETURN amount * 0.2; END;"},
		},
	}
	actual := &model.Schema{
		Functions: []model.Function{
			{Name: "calculate_tax", Schema: "public", Language: "plpgsql", ReturnType: "numeric",
				Args: []model.FunctionArg{{Name: "amount", Type: "numeric"}},
				Body: "BEGIN RETURN amount * 0.1; END;"},
		},
	}
	d := Diff(desired, actual)
	if len(d.FunctionsChanged) != 1 {
		t.Fatalf("expected 1 function changed, got %d", len(d.FunctionsChanged))
	}
	fd := d.FunctionsChanged[0]
	if fd.BodyChanged == nil {
		t.Fatal("expected BodyChanged to be set")
	}
	if fd.SignatureChanged {
		t.Error("body-only change should not set SignatureChanged")
	}
}

func TestFunctionSignatureChanged(t *testing.T) {
	desired := &model.Schema{
		Functions: []model.Function{
			{Name: "calculate_tax", Schema: "public", Language: "plpgsql", ReturnType: "numeric",
				Args: []model.FunctionArg{{Name: "amount", Type: "numeric"}, {Name: "rate", Type: "numeric"}},
				Body: "BEGIN RETURN amount * rate; END;"},
		},
	}
	actual := &model.Schema{
		Functions: []model.Function{
			{Name: "calculate_tax", Schema: "public", Language: "plpgsql", ReturnType: "numeric",
				Args: []model.FunctionArg{{Name: "amount", Type: "numeric"}},
				Body: "BEGIN RETURN amount * 0.1; END;"},
		},
	}
	d := Diff(desired, actual)
	if len(d.FunctionsChanged) != 1 {
		t.Fatalf("expected 1 function changed, got %d", len(d.FunctionsChanged))
	}
	fd := d.FunctionsChanged[0]
	if !fd.ArgsChanged {
		t.Error("expected ArgsChanged to be true")
	}
	if !fd.SignatureChanged {
		t.Error("expected SignatureChanged to be true when arg count changes")
	}
}

func TestFunctionReturnTypeChanged(t *testing.T) {
	desired := &model.Schema{
		Functions: []model.Function{
			{Name: "get_name", Schema: "public", Language: "sql", ReturnType: "text",
				Body: "SELECT name FROM users LIMIT 1"},
		},
	}
	actual := &model.Schema{
		Functions: []model.Function{
			{Name: "get_name", Schema: "public", Language: "sql", ReturnType: "varchar",
				Body: "SELECT name FROM users LIMIT 1"},
		},
	}
	d := Diff(desired, actual)
	if len(d.FunctionsChanged) != 1 {
		t.Fatalf("expected 1 function changed, got %d", len(d.FunctionsChanged))
	}
	fd := d.FunctionsChanged[0]
	if fd.ReturnTypeChanged == nil {
		t.Fatal("expected ReturnTypeChanged to be set")
	}
	if !fd.SignatureChanged {
		t.Error("expected SignatureChanged when return type changes")
	}
}

func TestFunctionArgDefaultOnly(t *testing.T) {
	// Changing only arg defaults should set ArgsChanged but NOT SignatureChanged.
	desired := &model.Schema{
		Functions: []model.Function{
			{Name: "calc", Schema: "public", Language: "plpgsql", ReturnType: "numeric",
				Args: []model.FunctionArg{{Name: "amount", Type: "numeric", Default: "100"}},
				Body: "BEGIN RETURN amount; END;"},
		},
	}
	actual := &model.Schema{
		Functions: []model.Function{
			{Name: "calc", Schema: "public", Language: "plpgsql", ReturnType: "numeric",
				Args: []model.FunctionArg{{Name: "amount", Type: "numeric", Default: "0"}},
				Body: "BEGIN RETURN amount; END;"},
		},
	}
	d := Diff(desired, actual)
	if len(d.FunctionsChanged) != 1 {
		t.Fatalf("expected 1 function changed, got %d", len(d.FunctionsChanged))
	}
	fd := d.FunctionsChanged[0]
	if !fd.ArgsChanged {
		t.Error("expected ArgsChanged for default change")
	}
	if fd.SignatureChanged {
		t.Error("default-only change should not set SignatureChanged")
	}
}

func TestFunctionIdenticalNoDiff(t *testing.T) {
	fn := model.Function{
		Name: "calc", Schema: "public", Language: "plpgsql", ReturnType: "numeric",
		Args: []model.FunctionArg{{Name: "a", Type: "numeric"}},
		Body: "BEGIN RETURN a; END;",
	}
	schema := &model.Schema{Functions: []model.Function{fn}}
	d := Diff(schema, schema)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff for identical functions, got: %s", d.Summary())
	}
}

func TestFunctionSummary(t *testing.T) {
	d := &SchemaDiff{
		FunctionsAdded:   []string{"fn1"},
		FunctionsRemoved: []string{"fn2"},
		FunctionsChanged: []FunctionDiff{{Name: "fn3"}},
	}
	s := d.Summary()
	if !strings.Contains(s, "1 function(s) added") {
		t.Errorf("summary missing added: %s", s)
	}
	if !strings.Contains(s, "1 function(s) removed") {
		t.Errorf("summary missing removed: %s", s)
	}
	if !strings.Contains(s, "1 function(s) changed") {
		t.Errorf("summary missing changed: %s", s)
	}
}

func TestDomainAdded(t *testing.T) {
	desired := &model.Schema{
		Domains: []model.Domain{
			{Name: "slug", Schema: "public", BaseType: "text", Check: "VALUE ~ '^[a-z0-9-]+$'"},
		},
	}
	actual := &model.Schema{}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.DomainsAdded) != 1 || d.DomainsAdded[0] != "slug" {
		t.Errorf("expected slug added, got: %v", d.DomainsAdded)
	}
}

func TestDomainRemoved(t *testing.T) {
	desired := &model.Schema{}
	actual := &model.Schema{
		Domains: []model.Domain{
			{Name: "slug", Schema: "public", BaseType: "text", Check: "VALUE ~ '^[a-z0-9-]+$'"},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.DomainsRemoved) != 1 || d.DomainsRemoved[0] != "slug" {
		t.Errorf("expected slug removed, got: %v", d.DomainsRemoved)
	}
}

func TestDomainCheckChanged(t *testing.T) {
	desired := &model.Schema{
		Domains: []model.Domain{
			{Name: "slug", Schema: "public", BaseType: "text", Check: "VALUE ~ '^[a-z0-9_-]+$'"},
		},
	}
	actual := &model.Schema{
		Domains: []model.Domain{
			{Name: "slug", Schema: "public", BaseType: "text", Check: "VALUE ~ '^[a-z0-9-]+$'"},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.DomainsChanged) != 1 {
		t.Fatalf("expected 1 domain changed, got %d", len(d.DomainsChanged))
	}
	dd := d.DomainsChanged[0]
	if dd.Name != "slug" {
		t.Errorf("expected domain name slug, got %q", dd.Name)
	}
	if dd.CheckChanged == nil {
		t.Fatal("expected CheckChanged to be set")
	}
	if dd.CheckChanged[0] != "VALUE ~ '^[a-z0-9-]+$'" || dd.CheckChanged[1] != "VALUE ~ '^[a-z0-9_-]+$'" {
		t.Errorf("unexpected check change: %v", dd.CheckChanged)
	}
}

func TestDomainDefaultChanged(t *testing.T) {
	desired := &model.Schema{
		Domains: []model.Domain{
			{Name: "counter", Schema: "public", BaseType: "bigint", Default: "1"},
		},
	}
	actual := &model.Schema{
		Domains: []model.Domain{
			{Name: "counter", Schema: "public", BaseType: "bigint", Default: "0"},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.DomainsChanged) != 1 {
		t.Fatalf("expected 1 domain changed, got %d", len(d.DomainsChanged))
	}
	dd := d.DomainsChanged[0]
	if dd.DefaultChanged == nil {
		t.Fatal("expected DefaultChanged to be set")
	}
	if dd.DefaultChanged[0] != "0" || dd.DefaultChanged[1] != "1" {
		t.Errorf("unexpected default change: %v", dd.DefaultChanged)
	}
}

func TestDomainNotNullChanged(t *testing.T) {
	desired := &model.Schema{
		Domains: []model.Domain{
			{Name: "slug", Schema: "public", BaseType: "text", NotNull: true},
		},
	}
	actual := &model.Schema{
		Domains: []model.Domain{
			{Name: "slug", Schema: "public", BaseType: "text", NotNull: false},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.DomainsChanged) != 1 {
		t.Fatalf("expected 1 domain changed, got %d", len(d.DomainsChanged))
	}
	dd := d.DomainsChanged[0]
	if dd.NotNullChanged == nil {
		t.Fatal("expected NotNullChanged to be set")
	}
	if dd.NotNullChanged[0] != false || dd.NotNullChanged[1] != true {
		t.Errorf("unexpected not_null change: %v", dd.NotNullChanged)
	}
}

func TestDomainUnchanged(t *testing.T) {
	schema := &model.Schema{
		Domains: []model.Domain{
			{Name: "slug", Schema: "public", BaseType: "text", Check: "VALUE ~ '^[a-z0-9-]+$'"},
		},
	}
	d := Diff(schema, schema)
	if len(d.DomainsAdded) != 0 || len(d.DomainsRemoved) != 0 || len(d.DomainsChanged) != 0 {
		t.Error("expected no domain changes for identical schemas")
	}
}

func TestDomainSchemaQualified(t *testing.T) {
	desired := &model.Schema{
		Domains: []model.Domain{
			{Name: "slug", Schema: "custom", BaseType: "text"},
		},
	}
	actual := &model.Schema{}
	d := Diff(desired, actual)
	if len(d.DomainsAdded) != 1 || d.DomainsAdded[0] != "custom.slug" {
		t.Errorf("expected custom.slug added, got: %v", d.DomainsAdded)
	}
}

func TestDomainCommentChanged(t *testing.T) {
	desired := &model.Schema{
		Domains: []model.Domain{
			{Name: "slug", Schema: "public", BaseType: "text", Comment: "URL-safe identifier"},
		},
	}
	actual := &model.Schema{
		Domains: []model.Domain{
			{Name: "slug", Schema: "public", BaseType: "text", Comment: "old comment"},
		},
	}
	d := Diff(desired, actual)
	if len(d.DomainsChanged) != 1 {
		t.Fatalf("expected 1 domain changed, got %d", len(d.DomainsChanged))
	}
	dd := d.DomainsChanged[0]
	if dd.CommentChanged == nil {
		t.Fatal("expected CommentChanged to be set")
	}
	if dd.CommentChanged[0] != "old comment" || dd.CommentChanged[1] != "URL-safe identifier" {
		t.Errorf("unexpected comment change: %v", dd.CommentChanged)
	}
}

func TestDomainBaseTypeChanged(t *testing.T) {
	desired := &model.Schema{
		Domains: []model.Domain{
			{Name: "counter", Schema: "public", BaseType: "bigint"},
		},
	}
	actual := &model.Schema{
		Domains: []model.Domain{
			{Name: "counter", Schema: "public", BaseType: "integer"},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.DomainsChanged) != 1 {
		t.Fatalf("expected 1 domain changed, got %d", len(d.DomainsChanged))
	}
	dd := d.DomainsChanged[0]
	if dd.BaseTypeChanged == nil {
		t.Fatal("expected BaseTypeChanged to be set")
	}
	if dd.BaseTypeChanged[0] != "integer" || dd.BaseTypeChanged[1] != "bigint" {
		t.Errorf("unexpected base type change: %v", dd.BaseTypeChanged)
	}
}

func TestTriggerAdded(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "public", Triggers: []model.Trigger{
				{Name: "audit_insert", Function: "audit_fn", Events: []string{"INSERT"}, Timing: "AFTER", ForEach: "ROW"},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "public"},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 changed table, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if len(td.TriggersAdded) != 1 {
		t.Fatalf("expected 1 trigger added, got %d", len(td.TriggersAdded))
	}
	if td.TriggersAdded[0].Name != "audit_insert" {
		t.Errorf("expected trigger name audit_insert, got %s", td.TriggersAdded[0].Name)
	}
}

func TestTriggerRemoved(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "public"},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "public", Triggers: []model.Trigger{
				{Name: "audit_insert", Function: "audit_fn", Events: []string{"INSERT"}, Timing: "AFTER", ForEach: "ROW"},
			}},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	td := d.TablesChanged[0]
	if len(td.TriggersRemoved) != 1 || td.TriggersRemoved[0] != "audit_insert" {
		t.Errorf("expected audit_insert removed, got %v", td.TriggersRemoved)
	}
}

func TestTriggerChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "public", Triggers: []model.Trigger{
				{Name: "audit_insert", Function: "new_audit_fn", Events: []string{"INSERT", "UPDATE"}, Timing: "AFTER", ForEach: "ROW"},
			}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "public", Triggers: []model.Trigger{
				{Name: "audit_insert", Function: "audit_fn", Events: []string{"INSERT"}, Timing: "AFTER", ForEach: "ROW"},
			}},
		},
	}
	d := Diff(desired, actual)
	if d.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	td := d.TablesChanged[0]
	if len(td.TriggersChanged) != 1 {
		t.Fatalf("expected 1 trigger changed, got %d", len(td.TriggersChanged))
	}
	tc := td.TriggersChanged[0]
	if tc.Name != "audit_insert" {
		t.Errorf("expected trigger name audit_insert, got %s", tc.Name)
	}
	if tc.Old.Function != "audit_fn" {
		t.Errorf("expected old function audit_fn, got %s", tc.Old.Function)
	}
	if tc.New.Function != "new_audit_fn" {
		t.Errorf("expected new function new_audit_fn, got %s", tc.New.Function)
	}
}

func TestTriggerUnchanged(t *testing.T) {
	trig := model.Trigger{
		Name: "audit_insert", Function: "audit_fn", Events: []string{"INSERT"},
		Timing: "AFTER", ForEach: "ROW",
	}
	desired := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "public", Triggers: []model.Trigger{trig}},
		},
	}
	actual := &model.Schema{
		Tables: []model.Table{
			{Name: "orders", Schema: "public", Triggers: []model.Trigger{trig}},
		},
	}
	d := Diff(desired, actual)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff for identical triggers, got: %s", d.Summary())
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestSequenceFormatTerminal(t *testing.T) {
	d := &SchemaDiff{
		SequencesAdded:   []string{"order_seq"},
		SequencesRemoved: []string{"old_seq"},
		SequencesChanged: []SequenceDiff{
			{
				Name:         "counter_seq",
				StartChanged: &[2]*int64{int64Ptr(1), int64Ptr(100)},
				CycleChanged: &[2]bool{false, true},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, "+ sequence order_seq") {
		t.Errorf("expected sequence added in output, got:\n%s", out)
	}
	if !strings.Contains(out, "- sequence old_seq") {
		t.Errorf("expected sequence removed in output, got:\n%s", out)
	}
	if !strings.Contains(out, "~ sequence counter_seq") {
		t.Errorf("expected sequence changed in output, got:\n%s", out)
	}
	if !strings.Contains(out, "start: 1 -> 100") {
		t.Errorf("expected start change in output, got:\n%s", out)
	}
	if !strings.Contains(out, "cycle: false -> true") {
		t.Errorf("expected cycle change in output, got:\n%s", out)
	}
}

func TestFunctionFormatTerminal(t *testing.T) {
	d := &SchemaDiff{
		FunctionsAdded:   []string{"new_fn"},
		FunctionsRemoved: []string{"old_fn"},
		FunctionsChanged: []FunctionDiff{
			{
				Name:              "my_fn",
				BodyChanged:       &[2]string{"old body", "new body"},
				VolatilityChanged: &[2]string{"VOLATILE", "STABLE"},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, "+ function new_fn") {
		t.Errorf("expected function added in output, got:\n%s", out)
	}
	if !strings.Contains(out, "- function old_fn") {
		t.Errorf("expected function removed in output, got:\n%s", out)
	}
	if !strings.Contains(out, "~ function my_fn") {
		t.Errorf("expected function changed in output, got:\n%s", out)
	}
	if !strings.Contains(out, "body changed") {
		t.Errorf("expected body changed in output, got:\n%s", out)
	}
	if !strings.Contains(out, "volatility: VOLATILE -> STABLE") {
		t.Errorf("expected volatility change in output, got:\n%s", out)
	}
}

func TestExclusionFormatTerminal(t *testing.T) {
	d := &SchemaDiff{
		TablesChanged: []TableDiff{
			{
				Name: "bookings",
				ExclusionsAdded: []model.ExclusionConstraint{
					{Name: "no_overlap"},
				},
				ExclusionsRemoved: []string{"old_excl"},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, "+ exclusion no_overlap") {
		t.Errorf("expected exclusion added in output, got:\n%s", out)
	}
	if !strings.Contains(out, "- exclusion old_excl") {
		t.Errorf("expected exclusion removed in output, got:\n%s", out)
	}
}

func TestTriggerFormatTerminal(t *testing.T) {
	d := &SchemaDiff{
		TablesChanged: []TableDiff{
			{
				Name: "orders",
				TriggersAdded: []model.Trigger{
					{
						Name:     "audit_trigger",
						Function: "audit_fn",
						Events:   []string{"INSERT"},
						Timing:   "AFTER",
						ForEach:  "ROW",
					},
				},
				TriggersRemoved: []string{"old_trigger"},
				TriggersChanged: []TriggerChange{
					{Name: "update_trigger"},
				},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, "+ trigger audit_trigger") {
		t.Errorf("expected trigger added in output, got:\n%s", out)
	}
	if !strings.Contains(out, "- trigger old_trigger") {
		t.Errorf("expected trigger removed in output, got:\n%s", out)
	}
	if !strings.Contains(out, "~ trigger update_trigger") {
		t.Errorf("expected trigger changed in output, got:\n%s", out)
	}
}

func TestCollationFormatTerminal(t *testing.T) {
	d := &SchemaDiff{
		TablesChanged: []TableDiff{
			{
				Name: "users",
				ColumnsChanged: []ColumnChange{
					{
						Name:             "name",
						CollationChanged: &[2]string{"en_US", "de_DE"},
						Risk:             risk.Classification{RiskLevel: risk.Safe},
					},
				},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, `collation: "en_US" -> "de_DE"`) {
		t.Errorf("expected collation change in output, got:\n%s", out)
	}
}

func TestStatisticsFormatTerminal(t *testing.T) {
	d := &SchemaDiff{
		TablesChanged: []TableDiff{
			{
				Name: "metrics",
				ColumnsChanged: []ColumnChange{
					{
						Name:              "value",
						StatisticsChanged: &[2]*int{intPtr(100), intPtr(1000)},
						Risk:              risk.Classification{RiskLevel: risk.Safe},
					},
				},
			},
		},
	}
	out := FormatTerminal(d)
	if !strings.Contains(out, "statistics: 100 -> 1000") {
		t.Errorf("expected statistics change in output, got:\n%s", out)
	}
}
