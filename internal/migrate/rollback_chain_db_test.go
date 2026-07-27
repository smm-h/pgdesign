package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/catalog"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/testdb"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// threeTableDesired is twoTableDesired plus a standalone products table (the
// second edge's post-state).
func threeTableDesired() *model.Schema {
	s := twoTableDesired()
	s.Tables = append(s.Tables, model.Table{
		Name: "products", Schema: "public", PK: []string{"id"}, Comment: "products",
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
			{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
		},
	})
	s.Canonicalize()
	return s
}

// twoEdgeChainProject builds a chain project with a genesis edge (users+orders)
// and a second edge that adds products. Returns the project and the two edge
// target revisions r1 (after genesis) and r2 (after add-products).
func twoEdgeChainProject(t *testing.T) (*ChainProject, rev.Revision, rev.Revision) {
	t.Helper()
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := extregistry.NewBuiltinRegistry()

	desired1 := twoTableDesired()
	d1 := &diff.SchemaDiff{TablesAdded: []string{"public.users", "public.orders"}}
	m1, _ := GenerateMigration(d1, desired1, "0.1.0", nil, 0, 0, reg)
	if _, err := GenerateEdge(p, m1, desired1, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("GenerateEdge genesis: %v", err)
	}
	r1, err := rev.Compute(desired1, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}

	desired2 := threeTableDesired()
	d2 := diff.Diff(desired2, desired1)
	m2, _ := GenerateMigration(d2, desired2, "0.2.0", nil, 0, 0, reg)
	if _, err := GenerateEdge(p, m2, desired2, desired1, r1, rev.RegistryPresent, "add-products"); err != nil {
		t.Fatalf("GenerateEdge add-products: %v", err)
	}
	r2, err := rev.Compute(desired2, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	return p, r1, r2
}

func relExists(t *testing.T, ctx context.Context, conn *pgx.Conn, rel string) bool {
	t.Helper()
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", rel).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func appliedViewCount(t *testing.T, ctx context.Context, conn *pgx.Conn) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pgdesign_applied_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func currentRevision(t *testing.T, ctx context.Context, conn *pgx.Conn) string {
	t.Helper()
	pos, ok, err := readChainPosition(ctx, conn)
	if err != nil || !ok {
		t.Fatalf("read position: ok=%v err=%v", ok, err)
	}
	return pos.CurrentRevision
}

// TestChainRollbackSingleStep: after applying a two-edge chain, a single-step
// rollback reverses the most-recent edge (drops products, back to r1), leaving
// earlier objects intact; a second single-step rollback returns to genesis. An
// out-of-band table pgdesign never created is NEVER dropped. The applied view
// stays coherent (an edge fully rolled back disappears from applied).
func TestChainRollbackSingleStep(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	// An object pgdesign never created — rollback must never touch it.
	if _, err := conn.Exec(ctx, "CREATE TABLE public.external (id int PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	p, r1, r2 := twoEdgeChainProject(t)
	if _, err := ApplyChain(ctx, conn, p, "5s", nil); err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}
	if got := currentRevision(t, ctx, conn); got != r2.String() {
		t.Fatalf("after apply position %s != r2 %s", got, r2.String())
	}
	if n := appliedViewCount(t, ctx, conn); n != 2 {
		t.Fatalf("applied view = %d, want 2", n)
	}

	// Single-step rollback: reverse add-products.
	rolled, err := RollbackChain(ctx, conn, p, "", "5s")
	if err != nil {
		t.Fatalf("RollbackChain: %v", err)
	}
	if len(rolled) != 1 {
		t.Fatalf("expected 1 rolled edge, got %v", rolled)
	}
	if relExists(t, ctx, conn, "public.products") {
		t.Error("products should be dropped after rolling back add-products")
	}
	if !relExists(t, ctx, conn, "public.users") || !relExists(t, ctx, conn, "public.orders") {
		t.Error("users/orders must survive rolling back only add-products")
	}
	if got := currentRevision(t, ctx, conn); got != r1.String() {
		t.Errorf("position %s != r1 %s", got, r1.String())
	}
	if n := appliedViewCount(t, ctx, conn); n != 1 {
		t.Errorf("applied view = %d, want 1 after one rollback", n)
	}

	// Second single-step rollback: reverse genesis, back to empty.
	if _, err := RollbackChain(ctx, conn, p, "", "5s"); err != nil {
		t.Fatalf("RollbackChain (2): %v", err)
	}
	if relExists(t, ctx, conn, "public.users") || relExists(t, ctx, conn, "public.orders") {
		t.Error("users/orders should be dropped after rolling back genesis")
	}
	if got := currentRevision(t, ctx, conn); got != "" {
		t.Errorf("position %q != genesis", got)
	}
	if n := appliedViewCount(t, ctx, conn); n != 0 {
		t.Errorf("applied view = %d, want 0 at genesis", n)
	}

	// The out-of-band table was never dropped.
	if !relExists(t, ctx, conn, "public.external") {
		t.Error("public.external (never created by pgdesign) must not be dropped by rollback")
	}
}

// TestChainRollbackToRevision: `rollback --to <revision>` reverses every edge down
// to (not including) the target revision in one call.
func TestChainRollbackToRevision(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	p, r1, _ := twoEdgeChainProject(t)
	if _, err := ApplyChain(ctx, conn, p, "5s", nil); err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}

	// Roll back to r1 (keeps genesis applied, drops add-products).
	rolled, err := RollbackChain(ctx, conn, p, r1.String(), "5s")
	if err != nil {
		t.Fatalf("RollbackChain --to r1: %v", err)
	}
	if len(rolled) != 1 {
		t.Fatalf("expected 1 rolled edge to reach r1, got %v", rolled)
	}
	if relExists(t, ctx, conn, "public.products") {
		t.Error("products should be gone after --to r1")
	}
	if !relExists(t, ctx, conn, "public.users") {
		t.Error("users must survive --to r1")
	}
	if got := currentRevision(t, ctx, conn); got != r1.String() {
		t.Errorf("position %s != r1 %s", got, r1.String())
	}

	// --to the same revision again: nothing to do (error, already there).
	if _, err := RollbackChain(ctx, conn, p, r1.String(), "5s"); err == nil {
		t.Error("rolling back to the current revision should error (nothing to do)")
	}
}

