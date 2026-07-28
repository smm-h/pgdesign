package migrate

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// colExists reports whether column col exists on public.tbl.
func colExists(t *testing.T, ctx context.Context, conn *pgx.Conn, tbl, col string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2)`,
		tbl, col).Scan(&exists)
	if err != nil {
		t.Fatal(err)
	}
	return exists
}

// usersEmailAddr is the genesis post-state: users(id, email_addr).
func usersEmailAddr() *model.Schema {
	s := &model.Schema{
		Name: "public", PGVersion: 16,
		Tables: []model.Table{{
			Name: "users", Schema: "public", PK: []string{"id"}, Comment: "users",
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
				{Name: "email_addr", PGType: typeinfo.T("text"), NotNull: true},
			},
		}},
	}
	s.Canonicalize()
	return s
}

// usersEmail is the second post-state: users(id, email) — a pure rename.
func usersEmail() *model.Schema {
	s := &model.Schema{
		Name: "public", PGVersion: 16,
		Tables: []model.Table{{
			Name: "users", Schema: "public", PK: []string{"id"}, Comment: "users",
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
				{Name: "email", PGType: typeinfo.T("text"), NotNull: true},
			},
		}},
	}
	s.Canonicalize()
	return s
}

// TestChainRenameColumnRoundTrip is the end-to-end rename gate proof: a declared
// column rename generates an ALTER TABLE ... RENAME COLUMN edge; applying it
// renames email_addr -> email preserving the table; a single-step rollback
// reverses the rename, restoring email_addr. No data-loss drop+create anywhere.
func TestChainRenameColumnRoundTrip(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := extregistry.NewBuiltinRegistry()

	// Edge 1 (genesis): create users(id, email_addr).
	desired1 := usersEmailAddr()
	d1 := &diff.SchemaDiff{TablesAdded: []string{"users"}}
	m1, _ := GenerateMigration(d1, desired1, "", reg)
	if _, err := GenerateEdge(p, m1, desired1, nil, rev.Revision{}, rev.RegistryPresent, "create-users"); err != nil {
		t.Fatalf("GenerateEdge 1: %v", err)
	}
	r1, err := rev.Compute(desired1, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}

	// Edge 2: declared rename email_addr -> email.
	desired2 := usersEmail()
	d2 := diff.Diff(desired2, desired1)
	spec := diff.RenameSpec{Columns: []diff.ColumnRenameSpec{{Table: "users", From: "email_addr", To: "email"}}}
	if err := diff.ResolveRenames(d2, desired2, desired1, spec, false); err != nil {
		t.Fatalf("ResolveRenames: %v", err)
	}
	m2, _ := GenerateMigration(d2, desired2, "", reg)
	// The edge must be a pure rename: one rename_column op, no drop/add column.
	foundRename := false
	for _, op := range m2.DDLOps {
		if op.Op == "rename_column" {
			foundRename = true
		}
		if op.Op == "add_column" || op.Op == "drop_column" {
			t.Fatalf("rename edge must not drop/create columns, saw %s", op.Op)
		}
	}
	if !foundRename {
		t.Fatal("expected a rename_column op in the rename edge")
	}
	if _, err := GenerateEdge(p, m2, desired2, desired1, r1, rev.RegistryPresent, "rename-email"); err != nil {
		t.Fatalf("GenerateEdge 2: %v", err)
	}

	// Apply the whole chain.
	if _, err := ApplyChain(ctx, conn, p, "", "5s", nil); err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}
	if !colExists(t, ctx, conn, "users", "email") {
		t.Error("after apply, users.email must exist")
	}
	if colExists(t, ctx, conn, "users", "email_addr") {
		t.Error("after apply, users.email_addr must be gone (renamed, not duplicated)")
	}

	// Single-step rollback reverses the rename: email_addr restored.
	if _, err := RollbackChain(ctx, conn, p, r1.String(), "5s"); err != nil {
		t.Fatalf("RollbackChain: %v", err)
	}
	if !colExists(t, ctx, conn, "users", "email_addr") {
		t.Error("after rollback, users.email_addr must be restored")
	}
	if colExists(t, ctx, conn, "users", "email") {
		t.Error("after rollback, users.email must be gone")
	}
}

