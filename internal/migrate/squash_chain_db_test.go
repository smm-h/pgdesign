package migrate

import (
	"context"
	"testing"

	"github.com/smm-h/pgdesign/internal/rev"
)

// TestSquashChainAppliedRangeResumeDB is the headline 5.3 DB case: squash of an
// APPLIED range via consolidation, where a mid-range database resumes by walking
// the archived originals through the path-finder — and no tracking/journal rows
// are orphaned (applied history keeps referencing archived edge_ids, which stay
// resolvable).
func TestSquashChainAppliedRangeResumeDB(t *testing.T) {
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
	m1 := tableModel("a")
	m2 := tableModel("a", "b")
	m3 := tableModel("a", "b", "c")
	r1 := appendEdge(t, p, m1, nil, rev.Revision{}, "create-a", "")
	r2 := appendEdge(t, p, m2, m1, r1, "create-b", "")

	// Apply e1;e2 -> the DB lands MID-RANGE at R2, with e2 applied.
	if _, err := ApplyChain(ctx, conn, p, "", "5s", nil); err != nil {
		t.Fatalf("ApplyChain (e1;e2): %v", err)
	}
	pos, _, err := readChainPosition(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if pos.CurrentRevision != r2.String() {
		t.Fatalf("position after e1;e2 = %s, want R2 %s", pos.CurrentRevision, r2)
	}

	// Capture e2's id (an APPLIED edge) before it is superseded.
	liveBefore, _ := p.LoadLiveEdges()
	var e2ID string
	for _, e := range liveBefore {
		if eq, _ := e.Parent.Equal(r1); eq {
			e2ID = e.ID()
		}
	}
	if e2ID == "" {
		t.Fatal("could not find e2 before squash")
	}
	e2Rows := countJournalRows(t, ctx, conn, e2ID)
	if e2Rows == 0 {
		t.Fatal("expected e2 to have journal rows after apply")
	}

	// Add e3, then squash the APPLIED range e2;e3 into a consolidation.
	r3 := appendEdge(t, p, m3, m2, r2, "create-c", "")
	res, err := SquashChain(p, r1.String(), r3.String(), "")
	if err != nil {
		t.Fatalf("SquashChain (applied range): %v", err)
	}
	if len(res.SupersededIDs) != 2 {
		t.Fatalf("superseded = %d, want 2", len(res.SupersededIDs))
	}

	// The DB is still stamped mid-range at R2. Apply resumes via the ARCHIVED
	// original e3 (the live consolidation's parent R1 is upstream of R2, so it is
	// not applicable — the path-finder walks the archive).
	applied, err := ApplyChain(ctx, conn, p, "", "5s", nil)
	if err != nil {
		t.Fatalf("ApplyChain (resume from mid-range): %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("expected 1 edge applied on resume, got %d (%v)", len(applied), applied)
	}
	pos2, _, err := readChainPosition(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if pos2.CurrentRevision != r3.String() {
		t.Errorf("position after resume = %s, want R3 %s", pos2.CurrentRevision, r3)
	}
	// The mid-range table c was created.
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.c') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("resume apply should have created public.c")
	}

	// No orphaned rows: e2's applied-history rows survive and its edge stays
	// resolvable from the archive.
	if got := countJournalRows(t, ctx, conn, e2ID); got != e2Rows {
		t.Errorf("e2 journal rows changed after squash: %d -> %d (must be untouched)", e2Rows, got)
	}
	arch, err := p.LoadArchivedEdges()
	if err != nil {
		t.Fatal(err)
	}
	var e2Archived bool
	for _, e := range arch {
		if e.ID() == e2ID {
			e2Archived = true
		}
	}
	if !e2Archived {
		t.Error("e2 must be resolvable from the archive after being superseded")
	}
}

// TestSquashChainApplyFromGenesisViaConsolidationDB: a FRESH database applies the
// post-squash chain, using the consolidation edge directly (genesis -> head is
// shortest through it), and lands at the same head a sequential apply would.
func TestSquashChainApplyFromGenesisViaConsolidationDB(t *testing.T) {
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	p, _, _, _, r1, _, r3 := threeEdgeChain(t)
	if _, err := SquashChain(p, r1.String(), r3.String(), ""); err != nil {
		t.Fatalf("SquashChain: %v", err)
	}

	applied, err := ApplyChain(ctx, conn, p, "", "5s", nil)
	if err != nil {
		t.Fatalf("ApplyChain: %v", err)
	}
	// genesis edge + consolidation = 2 edges.
	if len(applied) != 2 {
		t.Fatalf("expected 2 edges applied (genesis + consolidation), got %d (%v)", len(applied), applied)
	}
	pos, _, err := readChainPosition(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if pos.CurrentRevision != r3.String() {
		t.Errorf("final position = %s, want R3 %s", pos.CurrentRevision, r3)
	}
	for _, tbl := range []string{"a", "b", "c"} {
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT to_regclass('public.'||$1) IS NOT NULL", tbl).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("table public.%s should exist after apply-via-consolidation", tbl)
		}
	}
}
