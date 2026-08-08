package testdb

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/testenv"
)

// refusingDSN returns a connection string for a loopback port that this process
// has just proved nothing is listening on. Dialing it fails immediately with
// ECONNREFUSED, which is the "a database was NAMED but does not work" case --
// the case every Require* helper has to resolve through PGDESIGN_REQUIRE_DB
// rather than by skipping.
func refusingDSN(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a loopback port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("closing the reserved port: %v", err)
	}
	return "postgres://nobody@" + addr + "/nothing?sslmode=disable&connect_timeout=2"
}

// TestUnavailableHonorsRequireDB pins the single decision point: a database
// that was named but does not work skips normally and FAILS under
// PGDESIGN_REQUIRE_DB=1.
func TestUnavailableHonorsRequireDB(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)

	rec := &recordingTB{TB: t}
	Unavailable(rec, "the server said %s", "no")
	if rec.failed {
		t.Fatalf("Unavailable failed without %s=1: %s", requireDBEnv, rec.failMsg)
	}
	if !rec.skipped {
		t.Fatal("Unavailable neither skipped nor failed without the require gate")
	}
	if !strings.Contains(rec.skipMsg, "the server said no") {
		t.Errorf("skip message lost the cause: %s", rec.skipMsg)
	}

	t.Setenv(requireDBEnv, "1")
	rec = &recordingTB{TB: t}
	Unavailable(rec, "the server said %s", "no")
	if rec.skipped {
		t.Fatalf("Unavailable skipped under %s=1: %s", requireDBEnv, rec.skipMsg)
	}
	if !rec.failed {
		t.Fatalf("Unavailable did not fail under %s=1", requireDBEnv)
	}
	if !strings.Contains(rec.failMsg, requireDBEnv) {
		t.Errorf("failure message does not name %s: %s", requireDBEnv, rec.failMsg)
	}
}

// TestRequireManagerFailsUnderRequireDBWhenTheServerRefuses is the regression
// test for the skip-after-RequireURL bug: every database-backed package resolved
// the DSN through the require-honoring RequireURL and then skipped on the very
// next line when the probe dial failed, so a lane that declared a database must
// exist passed green while running no database tests at all.
func TestRequireManagerFailsUnderRequireDBWhenTheServerRefuses(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)
	t.Setenv(ConnectionEnv, refusingDSN(t))
	t.Setenv(requireDBEnv, "1")

	rec := &recordingTB{TB: t}
	if mgr := RequireManager(rec); mgr != nil {
		t.Fatal("RequireManager returned a manager for a server that refuses connections")
	}
	if rec.skipped {
		t.Fatalf("RequireManager skipped under %s=1 with an unusable database: %s", requireDBEnv, rec.skipMsg)
	}
	if !rec.failed {
		t.Fatalf("RequireManager did not fail under %s=1 with an unusable database", requireDBEnv)
	}
}

// TestRequireManagerSkipsWithoutRequireDB is the other half: a developer with no
// database still gets a skip, never a failure.
func TestRequireManagerSkipsWithoutRequireDB(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)
	t.Setenv(ConnectionEnv, refusingDSN(t))

	rec := &recordingTB{TB: t}
	if mgr := RequireManager(rec); mgr != nil {
		t.Fatal("RequireManager returned a manager for a server that refuses connections")
	}
	if rec.failed {
		t.Fatalf("RequireManager failed without the require gate: %s", rec.failMsg)
	}
	if !rec.skipped {
		t.Fatal("RequireManager neither skipped nor failed without the require gate")
	}
}

// TestRequireConnHonorsRequireDB pins the same contract for the direct-connection
// helper, which replaced a raw os.Getenv(PGDESIGN_DB) + t.Skip in the CLI's
// live-imports tests -- a site the require gate could not reach at all.
func TestRequireConnHonorsRequireDB(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)
	dsn := refusingDSN(t)

	t.Setenv(ConnectionEnv, dsn)
	rec := &recordingTB{TB: t}
	if conn := RequireConn(rec, context.Background()); conn != nil {
		t.Fatal("RequireConn returned a connection to a server that refuses connections")
	}
	if rec.failed {
		t.Fatalf("RequireConn failed without the require gate: %s", rec.failMsg)
	}
	if !rec.skipped {
		t.Fatal("RequireConn neither skipped nor failed without the require gate")
	}

	t.Setenv(requireDBEnv, "1")
	rec = &recordingTB{TB: t}
	if conn := RequireConn(rec, context.Background()); conn != nil {
		t.Fatal("RequireConn returned a connection to a server that refuses connections")
	}
	if rec.skipped {
		t.Fatalf("RequireConn skipped under %s=1: %s", requireDBEnv, rec.skipMsg)
	}
	if !rec.failed {
		t.Fatalf("RequireConn did not fail under %s=1", requireDBEnv)
	}
}

// TestRequireHelpersHonorAnAbsentDSN pins that the absent-DSN verdict still
// comes from RequireURL: skip normally, fail under the require gate. Neither
// helper dials anything in this state -- there is nothing to dial.
func TestRequireHelpersHonorAnAbsentDSN(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)

	rec := &recordingTB{TB: t}
	RequireManager(rec)
	if rec.failed || !rec.skipped {
		t.Fatalf("RequireManager with no DSN: skipped=%v failed=%v (%s%s)",
			rec.skipped, rec.failed, rec.skipMsg, rec.failMsg)
	}

	t.Setenv(requireDBEnv, "1")
	rec = &recordingTB{TB: t}
	RequireManager(rec)
	if rec.skipped || !rec.failed {
		t.Fatalf("RequireManager with no DSN under %s=1: skipped=%v failed=%v",
			requireDBEnv, rec.skipped, rec.failed)
	}
}

// TestMainManagerFailsHardOnAnUnusableDSN is the TestMain-level half of the same
// rule, and the regression test for serve's "best-effort database setup": a DSN
// that cannot be turned into a working manager is exit 1 ALWAYS, not a quiet 0
// that lets the binary run its DB-free tests and report green.
func TestMainManagerFailsHardOnAnUnusableDSN(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)
	// A URL with no database name: NewManager rejects it without dialing.
	t.Setenv(ConnectionEnv, "postgres://nobody@127.0.0.1:1/")

	mgr, code, ok := MainManager()
	if ok || mgr != nil {
		t.Fatal("MainManager reported success for a DSN that carries no database name")
	}
	if code != 1 {
		t.Fatalf("MainManager returned exit code %d for an unusable DSN; want 1 "+
			"(a named database that does not work is a broken lane, not an absent one)", code)
	}
}

// TestMainManagerReportsAnAbsentDSNAsTheOnlyBenignCase pins the one case that
// may still end in a skip, and pins that the require gate overrides even that.
func TestMainManagerReportsAnAbsentDSNAsTheOnlyBenignCase(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)

	if mgr, code, ok := MainManager(); ok || mgr != nil || code != 0 {
		t.Fatalf("MainManager with no DSN: ok=%v code=%d; want ok=false code=0", ok, code)
	}

	t.Setenv(requireDBEnv, "1")
	if mgr, code, ok := MainManager(); ok || mgr != nil || code != 1 {
		t.Fatalf("MainManager with no DSN under %s=1: ok=%v code=%d; want ok=false code=1",
			requireDBEnv, ok, code)
	}
}
