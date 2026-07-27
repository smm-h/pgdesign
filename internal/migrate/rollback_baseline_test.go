package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestLegacyRollbackRefusesBaselineRow is the red-green regression for the live
// baseline data-loss bug (roadmap 5.6, Part III L5 grounding). A baseline row
// records checksum literal "baseline" and applies NOTHING — the database's schema
// pre-existed and pgdesign never created it. Yet the legacy Rollback loaded
// version+".toml" and ran ITS file down-ops regardless, executing DROPs against
// objects pgdesign never created (rollback.go:41,207 era). Here the migration
// file's down would DROP a table that exists out-of-band; a correct rollback must
// REFUSE the baseline row and leave the table intact.
func TestLegacyRollbackRefusesBaselineRow(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}
	defer conn.Close(ctx)

	// A table that pgdesign never created (adopted / pre-existing schema).
	if _, err := conn.Exec(ctx, "CREATE TABLE public.pgdesign_keepme (id int PRIMARY KEY)"); err != nil {
		t.Fatalf("create out-of-band table: %v", err)
	}

	// A migration file whose down-op DROPs that very table. Baseline records the
	// version but never runs the up-op; the down must never fire either.
	dir := t.TempDir()
	migration := `description = "Adopted table"

[[ddl]]
op = "create_table"
table = "public.pgdesign_keepme"
down = { op = "drop_table", table = "public.pgdesign_keepme" }
`
	if err := os.WriteFile(filepath.Join(dir, "0.1.0.toml"), []byte(migration), 0o644); err != nil {
		t.Fatal(err)
	}

	// Baseline: records version 0.1.0 with checksum "baseline"; applies nothing.
	if err := Baseline(ctx, conn, dir, "0.1.0", "adopt existing schema"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Rollback MUST refuse a baseline row (it applied nothing to reverse).
	_, err = Rollback(ctx, conn, dir, "")
	if err == nil {
		t.Error("Rollback of a baseline row must refuse, but it succeeded (data-loss path)")
	} else if !contains(err.Error(), "baseline") {
		t.Errorf("refusal should name the baseline row, got: %v", err)
	}

	// The out-of-band table MUST still exist — rollback must not DROP objects
	// pgdesign never created.
	var exists bool
	if err := conn.QueryRow(ctx,
		"SELECT to_regclass('public.pgdesign_keepme') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("public.pgdesign_keepme was dropped by rollback of a baseline row (LIVE DATA-LOSS BUG)")
	}
}

// TestLegacyRollbackToRefusesBaselineRow: the multi-step RollbackTo path must also
// refuse when a baseline row is in the rollback range, before executing anything.
func TestLegacyRollbackToRefusesBaselineRow(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "CREATE TABLE public.pgdesign_keepme (id int PRIMARY KEY)"); err != nil {
		t.Fatalf("create out-of-band table: %v", err)
	}

	dir := t.TempDir()
	// 0.1.0 is a plain baseline floor (its own down is a no-op marker).
	floor := `description = "Baseline floor"

[[ddl]]
op = "create_table"
table = "public.pgdesign_floor"
`
	if err := os.WriteFile(filepath.Join(dir, "0.1.0.toml"), []byte(floor), 0o644); err != nil {
		t.Fatal(err)
	}
	// 0.2.0 is ALSO baselined (adopted), and its file down would DROP the
	// out-of-band table. RollbackTo 0.1.0 rolls back 0.2.0 — a baseline row.
	adopted := `description = "Adopted table"

[[ddl]]
op = "create_table"
table = "public.pgdesign_keepme"
down = { op = "drop_table", table = "public.pgdesign_keepme" }
`
	if err := os.WriteFile(filepath.Join(dir, "0.2.0.toml"), []byte(adopted), 0o644); err != nil {
		t.Fatal(err)
	}

	// Baseline both up to 0.2.0: records 0.1.0 and 0.2.0 with checksum "baseline".
	if err := Baseline(ctx, conn, dir, "0.2.0", "adopt"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// RollbackTo 0.1.0 would roll back 0.2.0 (a baseline row) — must refuse before
	// executing anything.
	_, err = RollbackTo(ctx, conn, dir, "0.1.0", "")
	if err == nil {
		t.Error("RollbackTo across a baseline row must refuse")
	} else if !contains(err.Error(), "baseline") {
		t.Errorf("refusal should name the baseline row, got: %v", err)
	}

	// The out-of-band table survives.
	var exists bool
	if err := conn.QueryRow(ctx,
		"SELECT to_regclass('public.pgdesign_keepme') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("public.pgdesign_keepme dropped by RollbackTo across a baseline row (DATA LOSS)")
	}
}
