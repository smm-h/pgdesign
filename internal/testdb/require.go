package testdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// probeTimeout bounds the dial every Require* helper makes to prove the named
// server actually answers. Short enough that an unreachable host produces a
// verdict rather than a stalled test binary, long enough for a busy one.
const probeTimeout = 2 * time.Second

// Unavailable delivers the verdict for a database that WAS named but could not
// be used: the server refused the dial, the URL would not parse, a manager
// could not be built from it. It skips t normally and FAILS t under
// PGDESIGN_REQUIRE_DB=1.
//
// It exists because "resolve the DSN through RequireURL, then t.Skipf on the
// first probe failure" was the shape most database-backed packages grew
// independently, and it silently defeated the require gate: the lane declared
// that a database must exist, RequireURL agreed one was named, and the test
// skipped anyway the moment the server did not answer -- so a provisioning
// regression produced a green run full of skips, which is precisely what the
// require gate exists to prevent. Every failure downstream of DSN resolution
// goes through here, so the skip-or-fail decision lives in ONE place.
func Unavailable(t testing.TB, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	if RequireDB() {
		t.Fatalf("PostgreSQL required (%s=1) but not available: %s", requireDBEnv, msg)
		return
	}
	t.Skipf("PostgreSQL not available: %s", msg)
}

// RequireManager returns an ephemeral-database Manager for the configured
// server, having first proved the server answers.
//
// It is the one way a test obtains a Manager: no package builds one from a raw
// [NewManager] call, so no package can decide on its own that a broken database
// is a reason to skip. Absent DSN -> [RequireURL]'s verdict; named-but-unusable
// -> [Unavailable]'s verdict.
func RequireManager(t testing.TB) *Manager {
	t.Helper()
	dbURL := RequireURL(t)
	if dbURL == "" {
		// RequireURL has already skipped or failed t. With a real *testing.T
		// this line is unreachable (Skipf/Fatalf call runtime.Goexit); it is
		// reached only by the recording stand-in the guard tests use.
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	probe, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		Unavailable(t, "connecting to the configured database: %v", err)
		return nil
	}
	probe.Close(ctx)

	mgr, err := NewManager(dbURL)
	if err != nil {
		Unavailable(t, "building an ephemeral-database manager: %v", err)
		return nil
	}
	return mgr
}

// RequireEphemeralDB creates a throwaway database for t and registers its
// teardown. It is [RequireManager] followed by [Manager.SetupForTest], which is
// what nearly every database-backed test actually wants.
func RequireEphemeralDB(t testing.TB) *EphemeralDB {
	t.Helper()
	mgr := RequireManager(t)
	if mgr == nil {
		return nil
	}
	return mgr.SetupForTest(t, CreateOptions{})
}

// RequireConn opens a connection to the configured database itself (not to a
// throwaway one) and closes it when t finishes.
//
// Tests that need to touch the server directly -- resetting a schema, running
// a fixture as the maintenance user -- use this instead of reading the
// connection env and dialing by hand, so the absent/unusable verdicts are the
// same ones every other database-backed test gets.
func RequireConn(t testing.TB, ctx context.Context) *pgx.Conn {
	t.Helper()
	dbURL := RequireURL(t)
	if dbURL == "" {
		return nil
	}

	dialCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	conn, err := pgx.Connect(dialCtx, dbURL)
	if err != nil {
		Unavailable(t, "connecting to the configured database: %v", err)
		return nil
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

// MainManager resolves the database for a whole test binary and returns a
// Manager for it. It is the TestMain-level counterpart of [RequireManager],
// where there is no testing.TB to skip or fail and the verdict has to be an
// exit code.
//
// Three outcomes, and no fourth:
//
//   - No DSN at all. mgr is nil, ok is false, and code is [MainNoDatabase]'s:
//     0 so the binary's DB-free tests can still run, or 1 under
//     PGDESIGN_REQUIRE_DB=1. An absent database is the ONLY legitimate reason a
//     binary runs no database tests.
//   - A DSN that cannot be turned into a working Manager. ok is false and code
//     is 1, ALWAYS -- regardless of PGDESIGN_REQUIRE_DB. A named database that
//     does not work is a broken lane, not an absent one; "best-effort setup,
//     skip everything on failure" is exactly how a green run with zero database
//     coverage happens, and it is banned here.
//   - A working Manager. mgr is non-nil, ok is true, code is 0.
func MainManager() (mgr *Manager, code int, ok bool) {
	dbURL, has := DatabaseURL()
	if !has {
		return nil, MainNoDatabase(nil), false
	}
	m, err := NewManager(dbURL)
	if err != nil {
		return nil, MainFailed(fmt.Errorf("building an ephemeral-database manager: %w", err)), false
	}
	return m, 0, true
}

// MainFailed is the exit code for a TestMain whose database setup FAILED after
// a DSN was resolved: 1, unconditionally. It is the counterpart of
// [MainNoDatabase], which reports the one benign case (no DSN at all); every
// other setup outcome is a failure and never a skip.
func MainFailed(cause error) int {
	fmt.Fprintf(os.Stderr,
		"%s names a database, but this test binary could not set it up: %v\n"+
			"A named database that does not work is a broken lane, not an absent one -- "+
			"failing instead of running zero database tests.\n",
		ConnectionEnv, cause)
	return 1
}