// TestChainRollbackFileIndependent proves rollback reads the JOURNAL for its
// inverse content, not the live edge files: after apply, the live edge artifacts
// are moved out of migrations/chain/ into migrations/archive/, so migrations/chain/
// is EMPTY. Rollback still fully works (topology via the archive-inclusive load,
// down-ops via the journal, payloads via the object store).
func TestChainRollbackFileIndependent(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	p, r1, _ := twoEdgeChainProject(t)
	if _, err := ApplyChain(ctx, conn, p, "5s", nil); err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}

	// Move every live edge file out of migrations/chain/ into migrations/archive/.
	chainDir := filepath.Join(p.Root(), chainEdgesDir)
	archiveDir := filepath.Join(p.Root(), chainArchiveDir)
	ents, err := os.ReadDir(chainDir)
	if err != nil {
		t.Fatal(err)
	}
	moved := 0
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if err := os.Rename(filepath.Join(chainDir, e.Name()), filepath.Join(archiveDir, e.Name())); err != nil {
			t.Fatal(err)
		}
		moved++
	}
	if moved == 0 {
		t.Fatal("expected live edge files to move")
	}
	// migrations/chain/ is now empty.
	if live, err := p.LoadLiveEdges(); err != nil || len(live) != 0 {
		t.Fatalf("live edges after archival: %d (err=%v)", len(live), err)
	}

	// Rollback still works from the journal + store + archived topology.
	rolled, err := RollbackChain(ctx, conn, p, r1.String(), "5s")
	if err != nil {
		t.Fatalf("RollbackChain with archived edges: %v", err)
	}
	if len(rolled) != 1 {
		t.Fatalf("expected 1 rolled edge, got %v", rolled)
	}
	if relExists(t, ctx, conn, "public.products") {
		t.Error("products should be dropped even though the live edge file is gone")
	}
}

