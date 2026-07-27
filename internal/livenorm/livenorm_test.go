package livenorm

import (
	"context"
	"os"
	"strings"
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

// TestRoundTripNamespaceScoped is the concurrent-session regression: two
// sessions each create a temp table with the SAME name (_pgd_rt_1) carrying a
// _pgd_c constraint (per-session counters both start at 0, so the names are not
// globally unique). Both temp tables are visible in the shared catalog, so a
// lookup keyed only on relname + conname collides across sessions. The fix
// scopes the lookup to pg_my_temp_schema(), isolating each session to its own
// temp object. This test proves the collision exists (unscoped count == 2) and
// that the scoped lookup returns each session's OWN constraint.
func TestRoundTripNamespaceScoped(t *testing.T) {
	testdb.SkipIfNoPostgres(t)
	ctx := context.Background()
	url := testDBURL()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, `DROP TABLE IF EXISTS livenorm_ns`); err != nil {
		t.Fatalf("drop pre-existing: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE TABLE livenorm_ns (price int NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer admin.Exec(ctx, `DROP TABLE IF EXISTS livenorm_ns`)

	// Two independent sessions, each with a colliding _pgd_rt_1 / _pgd_c.
	setup := func(check string) *pgx.Conn {
		c, err := pgx.Connect(ctx, url)
		if err != nil {
			t.Skipf("connect session: %v", err)
		}
		if _, err := c.Exec(ctx, `CREATE TEMP TABLE _pgd_rt_1 (LIKE livenorm_ns)`); err != nil {
			t.Fatalf("create temp: %v", err)
		}
		if _, err := c.Exec(ctx, `ALTER TABLE _pgd_rt_1 ADD CONSTRAINT _pgd_c CHECK (`+check+`)`); err != nil {
			t.Fatalf("add check: %v", err)
		}
		return c
	}
	connA := setup("price > 111")
	defer connA.Close(ctx)
	connB := setup("price > 222")
	defer connB.Close(ctx)

	// The collision is real: keyed on relname+conname alone, both sessions'
	// constraints match.
	var unscoped int
	if err := connA.QueryRow(ctx,
		`SELECT count(*) FROM pg_constraint c JOIN pg_class r ON r.oid = c.conrelid
		  WHERE r.relname = '_pgd_rt_1' AND c.conname = '_pgd_c'`).Scan(&unscoped); err != nil {
		t.Fatalf("unscoped count: %v", err)
	}
	if unscoped < 2 {
		t.Fatalf("expected the cross-session collision to be visible (>=2 matches), got %d", unscoped)
	}

	// The scoped lookup (the one roundTrip uses) returns each session's OWN def.
	scoped := func(c *pgx.Conn) string {
		var def string
		if err := c.QueryRow(ctx,
			`SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c
			   JOIN pg_class r ON r.oid = c.conrelid
			  WHERE r.relname = '_pgd_rt_1' AND c.conname = '_pgd_c'
			    AND r.relnamespace = pg_my_temp_schema()`).Scan(&def); err != nil {
			t.Fatalf("scoped lookup: %v", err)
		}
		return def
	}
	if defA := scoped(connA); !strings.Contains(defA, "111") {
		t.Errorf("session A got the wrong constraint def: %q", defA)
	}
	if defB := scoped(connB); !strings.Contains(defB, "222") {
		t.Errorf("session B got the wrong constraint def: %q", defB)
	}
}
