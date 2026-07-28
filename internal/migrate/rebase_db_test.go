package migrate

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/rev"
)

// TestRebaseServedForwardApply proves that a database stamped at a rebased-away
// revision APPLIES FORWARD via the remap (roadmap 5.10 Verify): apply consults
// the remap before declaring a position unreachable, so the database is served,
// never orphaned.
//
// Setup: a linear branch (genesis base -> +rebase_b) is applied to a real
// database, leaving it physically at the rebased revision r3. The forked project
// is then rebased. Applying the forked project against that database must find the
// served-forward path via the remap (canon(r3) -> the re-parented revision) and
// apply the remaining re-parented tail edge — not raise a NoPathError.
func TestRebaseServedForwardApply(t *testing.T) {
	edb := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, edb.URL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// 1. Bring the database physically to r3 (base + rebase_b) via a LINEAR chain.
	pLinear, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := twoTableDesired()
	r1 := buildEdge(t, pLinear, nil, base, rev.Revision{}, "genesis")
	branchB := withExtraTable("rebase_b")
	r3lin := buildEdge(t, pLinear, base, branchB, r1, "rebase-b")
	if _, err := ApplyChain(ctx, conn, pLinear, "", "5s", nil); err != nil {
		t.Fatalf("linear apply: %v", err)
	}
	cp, ok, err := readChainPosition(ctx, conn)
	if err != nil || !ok {
		t.Fatalf("read chain position: %v ok=%v", err, ok)
	}
	if cp.CurrentRevision != r3lin.String() {
		t.Fatalf("database should be at r3 %s, got %s", r3lin, cp.CurrentRevision)
	}

	// 2. Build the forked project and rebase branch B onto the kept branch A.
	p, _, r2, r3, _ := forkChain(t)
	if r3.String() != r3lin.String() {
		t.Fatalf("content-addressed r3 mismatch: linear %s vs forked %s", r3lin, r3)
	}
	res, err := RebaseChain(p, r2.String())
	if err != nil {
		t.Fatalf("RebaseChain: %v", err)
	}
	if res.Remap[r3.String()] == "" {
		t.Fatalf("remap must map the rebased-away r3")
	}

	// 3. Apply the forked+rebased project against the database at r3. The remap
	// serves it forward: it must apply the remaining re-parented tail edge (creating
	// rebase_c) and advance the position, never a NoPathError. dbURL="" skips the
	// post-apply reconcile (the rebase-only divergent-content caveat: the kept
	// branch's rebase_c rides forward, but rebase_a is a 3-way-merge concern that is
	// out of scope — see the rebase report).
	applied, err := ApplyChain(ctx, conn, p, "", "5s", nil)
	if err != nil {
		t.Fatalf("served-forward apply must succeed (not orphan): %v", err)
	}
	if len(applied) == 0 {
		t.Fatalf("expected the served-forward apply to apply the re-parented tail")
	}

	// The re-parented tail created rebase_c on the database.
	if !relExists(t, ctx, conn, "rebase_c") {
		t.Fatalf("served-forward apply should have created rebase_c")
	}
}