// TestChainRollbackBoundaryRefuses: rollback refuses to cross the chain_position
// boundary_revision (the upgrade/baseline floor), naming the boundary. Here the
// boundary is set at r1 (an upgrade floor); rolling back below it is frozen.
func TestChainRollbackBoundaryRefuses(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	p, r1, _ := twoEdgeChainProject(t)
	if _, err := ApplyChain(ctx, conn, p, "5s", nil); err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}

	// Simulate an upgrade boundary at r1 (everything at/below r1 is frozen).
	if _, err := conn.Exec(ctx,
		"UPDATE pgdesign_chain_position SET boundary_revision = $1, boundary_kind = 'upgrade' WHERE id = true",
		r1.String()); err != nil {
		t.Fatal(err)
	}

	// Rolling back to r1 is allowed (stays at the boundary).
	if _, err := RollbackChain(ctx, conn, p, r1.String(), "5s"); err != nil {
		t.Fatalf("rollback to the boundary should be allowed: %v", err)
	}
	// Now at r1 == boundary. A further single-step rollback crosses the boundary.
	_, err = RollbackChain(ctx, conn, p, "", "5s")
	if err == nil {
		t.Fatal("rollback across the boundary must refuse")
	}
	if !contains(err.Error(), "boundary") || !contains(err.Error(), r1.String()) {
		t.Errorf("refusal should name the boundary revision, got: %v", err)
	}
	// users/orders survive the refused rollback.
	if !relExists(t, ctx, conn, "public.users") {
		t.Error("a refused boundary rollback must not drop anything")
	}
}

// TestChainRollbackNonInvertibleRefuses: the reversibility pre-check runs against
// JOURNALED ops — an op whose recorded down_op is NULL (non-invertible, e.g. a
// DROP TABLE) is a refusal naming the op, BEFORE anything executes.
func TestChainRollbackNonInvertibleRefuses(t *testing.T) {
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

	// Genesis creates users+orders+products.
	desired1 := threeTableDesired()
	d1 := &diff.SchemaDiff{TablesAdded: []string{"public.users", "public.orders", "public.products"}}
	m1, _ := GenerateMigration(d1, desired1, "0.1.0", nil, 0, 0, reg)
	if _, err := GenerateEdge(p, m1, desired1, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("GenerateEdge genesis: %v", err)
	}
	r1, err := rev.Compute(desired1, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}

	// Second edge DROPS products (a non-invertible drop_table op).
	desired2 := twoTableDesired()
	d2 := diff.Diff(desired2, desired1)
	m2, _ := GenerateMigration(d2, desired2, "0.2.0", nil, 0, 0, reg)
	if _, err := GenerateEdge(p, m2, desired2, desired1, r1, rev.RegistryPresent, "drop-products"); err != nil {
		t.Fatalf("GenerateEdge drop-products: %v", err)
	}

	if _, err := ApplyChain(ctx, conn, p, "5s", nil); err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}
	if relExists(t, ctx, conn, "public.products") {
		t.Fatal("products should have been dropped by the second edge")
	}

	// Rolling back the drop-products edge must refuse: the drop_table op is
	// non-invertible (no recorded down-op).
	_, err = RollbackChain(ctx, conn, p, "", "5s")
	if err == nil {
		t.Fatal("rollback of a non-invertible edge must refuse")
	}
	if !contains(err.Error(), "non-invertible") || !contains(err.Error(), "drop_table") {
		t.Errorf("refusal should name the non-invertible op, got: %v", err)
	}
	// Nothing was executed: users/orders remain, position unchanged.
	if !relExists(t, ctx, conn, "public.users") {
		t.Error("a refused rollback must execute nothing")
	}
}

