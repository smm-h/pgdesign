package testdb

import (
	"context"
	"testing"
)

// TestCreateInvalidIndex verifies the helper deterministically leaves an
// invalid index (pg_index.indisvalid = false) with no backend kill.
func TestCreateInvalidIndex(t *testing.T) {
	SkipIfNoPostgres(t)
	ctx := context.Background()
	mgr := testManager(t)
	db := mgr.SetupForTest(t, CreateOptions{})
	conn, err := db.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Exec(ctx, "CREATE TABLE t (v integer NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	// Duplicate values so the unique concurrent build fails in its validation phase.
	if _, err := conn.Exec(ctx, "INSERT INTO t (v) VALUES (1), (1), (2)"); err != nil {
		t.Fatal(err)
	}

	if err := CreateInvalidIndex(ctx, conn, "ix_t_v", "t", "v"); err != nil {
		t.Fatalf("CreateInvalidIndex: %v", err)
	}

	// The index exists and is invalid.
	var valid bool
	if err := conn.QueryRow(ctx,
		"SELECT i.indisvalid FROM pg_class c JOIN pg_index i ON i.indexrelid = c.oid WHERE c.relname = 'ix_t_v'").
		Scan(&valid); err != nil {
		t.Fatalf("probe index: %v", err)
	}
	if valid {
		t.Fatal("expected invalid index, got valid")
	}

	// A DROP INDEX CONCURRENTLY IF EXISTS clears it (the resume protocol's rebuild step).
	if _, err := conn.Exec(ctx, "DROP INDEX CONCURRENTLY IF EXISTS ix_t_v"); err != nil {
		t.Fatalf("drop invalid index: %v", err)
	}
}
