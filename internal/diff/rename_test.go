package diff

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// col builds a simple NOT NULL text column with an optional comment.
func rcol(name, comment string) model.Column {
	return model.Column{Name: name, PGType: typeinfo.T("text"), NotNull: true, Comment: comment}
}

// usersTable builds a users table in public with an id column plus the given
// extra columns.
func usersTable(cols ...model.Column) model.Table {
	all := append([]model.Column{{Name: "id", PGType: typeinfo.T("int8"), NotNull: true}}, cols...)
	return model.Table{Name: "users", Schema: "public", PK: []string{"id"}, Comment: "users", Columns: all}
}

func renameSchema(tables ...model.Table) *model.Schema {
	s := &model.Schema{Name: "public", PGVersion: 16, Tables: tables}
	s.Canonicalize()
	return s
}

// TestRenameGate_DeclaredColumn: a declared column rename resolves to a
// ColumnsRenamed entry and empties the paired add/remove.
func TestRenameGate_DeclaredColumn(t *testing.T) {
	actual := renameSchema(usersTable(rcol("email_addr", "")))
	desired := renameSchema(usersTable(rcol("email", "")))
	d := Diff(desired, actual)

	spec := RenameSpec{Columns: []ColumnRenameSpec{{Table: "users", From: "email_addr", To: "email"}}}
	if err := ResolveRenames(d, desired, actual, spec, false); err != nil {
		t.Fatalf("declared rename should resolve, got: %v", err)
	}
	if len(d.TablesChanged) != 1 {
		t.Fatalf("expected 1 changed table, got %d", len(d.TablesChanged))
	}
	td := d.TablesChanged[0]
	if len(td.ColumnsRenamed) != 1 || td.ColumnsRenamed[0].From != "email_addr" || td.ColumnsRenamed[0].To != "email" {
		t.Fatalf("expected ColumnsRenamed [email_addr->email], got %+v", td.ColumnsRenamed)
	}
	if len(td.ColumnsRemoved) != 0 || len(td.ColumnsAdded) != 0 {
		t.Errorf("expected add/remove emptied, got removed=%v added=%v", td.ColumnsRemoved, td.ColumnsAdded)
	}
}

