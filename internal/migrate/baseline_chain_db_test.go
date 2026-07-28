package migrate

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/introspect"
)

// TestBaselineChainAdoptsForeignDatabase pins the chain-mode baseline: a database
// whose schema was created by other means is adopted onto the chain by
// synthesizing a genesis edge from introspection and stamping a baseline boundary
// (roadmap 5.10). Re-running is an idempotent no-op.
func TestBaselineChainAdoptsForeignDatabase(t *testing.T) {
	edb := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, edb.URL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// A FOREIGN database: a table created directly, no pgdesign structures.
	if _, err := conn.Exec(ctx, `CREATE TABLE widget (id bigint PRIMARY KEY, label text NOT NULL);
		COMMENT ON TABLE widget IS 'widgets';`); err != nil {
		t.Fatalf("create foreign schema: %v", err)
	}

	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	actual, diags, err := introspect.Introspect(ctx, edb.URL, []string{"public"})
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		t.Fatalf("introspect diags: %v", diags)
	}

	report, err := BaselineChain(ctx, conn, p, actual, "adopt foreign db")
	if err != nil {
		t.Fatalf("BaselineChain: %v", err)
	}
	if report.AlreadyAtBaseline {
		t.Fatal("first baseline should not report already-at-baseline")
	}

	// chain_position is stamped at the baseline target with boundary_kind='baseline'.
	cp, ok, err := readChainPosition(ctx, conn)
	if err != nil || !ok {
		t.Fatalf("read chain position: %v ok=%v", err, ok)
	}
	if cp.CurrentRevision != report.Target {
		t.Fatalf("position %s != baseline target %s", cp.CurrentRevision, report.Target)
	}
	if cp.BoundaryKind != "baseline" {
		t.Fatalf("boundary_kind = %q, want baseline", cp.BoundaryKind)
	}
	if cp.BoundaryRevision != report.Target {
		t.Fatalf("boundary_revision %s != target %s", cp.BoundaryRevision, report.Target)
	}

	// The store, manifest, and baseline edge are mutually consistent.
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("VerifyChainConsistency after baseline: %v", err)
	}

	// Idempotent: re-running against the unchanged database is a no-op.
	report2, err := BaselineChain(ctx, conn, p, actual, "adopt foreign db")
	if err != nil {
		t.Fatalf("second BaselineChain: %v", err)
	}
	if !report2.AlreadyAtBaseline {
		t.Fatal("re-baselining an unchanged database should be a no-op")
	}
}
