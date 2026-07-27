package migrate

import (
	"context"
	"testing"

	"github.com/smm-h/pgdesign/internal/catalog"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/pgcap"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// TestChainApplyPreconditionDrift: when the world is NOT at the edge's from-state
// (an object a create op expects absent already exists), apply hard-errors naming
// object/expected/found — drift is loud, never absorbed (L5).
func TestChainApplyPreconditionDrift(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	// Pre-create public.users so the genesis edge's create_table users op drifts.
	if _, err := conn.Exec(ctx, "CREATE TABLE public.users (id integer PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	p := genesisChainProject(t)
	_, err = ApplyChain(ctx, conn, p, "", "5s", nil)
	if err == nil {
		t.Fatal("expected a precondition drift error")
	}
	for _, want := range []string{"table public.users", "expected absent", "found present"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// buildCICOp builds a self-contained create_index_concurrently op on public.t(email).
func buildCICOp(t *testing.T) (*objstore.Store, SelfContainedOp) {
	t.Helper()
	store, err := objstore.New(t.TempDir(), uint32(enc.CodecVersion))
	if err != nil {
		t.Fatal(err)
	}
	op, err := DDLOpToSelfContained(store, DDLOp{
		Op:      "create_index_concurrently",
		Table:   "public.t",
		Name:    "ix_t_email",
		Columns: []string{"email"},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return store, op
}

// TestResumeCICRebuildsInvalidIndex: a lingering intent whose index is INVALID
// (interrupted CIC) is DROP-rebuilt to VALID on resume (roadmap L8).
func TestResumeCICRebuildsInvalidIndex(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "CREATE TABLE public.t (email text NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	// Duplicate data so a UNIQUE concurrent build leaves an invalid index of the target name.
	if _, err := conn.Exec(ctx, "INSERT INTO public.t VALUES ('a'),('a'),('b')"); err != nil {
		t.Fatal(err)
	}
	if err := testdb.CreateInvalidIndex(ctx, conn, "ix_t_email", "public.t", "email"); err != nil {
		t.Fatal(err)
	}
	// Confirm the seeded state is invalid.
	if info, ok, _ := catalog.Index(ctx, conn, "public", "ix_t_email"); !ok || info.Valid {
		t.Fatalf("setup: expected present invalid index, got ok=%v info=%+v", ok, info)
	}

	store, op := buildCICOp(t)
	// Resume: the protocol drops the invalid index and rebuilds a valid (non-unique) one.
	if err := executeNonTransactionalOp(ctx, conn, store, op, true); err != nil {
		t.Fatalf("resume CIC: %v", err)
	}
	info, ok, err := catalog.Index(ctx, conn, "public", "ix_t_email")
	if err != nil || !ok {
		t.Fatalf("post-resume index: ok=%v err=%v", ok, err)
	}
	if !info.Valid {
		t.Error("resume must rebuild the index to VALID")
	}
}

// TestResumeCICValidLeftAsIs: a lingering intent whose index is already VALID
// (crash after build before confirm) is left untouched — no needless rebuild.
func TestResumeCICValidLeftAsIs(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "CREATE TABLE public.t (email text NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO public.t VALUES ('a'),('b')"); err != nil {
		t.Fatal(err)
	}
	// Seed a VALID index of the target name (models a build that finished before the crash).
	if _, err := conn.Exec(ctx, "CREATE INDEX ix_t_email ON public.t (email)"); err != nil {
		t.Fatal(err)
	}
	var oidBefore uint32
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.ix_t_email')::oid").Scan(&oidBefore); err != nil {
		t.Fatal(err)
	}

	store, op := buildCICOp(t)
	if err := executeNonTransactionalOp(ctx, conn, store, op, true); err != nil {
		t.Fatalf("resume CIC (valid): %v", err)
	}
	// The index is untouched (same oid — not dropped and rebuilt).
	var oidAfter uint32
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.ix_t_email')::oid").Scan(&oidAfter); err != nil {
		t.Fatal(err)
	}
	if oidBefore != oidAfter {
		t.Errorf("valid index should be left as-is, oid %d -> %d", oidBefore, oidAfter)
	}
}

// TestResumeDropCICIdempotent: DROP INDEX CONCURRENTLY IF EXISTS is idempotent —
// a first run drops the present index, and a resume run (index already gone) is a
// clean no-op, not an error (roadmap L8: resume idempotent in Postgres's own
// state model).
func TestResumeDropCICIdempotent(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "CREATE TABLE public.t (email text NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "CREATE INDEX ix_t_email ON public.t (email)"); err != nil {
		t.Fatal(err)
	}

	store, err := objstore.New(t.TempDir(), uint32(enc.CodecVersion))
	if err != nil {
		t.Fatal(err)
	}
	op, err := DDLOpToSelfContained(store, DDLOp{
		Op:      "drop_index_concurrently",
		Table:   "public.t",
		Name:    "ix_t_email",
		Columns: []string{"email"},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Fresh run executes the drop (the precondition is the caller's concern now).
	if err := executeNonTransactionalOp(ctx, conn, store, op, false); err != nil {
		t.Fatalf("drop-CIC fresh run: %v", err)
	}
	if _, ok, _ := catalog.Index(ctx, conn, "public", "ix_t_email"); ok {
		t.Fatal("index should be gone after the drop")
	}
	// Resume run: the index is already gone; IF EXISTS makes it a clean no-op.
	if err := executeNonTransactionalOp(ctx, conn, store, op, true); err != nil {
		t.Fatalf("drop-CIC resume must be idempotent, got: %v", err)
	}
}

// TestPreconditionDropDriftMissingObject: a drop op's precondition (object PRESENT)
// hard-errors with a precise object/expected/found message when the object is
// absent — drift is loud (L5).
func TestPreconditionDropDriftMissingObject(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	store, err := objstore.New(t.TempDir(), uint32(enc.CodecVersion))
	if err != nil {
		t.Fatal(err)
	}
	op, err := DDLOpToSelfContained(store, DDLOp{Op: "drop_table", Table: "public.gone"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = checkPreconditions(ctx, conn, store, nil, op)
	if err == nil {
		t.Fatal("expected a precondition drift error for a missing drop target")
	}
	for _, want := range []string{"table public.gone", "expected present", "found absent"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestEnumAddPathSelection is the pre-12 vs 12+ path-selection unit test for
// version-conditional enum-add (roadmap: the postgres:11 leg is out of scope, so
// the non-transactional class is covered by this UNIT test, not a live run).
func TestEnumAddPathSelection(t *testing.T) {
	cases := []struct {
		pgVersion int
		wantNonTx bool
	}{
		{11, true},  // pre-12: ALTER TYPE ADD VALUE cannot run in a transaction
		{12, false}, // 12+: transactional
		{18, false},
		{0, true},   // unknown version: conservative (treat as non-transactional)
	}
	for _, c := range cases {
		got := IsNonTransactional(DDLOp{Op: "alter_enum_add_value", PGVersion: c.pgVersion})
		if got != c.wantNonTx {
			t.Errorf("pg%d: IsNonTransactional=%v, want %v", c.pgVersion, got, c.wantNonTx)
		}
		// Cross-check the capability registry is the single source of truth.
		if has := pgcap.Has(c.pgVersion, pgcap.TransactionalEnumAdd); has == c.wantNonTx {
			t.Errorf("pg%d: pgcap.Has(TransactionalEnumAdd)=%v inconsistent with wantNonTx=%v", c.pgVersion, has, c.wantNonTx)
		}
	}
}