// TestRenameGate_UndeclaredColumnBlocks: an undeclared plausible column rename
// is a hard error naming the pair and pointing at [renames].
func TestRenameGate_UndeclaredColumnBlocks(t *testing.T) {
	actual := renameSchema(usersTable(rcol("email_addr", "")))
	desired := renameSchema(usersTable(rcol("email", "")))
	d := Diff(desired, actual)

	err := ResolveRenames(d, desired, actual, RenameSpec{}, false)
	if err == nil {
		t.Fatal("undeclared plausible rename must be a hard error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "email_addr") || !strings.Contains(msg, "email") {
		t.Errorf("error should name both columns, got: %s", msg)
	}
	if !strings.Contains(msg, "[renames]") {
		t.Errorf("error should point at [renames], got: %s", msg)
	}
}

// TestRenameGate_AmbiguousColumn: a removed column content-equal to more than
// one added column is a hard error listing all candidates, never auto-paired.
func TestRenameGate_AmbiguousColumn(t *testing.T) {
	actual := renameSchema(usersTable(rcol("a", "")))
	desired := renameSchema(usersTable(rcol("b", ""), rcol("c", "")))
	d := Diff(desired, actual)

	err := ResolveRenames(d, desired, actual, RenameSpec{}, false)
	if err == nil {
		t.Fatal("ambiguous rename must be a hard error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ambiguous") {
		t.Errorf("error should say ambiguous, got: %s", msg)
	}
	if !strings.Contains(msg, "b") || !strings.Contains(msg, "c") {
		t.Errorf("error should list both candidates, got: %s", msg)
	}
}

// TestRenameGate_DeliberateDropEscape: making the definitions differ (a comment)
// defeats plausibility, so the drop+add proceeds with no gate error.
func TestRenameGate_DeliberateDropEscape(t *testing.T) {
	actual := renameSchema(usersTable(rcol("foo", "")))
	desired := renameSchema(usersTable(rcol("bar", "a different purpose")))
	d := Diff(desired, actual)

	if err := ResolveRenames(d, desired, actual, RenameSpec{}, false); err != nil {
		t.Fatalf("a differing-definition drop+add must not trip the gate, got: %v", err)
	}
	td := d.TablesChanged[0]
	if len(td.ColumnsRenamed) != 0 {
		t.Errorf("no rename should be recorded, got %+v", td.ColumnsRenamed)
	}
}

// TestRenameGate_StaleColumnEntry: a declared rename whose old column is not
// being dropped is a validation error.
func TestRenameGate_StaleColumnEntry(t *testing.T) {
	// email_addr exists in both -> not removed; the declared rename is stale.
	actual := renameSchema(usersTable(rcol("email_addr", "")))
	desired := renameSchema(usersTable(rcol("email_addr", "")))
	d := Diff(desired, actual)

	spec := RenameSpec{Columns: []ColumnRenameSpec{{Table: "users", From: "email_addr", To: "email"}}}
	err := ResolveRenames(d, desired, actual, spec, false)
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected a stale-entry error, got: %v", err)
	}
}

// TestRenameGate_DeclaredTable: a declared table rename resolves via masked
// content-id equality into a TablesRenamed entry.
func TestRenameGate_DeclaredTable(t *testing.T) {
	body := []model.Column{{Name: "id", PGType: typeinfo.T("int8"), NotNull: true}, rcol("x", "")}
	oldT := model.Table{Name: "old_t", Schema: "public", PK: []string{"id"}, Comment: "t", Columns: body}
	newT := model.Table{Name: "new_t", Schema: "public", PK: []string{"id"}, Comment: "t", Columns: body}
	actual := renameSchema(oldT)
	desired := renameSchema(newT)
	d := Diff(desired, actual)

	spec := RenameSpec{Tables: []RenamePair{{From: "old_t", To: "new_t"}}}
	if err := ResolveRenames(d, desired, actual, spec, false); err != nil {
		t.Fatalf("declared table rename should resolve, got: %v", err)
	}
	if len(d.TablesRenamed) != 1 || d.TablesRenamed[0].From != "old_t" || d.TablesRenamed[0].To != "new_t" {
		t.Fatalf("expected TablesRenamed [old_t->new_t], got %+v", d.TablesRenamed)
	}
	if len(d.TablesRemoved) != 0 || len(d.TablesAdded) != 0 {
		t.Errorf("expected add/remove emptied, got removed=%v added=%v", d.TablesRemoved, d.TablesAdded)
	}
}

// fkBearingTable builds a table with a child_id column, an FK to `other` named by
// the default convention (fk_<table>_other), and the enrich-derived auto-FK
// coverage index (idx_<table>_child_id, IsAutoFK). Both the FK name and the auto
// index name embed the table name — exactly the artifacts that defeat the masked
// rename gate before neutralization.
func fkBearingTable(name string) model.Table {
	return model.Table{
		Name:    name,
		Schema:  "public",
		Comment: "t",
		PK:      []string{"id"},
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
			{Name: "child_id", PGType: typeinfo.T("int8"), NotNull: true},
		},
		FKs: []model.FK{{
			Name: "fk_" + name + "_other", Columns: []string{"child_id"},
			RefSchema: "public", RefTable: "other", RefColumns: []string{"id"}, OnDelete: "cascade",
		}},
		Indexes: []model.Index{{
			Name: "idx_" + name + "_child_id", Columns: []string{"child_id"},
			Method: "btree", IsAutoFK: true,
		}},
	}
}

// TestRenameGate_UndeclaredFKTableBlocks: an undeclared rename of an FK-BEARING
// table must fire the gate. Before the maskedTableID neutralization the auto-FK
// index name and convention FK name embed the table name, so the masked ids
// DIFFER and the rename silently escapes as a drop+create (the data-loss hazard
// the gate exists to prevent).
func TestRenameGate_UndeclaredFKTableBlocks(t *testing.T) {
	oldT := fkBearingTable("old_t")
	newT := fkBearingTable("new_t")
	d := Diff(renameSchema(newT), renameSchema(oldT))

	err := ResolveRenames(d, renameSchema(newT), renameSchema(oldT), RenameSpec{}, false)
	if err == nil {
		t.Fatal("undeclared rename of an FK-bearing table must be a hard error (gate must fire)")
	}
	if !strings.Contains(err.Error(), "old_t") || !strings.Contains(err.Error(), "new_t") {
		t.Errorf("error should name both tables, got: %s", err.Error())
	}
}

