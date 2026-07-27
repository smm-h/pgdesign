package migrate

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// chainEphemeralDB returns a fresh ephemeral database, skipping the test cleanly
// when no PostgreSQL server is reachable.
func chainEphemeralDB(t *testing.T) *testdb.EphemeralDB {
	t.Helper()
	dbURL := os.Getenv("PGDESIGN_DB")
	if dbURL == "" {
		dbURL = "postgres://localhost:5432/pgdesign?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	probe, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Skipf("no database available: %v", err)
	}
	probe.Close(ctx)
	mgr, err := testdb.NewManager(dbURL)
	if err != nil {
		t.Skipf("no database manager: %v", err)
	}
	return mgr.SetupForTest(t, testdb.CreateOptions{})
}

// genesisChainProject builds a chain project with a single genesis edge for the
// twoTable model and returns the project.
func genesisChainProject(t *testing.T) *ChainProject {
	t.Helper()
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := twoTableDesired()
	d := &diff.SchemaDiff{TablesAdded: []string{"public.users", "public.orders"}}
	m, _ := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, m, desired, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("GenerateEdge: %v", err)
	}
	return p
}

func countJournalRows(t *testing.T, ctx context.Context, conn *pgx.Conn, edgeID string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pgdesign_migration_ops WHERE edge_id = $1", edgeID).Scan(&n); err != nil {
		t.Fatalf("count journal rows: %v", err)
	}
	return n
}

// TestChainApplyRoundTrip: a fresh database, chain-mode apply, then a re-apply
// that is a no-op. Verifies the tables were created, the chain position advanced
// to the head, and the applied-migrations view reports the edge.
func TestChainApplyRoundTrip(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	p := genesisChainProject(t)
	head, _, err := ChainHead(p)
	if err != nil {
		t.Fatal(err)
	}

	applied, err := ApplyChain(ctx, conn, p, "5s", nil)
	if err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("expected 1 applied edge, got %d (%v)", len(applied), applied)
	}

	// The shop schema tables exist.
	for _, rel := range []string{"public.users", "public.orders"} {
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", rel).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("table %s was not created", rel)
		}
	}

	// The chain position advanced to the head revision.
	pos, ok, err := readChainPosition(ctx, conn)
	if err != nil || !ok {
		t.Fatalf("read position: ok=%v err=%v", ok, err)
	}
	if pos.CurrentRevision != head.String() {
		t.Errorf("position %s != head %s", pos.CurrentRevision, head.String())
	}
	if pos.InProgressEdge != nil {
		t.Errorf("in_progress_edge should be NULL after a completed edge, got %v", *pos.InProgressEdge)
	}

	// The applied-migrations view reports exactly one applied edge.
	var appliedCount int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pgdesign_applied_migrations").Scan(&appliedCount); err != nil {
		t.Fatal(err)
	}
	if appliedCount != 1 {
		t.Errorf("applied view rows = %d, want 1", appliedCount)
	}

	// Re-apply is a no-op (already at the head).
	applied2, err := ApplyChain(ctx, conn, p, "5s", nil)
	if err != nil {
		t.Fatalf("ApplyChain (re-apply): %v", err)
	}
	if len(applied2) != 0 {
		t.Errorf("re-apply should be a no-op, applied %v", applied2)
	}
}

// TestChainApplyCrashMidEdge: an injected failure mid-edge leaves the journal
// consistent — the transactional ops roll back, the position is not advanced, and
// no confirmed op rows remain for the edge.
func TestChainApplyCrashMidEdge(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	p := genesisChainProject(t)
	live, err := p.LoadLiveEdges()
	if err != nil || len(live) != 1 {
		t.Fatalf("LoadLiveEdges: %d (err=%v)", len(live), err)
	}
	edgeID := live[0].ID()

	// Fail after the second op (the first create_table) executes and journals.
	hooks := &ApplyHooks{AfterOp: func(id string, seq int) error {
		if seq == 1 {
			return fmt.Errorf("injected crash after op %d", seq)
		}
		return nil
	}}
	_, err = ApplyChain(ctx, conn, p, "5s", hooks)
	if err == nil {
		t.Fatal("expected ApplyChain to fail from the injected crash")
	}

	// The transactional edge rolled back: no confirmed op rows, position not advanced.
	if n := countJournalRows(t, ctx, conn, edgeID); n != 0 {
		t.Errorf("expected 0 journal rows after rollback, got %d", n)
	}
	pos, ok, err := readChainPosition(ctx, conn)
	if err != nil || !ok {
		t.Fatalf("read position: ok=%v err=%v", ok, err)
	}
	if pos.CurrentRevision != "" {
		t.Errorf("position should remain genesis after crash, got %q", pos.CurrentRevision)
	}

	// The users table must not exist (rolled back with the transaction).
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.users') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("public.users must not exist after a rolled-back edge")
	}

	// A clean re-apply now succeeds and completes the edge.
	applied, err := ApplyChain(ctx, conn, p, "5s", nil)
	if err != nil {
		t.Fatalf("clean re-apply: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("re-apply should complete the edge, applied %v", applied)
	}
}

// TestChainApplyDryRunExecutesNothing: dry-run previews the plan and writes
// nothing — no tables, and no tracking structures are even created.
func TestChainApplyDryRunExecutesNothing(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	p := genesisChainProject(t)
	plans, err := PlanApplyChain(ctx, conn, p)
	if err != nil {
		t.Fatalf("PlanApplyChain: %v", err)
	}
	if len(plans) != 1 || len(plans[0].SQL) == 0 {
		t.Fatalf("expected a non-empty plan, got %+v", plans)
	}

	// Nothing was created: neither the tables nor the tracking structures.
	for _, rel := range []string{"public.users", "public.orders", "pgdesign_chain_position", "pgdesign_migration_ops"} {
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", rel).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("dry-run must not create %s", rel)
		}
	}
}

// TestPreUpgradeGuardAcrossStates: the guard fires exactly when the legacy table
// is present and the chain position is absent, and passes for fresh and
// post-upgrade databases.
func TestPreUpgradeGuardAcrossStates(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	// Fresh database: neither structure present -> guard passes.
	if err := GuardNotPreUpgrade(ctx, conn); err != nil {
		t.Fatalf("fresh DB should pass the guard, got %v", err)
	}

	// Pre-upgrade database: legacy table present, no chain position -> guard fires.
	if err := EnsureMigrationsTable(ctx, conn); err != nil {
		t.Fatal(err)
	}
	err = GuardNotPreUpgrade(ctx, conn)
	if err == nil {
		t.Fatal("pre-upgrade DB should fail the guard")
	}
	if !contains(err.Error(), "migrate upgrade") {
		t.Errorf("guard error should name `migrate upgrade`, got %v", err)
	}

	// Post-upgrade database: chain structures present -> guard passes even with the
	// legacy table still around (upgrade drops it, but presence must not re-fire).
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateTrackingStructures(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := GuardNotPreUpgrade(ctx, conn); err != nil {
		t.Errorf("post-upgrade DB should pass the guard, got %v", err)
	}
}