// TestChainRollbackMidEdgeCIC: aborting an in-progress edge reverses its CONFIRMED
// ops and applies the class-specific protocol to an unconfirmed CREATE INDEX
// CONCURRENTLY intent — dropping the possibly-invalid index IF EXISTS. The edge is
// seeded mid-apply (a confirmed create_table plus a lingering CIC intent) against a
// live invalid index; the rollback drops the index and the table and clears the
// in-progress marker.
func TestChainRollbackMidEdgeCIC(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	// A genesis edge: create_table users + create_index_concurrently users_email_idx.
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := &model.Schema{
		Name: "public", PGVersion: 16,
		Tables: []model.Table{{
			Name: "users", Schema: "public", PK: []string{"id"}, Comment: "users",
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
				{Name: "email", PGType: typeinfo.T("text"), NotNull: true},
			},
			Indexes: []model.Index{{Name: "users_email_idx", Columns: []string{"email"}}},
		}},
	}
	desired.Canonicalize()
	m := &Migration{DDLOps: []DDLOp{
		{
			Op: "create_table", Table: "public.users", Comment: "users", PK: []string{"id"},
			PGVersion: 16, TableDef: &desired.Tables[0],
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_table", Table: "public.users"}}},
		},
		{
			Op: "create_index_concurrently", Table: "public.users", Name: "users_email_idx", Columns: []string{"email"},
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_index_concurrently", Table: "public.users", Name: "users_email_idx", Columns: []string{"email"}}}},
		},
	}}
	if _, err := GenerateEdge(p, m, desired, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("GenerateEdge: %v", err)
	}
	live, err := p.LoadLiveEdges()
	if err != nil || len(live) != 1 {
		t.Fatalf("LoadLiveEdges: %d (err=%v)", len(live), err)
	}
	E := live[0]
	edgeID := E.ID()

	// The real mid-apply DB state: the users table exists (confirmed create_table),
	// and an INVALID users_email_idx exists (the interrupted CIC).
	if _, err := conn.Exec(ctx, "CREATE TABLE public.users (id int8 PRIMARY KEY, email text NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	// Duplicate emails make the seeded UNIQUE concurrent build fail, leaving an
	// INVALID index of the target name (the interrupted-CIC state).
	if _, err := conn.Exec(ctx, "INSERT INTO public.users VALUES (1,'a'),(2,'a'),(3,'b')"); err != nil {
		t.Fatal(err)
	}
	if err := testdb.CreateInvalidIndex(ctx, conn, "users_email_idx", "public.users", "email"); err != nil {
		t.Fatal(err)
	}
	if info, ok, _ := catalog.Index(ctx, conn, "public", "users_email_idx"); !ok || info.Valid {
		t.Fatalf("setup: expected a present invalid index, got ok=%v info=%+v", ok, info)
	}

	// Seed the chain structures + position (in-progress edge = E) + the journal:
	// every op but the CIC is confirmed; the CIC is a lingering intent.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateTrackingStructures(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := insertChainPosition(ctx, tx, chainPosition{
		CurrentRevision: "", BoundaryRevision: "", BoundaryKind: "baseline", CodecEpoch: int(enc.CodecVersion),
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	checksum, err := edgeFileChecksum(E)
	if err != nil {
		t.Fatal(err)
	}
	cicFound := false
	for seq, op := range E.Ops {
		row, err := journalRowFor(op, seq, E.Slug, edgeID, checksum)
		if err != nil {
			t.Fatal(err)
		}
		if op.Kind() == "create_index_concurrently" {
			cicFound = true
			if err := journalIntentOp(ctx, conn, row); err != nil {
				t.Fatal(err)
			}
		} else if err := journalConfirmedOp(ctx, conn, row); err != nil {
			t.Fatal(err)
		}
	}
	if !cicFound {
		t.Fatal("expected a create_index_concurrently op in the edge")
	}
	if err := setInProgressEdge(ctx, conn, &edgeID); err != nil {
		t.Fatal(err)
	}

	// Abort the in-progress edge.
	if _, err := RollbackChain(ctx, conn, p, "", "5s"); err != nil {
		t.Fatalf("RollbackChain (mid-edge abort): %v", err)
	}

	// The possibly-invalid index was dropped IF EXISTS (CIC intent protocol).
	if _, ok, _ := catalog.Index(ctx, conn, "public", "users_email_idx"); ok {
		t.Error("the unconfirmed CIC index must be dropped on abort")
	}
	// The confirmed create_table was reversed.
	if relExists(t, ctx, conn, "public.users") {
		t.Error("the confirmed create_table must be reversed on abort")
	}
	// The journal is empty and the in-progress marker cleared; position at genesis.
	if n := countJournalRows(t, ctx, conn, edgeID); n != 0 {
		t.Errorf("journal rows after abort = %d, want 0", n)
	}
	pos, ok, err := readChainPosition(ctx, conn)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if pos.InProgressEdge != nil {
		t.Errorf("in_progress_edge should be cleared, got %v", *pos.InProgressEdge)
	}
	if pos.CurrentRevision != "" {
		t.Errorf("aborting an in-progress edge keeps the position at the parent (genesis), got %q", pos.CurrentRevision)
	}
}