// TestRenameGate_DeclaredFKTableResolves: a DECLARED rename of the same FK-bearing
// table must resolve to a TablesRenamed entry. Before neutralization the masked
// ids differ and the declared rename is wrongly rejected as "differ beyond their
// name".
func TestRenameGate_DeclaredFKTableResolves(t *testing.T) {
	oldT := fkBearingTable("old_t")
	newT := fkBearingTable("new_t")
	actual := renameSchema(oldT)
	desired := renameSchema(newT)
	d := Diff(desired, actual)

	spec := RenameSpec{Tables: []RenamePair{{From: "old_t", To: "new_t"}}}
	if err := ResolveRenames(d, desired, actual, spec, false); err != nil {
		t.Fatalf("declared rename of an FK-bearing table should resolve, got: %v", err)
	}
	if len(d.TablesRenamed) != 1 || d.TablesRenamed[0].From != "old_t" || d.TablesRenamed[0].To != "new_t" {
		t.Fatalf("expected TablesRenamed [old_t->new_t], got %+v", d.TablesRenamed)
	}
	if len(d.TablesRemoved) != 0 || len(d.TablesAdded) != 0 {
		t.Errorf("expected add/remove emptied, got removed=%v added=%v", d.TablesRemoved, d.TablesAdded)
	}
}

// TestRenameGate_CustomNamedIndexNotBlanked: neutralization must NOT blank custom
// (non-scheme) index names. Two tables whose ONLY difference is a custom index
// name must mask DIFFERENTLY, so no false rename pairing occurs (the drop+create
// proceeds without a spurious gate error).
func TestRenameGate_CustomNamedIndexNotBlanked(t *testing.T) {
	oldT := model.Table{
		Name: "old_t", Schema: "public", Comment: "t", PK: []string{"id"},
		Columns: []model.Column{{Name: "id", PGType: typeinfo.T("int8"), NotNull: true}, rcol("x", "")},
		Indexes: []model.Index{{Name: "lookup_a", Columns: []string{"x"}, Method: "btree"}},
	}
	newT := model.Table{
		Name: "new_t", Schema: "public", Comment: "t", PK: []string{"id"},
		Columns: []model.Column{{Name: "id", PGType: typeinfo.T("int8"), NotNull: true}, rcol("x", "")},
		Indexes: []model.Index{{Name: "lookup_b", Columns: []string{"x"}, Method: "btree"}},
	}
	d := Diff(renameSchema(newT), renameSchema(oldT))
	if err := ResolveRenames(d, renameSchema(newT), renameSchema(oldT), RenameSpec{}, false); err != nil {
		t.Fatalf("distinct custom index names must mask differently (no false pairing), got: %v", err)
	}
}

// TestRenameGate_UndeclaredTableBlocks: an undeclared plausible table rename
// (equal masked content-id) is a hard error.
func TestRenameGate_UndeclaredTableBlocks(t *testing.T) {
	body := []model.Column{{Name: "id", PGType: typeinfo.T("int8"), NotNull: true}, rcol("x", "")}
	oldT := model.Table{Name: "old_t", Schema: "public", PK: []string{"id"}, Comment: "t", Columns: body}
	newT := model.Table{Name: "new_t", Schema: "public", PK: []string{"id"}, Comment: "t", Columns: body}
	d := Diff(renameSchema(newT), renameSchema(oldT))

	err := ResolveRenames(d, renameSchema(newT), renameSchema(oldT), RenameSpec{}, false)
	if err == nil {
		t.Fatal("undeclared plausible table rename must be a hard error")
	}
	if !strings.Contains(err.Error(), "old_t") || !strings.Contains(err.Error(), "new_t") {
		t.Errorf("error should name both tables, got: %s", err.Error())
	}
}
