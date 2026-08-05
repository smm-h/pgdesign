package migrate

import (
	"context"
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/rev"
)

// TestNonTxFreshDriftLeavesNoIntentRow is rider 5's red-green guard: on the FRESH
// path, a non-transactional op whose precondition FAILS (drift) must abort BEFORE
// the intent row is written, so a drifted op never leaves an orphan intent row in
// the journal. Pre-fix, the intent row was written and committed before the
// precondition ran inside executeNonTransactionalOp, so the orphan survived.
//
// The edge is a single genesis create_index_concurrently op targeting an index
// that ALREADY exists in the database (its precondition is index-MustBeAbsent),
// modelling an out-of-band index that drifted the world off the edge's from-state.
func TestNonTxFreshDriftLeavesNoIntentRow(t *testing.T) {
	testenv.Isolate(t)
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	// Out-of-band table + index: the index already present means the CIC op's
	// MustBeAbsent precondition fails on the fresh path.
	if _, err := conn.Exec(ctx, "CREATE TABLE public.t (email text NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "CREATE INDEX ix_t_email ON public.t (email)"); err != nil {
		t.Fatal(err)
	}

	// A genesis edge whose single op is the drifting concurrent-index create.
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	op, err := DDLOpToSelfContained(p.Store(), DDLOp{
		Op:      "create_index_concurrently",
		Table:   "public.t",
		Name:    "ix_t_email",
		Columns: []string{"email"},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := Edge{
		Parent: rev.Revision{}, // genesis
		Target: syntheticTarget(t),
		Slug:   "drift-cic",
		Class:  rev.RegistryPresent,
		Ops:    []SelfContainedOp{op},
	}
	if _, err := p.WriteEdge(e); err != nil {
		t.Fatalf("WriteEdge: %v", err)
	}

	// Apply must fail on the precondition (index already present).
	if _, err := ApplyChain(ctx, conn, p, "", "5s", nil); err == nil {
		t.Fatal("expected a precondition drift error on the concurrent-index create")
	}

	// The crux: NO intent row was journaled for the drifted op. Chain structures
	// were seeded (ensureChainStructures runs before the failing edge), so the
	// journal table exists and is queryable.
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pgdesign_migration_ops WHERE edge_id = $1", e.ID()).Scan(&n); err != nil {
		t.Fatalf("count journal rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("fresh drift left %d journal row(s) for the edge; expected 0 (no orphan intent)", n)
	}
}

// syntheticTarget mints a distinct non-zero revision for a synthetic genesis edge
// whose target manifest is irrelevant to this test (apply fails before advancing).
func syntheticTarget(t *testing.T) rev.Revision {
	t.Helper()
	r, err := rev.Compute(twoTableDesired(), rev.RegistryPresent)
	if err != nil {
		t.Fatalf("rev.Compute: %v", err)
	}
	return r
}
