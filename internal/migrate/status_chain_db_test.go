package migrate

import (
	"context"
	"testing"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/rev"
)

// TestChainStatusOnUpgradedDB: chain-aware status on an upgraded database creates
// NOTHING (never resurrects the dropped legacy pgdesign_migrations table),
// reports the folded legacy migrations as applied, and — after a new edge is
// generated — reports that edge as pending. This is the rehearsal regression:
// `migrate status` called EnsureMigrationsTable and scanned dir/*.toml,
// recreating the legacy table on the upgraded database.
func TestChainStatusOnUpgradedDB(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	dir := t.TempDir()
	seeds := defaultSeeds(t)
	desired := setupPreUpgradeDB(t, ctx, ephDB, conn, dir, seeds)
	p, err := OpenChainProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upgradeForTest(ctx, conn, p, desired, dir); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	// Status right after upgrade: applied = the folded legacy versions, pending
	// empty (the DB is at the boundary head), and NOTHING is created.
	st, err := ComputeChainStatus(ctx, conn, p)
	if err != nil {
		t.Fatalf("ComputeChainStatus: %v", err)
	}
	if legacy, err := LegacyTrackingExists(ctx, conn); err != nil || legacy {
		t.Errorf("status must NOT resurrect the legacy table (exists=%v err=%v)", legacy, err)
	}
	if len(st.Applied) != len(seeds) {
		t.Errorf("Applied = %d edges, want %d (%v)", len(st.Applied), len(seeds), st.Applied)
	}
	if len(st.Pending) != 0 {
		t.Errorf("Pending = %d edges immediately after upgrade, want 0 (%v)", len(st.Pending), st.Pending)
	}

	// Generate a NEW edge (a schema change) parented at the boundary head.
	head, prev, err := ChainHead(p)
	if err != nil {
		t.Fatalf("ChainHead: %v", err)
	}
	desired2 := appendExtraTable(prev)
	d := diff.Diff(desired2, prev)
	if d.IsEmpty() {
		t.Fatal("expected a non-empty diff for the new table")
	}
	m2, _ := GenerateMigration(d, desired2, "", extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, m2, desired2, prev, head, rev.RegistryPresent, "add-extra"); err != nil {
		t.Fatalf("GenerateEdge: %v", err)
	}

	// Status now: the new edge is pending; applied set unchanged; still nothing created.
	st2, err := ComputeChainStatus(ctx, conn, p)
	if err != nil {
		t.Fatalf("ComputeChainStatus (post-generate): %v", err)
	}
	if len(st2.Pending) != 1 {
		t.Errorf("Pending = %d after generate, want 1 (%v)", len(st2.Pending), st2.Pending)
	}
	if len(st2.Applied) != len(seeds) {
		t.Errorf("Applied = %d after generate, want %d", len(st2.Applied), len(seeds))
	}
	if legacy, _ := LegacyTrackingExists(ctx, conn); legacy {
		t.Error("status must still not create the legacy table after a generate")
	}
}
