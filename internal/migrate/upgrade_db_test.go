package migrate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/testdb"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// legacySeed is one synthesized pre-upgrade migration: a semver file on disk and
// the row that records it in pgdesign_migrations.
type legacySeed struct {
	version   string
	appliedAt time.Time
	desc      string
	checksum  string // recorded checksum (may be intentionally wrong to test amnesty)
}

// setupPreUpgradeDB creates the real schema, the legacy tracking table, and the
// semver files + rows for the given seeds, and returns the introspected model
// (used as BOTH desired and actual so the reconcile is trivially clean).
func setupPreUpgradeDB(t *testing.T, ctx context.Context, ephDB *testdb.EphemeralDB, conn *pgx.Conn, migrationsDir string, seeds []legacySeed) *model.Schema {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE items (id bigint PRIMARY KEY, name text NOT NULL)`,
		`COMMENT ON TABLE items IS 'items'`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed schema %q: %v", stmt, err)
		}
	}
	if err := EnsureMigrationsTable(ctx, conn); err != nil {
		t.Fatal(err)
	}
	for _, s := range seeds {
		content := fmt.Sprintf("description = %q\n", s.desc)
		if err := os.WriteFile(filepath.Join(migrationsDir, s.version+".toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx,
			"INSERT INTO pgdesign_migrations (version, applied_at, checksum, description) VALUES ($1, $2, $3, $4)",
			s.version, s.appliedAt, s.checksum, s.desc); err != nil {
			t.Fatalf("seed row %s: %v", s.version, err)
		}
	}
	desired, diags, err := introspect.Introspect(ctx, ephDB.URL, []string{"public"})
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		t.Fatalf("introspect diagnostics: %v", diags)
	}
	return desired
}

func fileChecksum(t *testing.T, content string) string {
	t.Helper()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
}

func defaultSeeds(t *testing.T) []legacySeed {
	t.Helper()
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	return []legacySeed{
		{"0.1.0", base, "migration 0.1.0", fileChecksum(t, "description = \"migration 0.1.0\"\n")},
		{"0.2.0", base.Add(time.Minute), "migration 0.2.0", fileChecksum(t, "description = \"migration 0.2.0\"\n")},
	}
}

// TestUpgradeEndToEnd: a synthesized pre-upgrade DB upgrades cleanly — the view
// reproduces the snapshot, the legacy table is gone, chain_position is stamped at
// the boundary, the prefix edge is on disk, and consistency is green.
func TestUpgradeEndToEnd(t *testing.T) {
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

	// Snapshot the legacy applied set before the upgrade.
	before, err := snapshotLegacy(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(seeds) {
		t.Fatalf("expected %d legacy rows, got %d", len(seeds), len(before))
	}

	p, err := OpenChainProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	report, err := upgradeForTest(ctx, conn, p, desired, dir)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if report.AlreadyUpgraded {
		t.Fatal("first upgrade should not report already-upgraded")
	}
	if report.PrefixRows != len(seeds) {
		t.Errorf("PrefixRows = %d, want %d", report.PrefixRows, len(seeds))
	}
	if len(report.Amnesty) != 0 {
		t.Errorf("no amnesty expected for matching checksums, got %v", report.Amnesty)
	}

	// The legacy table is gone.
	if legacy, err := LegacyTrackingExists(ctx, conn); err != nil || legacy {
		t.Errorf("legacy table should be dropped (exists=%v err=%v)", legacy, err)
	}
	// chain_position is stamped at the boundary with kind 'upgrade'.
	pos, ok, err := readChainPosition(ctx, conn)
	if err != nil || !ok {
		t.Fatalf("read position: ok=%v err=%v", ok, err)
	}
	if pos.CurrentRevision != report.Boundary || pos.BoundaryRevision != report.Boundary {
		t.Errorf("position not at boundary: current=%s boundary=%s want=%s", pos.CurrentRevision, pos.BoundaryRevision, report.Boundary)
	}
	if pos.BoundaryKind != "upgrade" {
		t.Errorf("boundary_kind = %q, want upgrade", pos.BoundaryKind)
	}
	// The view reproduces the snapshot exactly.
	assertViewMatches(t, ctx, conn, before)
	// The prefix edge is on disk and consistency is green.
	if _, err := os.Stat(filepath.Join(dir, "chain", report.PrefixEdgeFile)); err != nil {
		t.Errorf("prefix edge file missing: %v", err)
	}
	if err := VerifyChainConsistency(p); err != nil {
		t.Errorf("consistency red after upgrade: %v", err)
	}

	// Re-running is a no-op (already upgraded).
	report2, err := upgradeForTest(ctx, conn, p, desired, dir)
	if err != nil {
		t.Fatalf("second upgrade: %v", err)
	}
	if !report2.AlreadyUpgraded {
		t.Error("second upgrade should report already-upgraded")
	}
}

// assertViewMatches checks the applied-migrations view reproduces the given
// legacy snapshot rows exactly.
func assertViewMatches(t *testing.T, ctx context.Context, conn *pgx.Conn, snapshot []legacyRow) {
	t.Helper()
	rows, err := conn.Query(ctx, "SELECT version, applied_at, COALESCE(description,''), checksum FROM pgdesign_applied_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type vrow struct {
		version   string
		appliedAt time.Time
		desc      string
		checksum  string
	}
	var got []vrow
	for rows.Next() {
		var v vrow
		if err := rows.Scan(&v.version, &v.appliedAt, &v.desc, &v.checksum); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if len(got) != len(snapshot) {
		t.Fatalf("view has %d rows, snapshot has %d", len(got), len(snapshot))
	}
	for i, g := range got {
		s := snapshot[i]
		wantDesc := ""
		if s.Description != nil {
			wantDesc = *s.Description
		}
		if g.version != s.Version || g.desc != wantDesc || g.checksum != s.Checksum {
			t.Errorf("view row %d = (%s,%s,%s), snapshot = (%s,%s,%s)", i, g.version, g.desc, g.checksum, s.Version, wantDesc, s.Checksum)
		}
		if !g.appliedAt.Equal(s.AppliedAt) {
			t.Errorf("view row %d applied_at = %v, snapshot = %v", i, g.appliedAt, s.AppliedAt)
		}
	}
}

// TestUpgradeAmnesty: a historical file whose bytes no longer match its recorded
// checksum yields a NAMED amnesty report, and the upgrade PROCEEDS by content.
func TestUpgradeAmnesty(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	dir := t.TempDir()
	seeds := defaultSeeds(t)
	// Corrupt the recorded checksum of 0.2.0 so its on-disk bytes differ.
	seeds[1].checksum = "deadbeef"
	desired := setupPreUpgradeDB(t, ctx, ephDB, conn, dir, seeds)

	p, err := OpenChainProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	report, err := upgradeForTest(ctx, conn, p, desired, dir)
	if err != nil {
		t.Fatalf("upgrade should PROCEED under amnesty, got %v", err)
	}
	if len(report.Amnesty) != 1 {
		t.Fatalf("expected 1 amnesty entry, got %d (%v)", len(report.Amnesty), report.Amnesty)
	}
	a := report.Amnesty[0]
	if a.Recorded != "deadbeef" || a.Actual == "deadbeef" {
		t.Errorf("amnesty entry wrong: %+v", a)
	}
	if a.File == "" {
		t.Error("amnesty entry should name the file")
	}
}

// TestUpgradeDriftRefusal: an out-of-band change so the TOML no longer matches
// the live DB refuses the upgrade, naming the drift.
func TestUpgradeDriftRefusal(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	dir := t.TempDir()
	desired := setupPreUpgradeDB(t, ctx, ephDB, conn, dir, defaultSeeds(t))

	// Introspect desired (before drift) but drift the live DB with an ALTER.
	if _, err := conn.Exec(ctx, "ALTER TABLE items ADD COLUMN extra text NOT NULL DEFAULT ''"); err != nil {
		t.Fatal(err)
	}
	actual, _, err := introspect.Introspect(ctx, ephDB.URL, []string{"public"})
	if err != nil {
		t.Fatal(err)
	}

	p, err := OpenChainProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Upgrade(ctx, conn, p, desired, actual, nil, dir, nil, nil)
	if err == nil {
		t.Fatal("upgrade should refuse when TOML does not match the drifted DB")
	}
	if !contains(err.Error(), "drift") && !contains(err.Error(), "does not match") {
		t.Errorf("refusal should mention drift, got %v", err)
	}
	// Nothing was stamped.
	if exists, _ := ChainStructuresExist(ctx, conn); exists {
		t.Error("no chain structures should be created on a refused upgrade")
	}
	if legacy, _ := LegacyTrackingExists(ctx, conn); !legacy {
		t.Error("legacy table must remain after a refused upgrade")
	}
}

// TestUpgradeCrashBeforeCommit: a BeforeCommit failure rolls the whole
// transaction back (files already landed), and a clean re-run completes.
func TestUpgradeCrashBeforeCommit(t *testing.T) {
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
	before, _ := snapshotLegacy(ctx, conn)

	p, err := OpenChainProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	crash := &UpgradeHooks{BeforeCommit: func() error { return fmt.Errorf("injected crash before commit") }}
	_, err = Upgrade(ctx, conn, p, desired, desired, nil, dir, nil, crash)
	if err == nil {
		t.Fatal("expected the injected before-commit crash to fail the upgrade")
	}

	// The transaction rolled back: no chain structures, legacy table intact.
	if exists, _ := ChainStructuresExist(ctx, conn); exists {
		t.Error("chain structures must not survive a rolled-back upgrade txn")
	}
	if legacy, _ := LegacyTrackingExists(ctx, conn); !legacy {
		t.Error("legacy table must remain after a rolled-back upgrade")
	}
	// But the prefix files landed idempotently before the txn.
	if err := VerifyChainConsistency(p); err != nil {
		t.Errorf("prefix files should be present and consistent after a pre-commit crash: %v", err)
	}

	// A clean re-run completes.
	report, err := upgradeForTest(ctx, conn, p, desired, dir)
	if err != nil {
		t.Fatalf("clean re-run after crash: %v", err)
	}
	if report.AlreadyUpgraded {
		t.Fatal("re-run after a rolled-back txn should perform the upgrade, not no-op")
	}
	assertViewMatches(t, ctx, conn, before)
}

// TestUpgradeConcurrentApplyBlocked: while an upgrade transaction is open and
// holds the advisory lock, a concurrent apply cannot proceed.
func TestUpgradeConcurrentApplyBlocked(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	dir := t.TempDir()
	desired := setupPreUpgradeDB(t, ctx, ephDB, conn, dir, defaultSeeds(t))

	p, err := OpenChainProject(dir)
	if err != nil {
		t.Fatal(err)
	}

	barrier := testdb.NewBarrier()
	done := make(chan error, 1)
	go func() {
		hooks := &UpgradeHooks{BeforeCommit: func() error {
			barrier.Arrive() // signal we are mid-transaction and holding the lock; wait for release
			return nil
		}}
		_, uerr := Upgrade(ctx, conn, p, desired, desired, nil, dir, nil, hooks)
		done <- uerr
	}()

	// Wait until the upgrade is mid-transaction, then attempt a concurrent apply on
	// a SEPARATE connection: it must be blocked (the upgrade holds the lock and the
	// legacy table is still visible, forcing the pre-upgrade guard).
	barrier.WaitArrived()
	conn2, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close(ctx)
	if _, aerr := ApplyChain(ctx, conn2, p, "", "5s", nil); aerr == nil {
		t.Error("concurrent apply during an in-flight upgrade must be blocked")
	}
	barrier.Release()

	if uerr := <-done; uerr != nil {
		t.Fatalf("upgrade should complete after the barrier release: %v", uerr)
	}
	if exists, _ := ChainStructuresExist(ctx, conn); !exists {
		t.Error("upgrade should have completed")
	}
}

// TestUpgradeMultiDatabase: two databases at the same reconciled schema but with
// DIFFERENT legacy row-sets upgrade against the SAME chain files idempotently —
// the shared prefix edge is written once, and each database's journal reproduces
// its own applied set.
func TestUpgradeMultiDatabase(t *testing.T) {
	ctx := context.Background()
	// Shared chain files directory (the union of both databases' prefixes).
	dir := t.TempDir()

	// Database A: three recorded migrations.
	ephA := chainEphemeralDB(t)
	connA, err := ephA.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connA.Close(ctx)
	baseTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	seedsA := []legacySeed{
		{"0.1.0", baseTime, "m1", fileChecksum(t, "description = \"m1\"\n")},
		{"0.2.0", baseTime.Add(time.Minute), "m2", fileChecksum(t, "description = \"m2\"\n")},
		{"0.3.0", baseTime.Add(2 * time.Minute), "m3", fileChecksum(t, "description = \"m3\"\n")},
	}
	// Write the seed files with matching content.
	desiredA := setupPreUpgradeDBWith(t, ctx, ephA, connA, dir, seedsA, map[string]string{
		"0.1.0": "description = \"m1\"\n", "0.2.0": "description = \"m2\"\n", "0.3.0": "description = \"m3\"\n",
	})

	pA, err := OpenChainProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	reportA, err := upgradeForTest(ctx, connA, pA, desiredA, dir)
	if err != nil {
		t.Fatalf("upgrade A: %v", err)
	}

	edgesAfterA, err := pA.LoadLiveEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(edgesAfterA) != 1 {
		t.Fatalf("expected exactly one shared prefix edge, got %d", len(edgesAfterA))
	}

	// Database B: the same schema, but only two recorded migrations (a different
	// applied depth). It upgrades against the SAME dir.
	ephB := chainEphemeralDB(t)
	connB, err := ephB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connB.Close(ctx)
	seedsB := []legacySeed{
		{"0.1.0", baseTime, "m1", fileChecksum(t, "description = \"m1\"\n")},
		{"0.2.0", baseTime.Add(time.Minute), "m2", fileChecksum(t, "description = \"m2\"\n")},
	}
	desiredB := setupPreUpgradeDBWith(t, ctx, ephB, connB, dir, seedsB, nil) // files already present from A

	pB, err := OpenChainProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	reportB, err := upgradeForTest(ctx, connB, pB, desiredB, dir)
	if err != nil {
		t.Fatalf("upgrade B: %v", err)
	}

	// The shared prefix files are unchanged (idempotent): still one edge, same boundary.
	edgesAfterB, err := pB.LoadLiveEdges()
	if err != nil {
		t.Fatal(err)
	}
	if len(edgesAfterB) != 1 {
		t.Fatalf("shared prefix should remain a single edge, got %d", len(edgesAfterB))
	}
	if reportA.Boundary != reportB.Boundary {
		t.Errorf("both DBs reconcile to the same model, so boundaries should match: %s vs %s", reportA.Boundary, reportB.Boundary)
	}
	// Each database's journal reproduces its OWN applied depth.
	if reportA.PrefixRows != 3 || reportB.PrefixRows != 2 {
		t.Errorf("journal depths: A=%d (want 3), B=%d (want 2)", reportA.PrefixRows, reportB.PrefixRows)
	}
	var countA, countB int
	connA.QueryRow(ctx, "SELECT count(*) FROM pgdesign_applied_migrations").Scan(&countA)
	connB.QueryRow(ctx, "SELECT count(*) FROM pgdesign_applied_migrations").Scan(&countB)
	if countA != 3 || countB != 2 {
		t.Errorf("view rows: A=%d (want 3), B=%d (want 2)", countA, countB)
	}
}

// setupPreUpgradeDBWith is setupPreUpgradeDB with caller-controlled file contents
// (so multiple databases can write byte-identical seed files, or reuse existing
// ones when files==nil).
func setupPreUpgradeDBWith(t *testing.T, ctx context.Context, ephDB *testdb.EphemeralDB, conn *pgx.Conn, dir string, seeds []legacySeed, files map[string]string) *model.Schema {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE items (id bigint PRIMARY KEY, name text NOT NULL)`,
		`COMMENT ON TABLE items IS 'items'`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed schema %q: %v", stmt, err)
		}
	}
	if err := EnsureMigrationsTable(ctx, conn); err != nil {
		t.Fatal(err)
	}
	for _, s := range seeds {
		if content, ok := files[s.version]; ok {
			if err := os.WriteFile(filepath.Join(dir, s.version+".toml"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := conn.Exec(ctx,
			"INSERT INTO pgdesign_migrations (version, applied_at, checksum, description) VALUES ($1, $2, $3, $4)",
			s.version, s.appliedAt, s.checksum, s.desc); err != nil {
			t.Fatalf("seed row %s: %v", s.version, err)
		}
	}
	desired, diags, err := introspect.Introspect(ctx, ephDB.URL, []string{"public"})
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		t.Fatalf("introspect diagnostics: %v", diags)
	}
	return desired
}

// TestUpgradePostUpgradeApplyContinues: after an upgrade, a NEW chain-mode edge
// generated from the reconstructed head applies forward from the boundary.
func TestUpgradePostUpgradeApplyContinues(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	dir := t.TempDir()
	desired := setupPreUpgradeDB(t, ctx, ephDB, conn, dir, defaultSeeds(t))
	p, err := OpenChainProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upgradeForTest(ctx, conn, p, desired, dir); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	// Reconstruct the head model (exercising the seam), append a NEW table, and
	// generate a new chain-mode edge parented at the boundary.
	head, prev, err := ChainHead(p)
	if err != nil {
		t.Fatalf("ChainHead: %v", err)
	}
	if prev == nil {
		t.Fatal("post-upgrade head model should reconstruct (non-nil)")
	}
	desired2 := appendExtraTable(prev)
	d := diff.Diff(desired2, prev)
	if d.IsEmpty() {
		t.Fatal("expected the new table to produce a non-empty diff")
	}
	m2, _ := GenerateMigration(d, desired2, "", extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, m2, desired2, prev, head, rev.RegistryPresent, "add-extra"); err != nil {
		t.Fatalf("GenerateEdge (post-upgrade): %v", err)
	}

	// Apply continues from the boundary and creates the new table.
	applied, err := ApplyChain(ctx, conn, p, "", "5s", nil)
	if err != nil {
		t.Fatalf("ApplyChain post-upgrade: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("expected 1 new edge applied, got %d (%v)", len(applied), applied)
	}
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.extra') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("post-upgrade apply should have created public.extra")
	}
}

// appendExtraTable returns a copy of base with an additional table `extra`.
func appendExtraTable(base *model.Schema) *model.Schema {
	cp := *base
	extra := model.Table{
		Name: "extra", Schema: "public", PK: []string{"id"}, Comment: "extra",
		Columns: []model.Column{{Name: "id", PGType: typeinfo.T("bigint"), NotNull: true}},
	}
	cp.Tables = append(append([]model.Table{}, base.Tables...), extra)
	cp.Canonicalize()
	return &cp
}

// upgradeForTest runs Upgrade with desired==actual (a trivially clean reconcile),
// no live normalizer, and no schema-file dirty-tree guard — the convenience path
// the DB tests use when the reconcile and dirty-tree concerns are exercised
// separately.
func upgradeForTest(ctx context.Context, conn *pgx.Conn, p *ChainProject, desired *model.Schema, dir string) (*UpgradeReport, error) {
	return Upgrade(ctx, conn, p, desired, desired, nil, dir, nil, nil)
}
