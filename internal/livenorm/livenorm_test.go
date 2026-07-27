package livenorm

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/smm-h/pgdesign/internal/sqlparse"
	"github.com/smm-h/pgdesign/internal/testdb"
)

func testDBURL() string {
	u := os.Getenv("PGDESIGN_DB")
	if u == "" {
		u = "postgres://localhost:5432/postgres?sslmode=disable"
	}
	return u
}

// setupTable creates a real table the round-trip can clone (LIKE), and returns
// a Normalizer plus a cleanup func. Skips cleanly without Postgres.
func setupTable(t *testing.T) (*Normalizer, func()) {
	t.Helper()
	testdb.SkipIfNoPostgres(t)

	ctx := context.Background()
	url := testDBURL()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, `DROP TABLE IF EXISTS livenorm_rt`); err != nil {
		admin.Close(ctx)
		t.Fatalf("drop pre-existing: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE TABLE livenorm_rt (status text NOT NULL, price int NOT NULL, kind int NOT NULL)`); err != nil {
		admin.Close(ctx)
		t.Fatalf("create table: %v", err)
	}

	n, err := New(ctx, url)
	if err != nil {
		admin.Close(ctx)
		t.Fatalf("new normalizer: %v", err)
	}
	cleanup := func() {
		n.Close()
		_, _ = admin.Exec(ctx, `DROP TABLE IF EXISTS livenorm_rt`)
		admin.Close(ctx)
	}
	return n, cleanup
}

// TestRoundTripConvergence is the round-trip suite: equivalently-spelled
// desired/introspected pairs converge via the DB. The desired form is
// round-tripped; the introspected form is what PG stores; both must normalize
// to the same string.
func TestRoundTripConvergence(t *testing.T) {
	n, cleanup := setupTable(t)
	defer cleanup()

	cases := []struct {
		desired      string
		introspected string
	}{
		// Catalog-dependent cast materialization — the residue pure N cannot
		// reach. Only the round-trip resolves it.
		{"status = 'active'", "status = 'active'::text"},
		// Catalog-independent classes still converge through the round-trip.
		{"price != 0", "price <> 0"},
		{"kind IN (1, 2, 3)", "kind = ANY (ARRAY[1, 2, 3])"},
	}
	for _, c := range cases {
		got := n.NormalizeExprForTable("public", "livenorm_rt", c.desired)
		want := sqlparse.NormalizeExpr(n.NormalizeExprForTable("public", "livenorm_rt", c.introspected))
		if got != want {
			t.Errorf("round-trip did not converge:\n  desired %q -> %q\n  introspected %q -> %q",
				c.desired, got, c.introspected, want)
		}
	}
}

// TestTempObjectCleanup verifies the throwaway temp table is always dropped —
// no _pgd_rt_% relation survives a normalization call.
func TestTempObjectCleanup(t *testing.T) {
	n, cleanup := setupTable(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		_ = n.NormalizeExprForTable("public", "livenorm_rt", "status = 'active'")
	}

	var leftover int
	err := n.conn.QueryRow(n.ctx,
		`SELECT count(*) FROM pg_class c
		   JOIN pg_namespace ns ON ns.oid = c.relnamespace
		  WHERE ns.nspname LIKE 'pg_temp%' AND c.relname LIKE '_pgd_rt_%'`,
	).Scan(&leftover)
	if err != nil {
		t.Fatalf("count temp: %v", err)
	}
	if leftover != 0 {
		t.Errorf("temp-object leak: %d _pgd_rt_%% relations survived", leftover)
	}
}

// TestForwardSimulationFallback is the minimal-residue-rule suite: where the
// round-trip cannot reach (an absent table), normalization falls to N, the
// forward-simulation rule set — deterministically, by reachability.
func TestForwardSimulationFallback(t *testing.T) {
	n, cleanup := setupTable(t)
	defer cleanup()

	expr := "status = 'active'"
	got := n.NormalizeExprForTable("public", "does_not_exist_table", expr)
	want := sqlparse.NormalizeExpr(expr)
	if got != want {
		t.Errorf("forward-sim fallback: got %q, want N form %q", got, want)
	}
	// N alone must NOT reach the catalog residue (proving the round-trip is
	// what adds the ::text on the reachable path).
	if got == sqlparse.NormalizeExpr("status = 'active'::text") {
		t.Error("forward-sim unexpectedly materialized the catalog cast")
	}
}
