package testdb

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/smm-h/stricttest/go/pgcluster"
)

const (
	// ConnectionEnv is the single environment variable every database-backed
	// test resolves its connection string from. It is the same variable the CLI
	// registers with strictcli's WithConnectionEnv, so a test and the binary it
	// exercises can never disagree about where the database is.
	ConnectionEnv = "PGDESIGN_DB"

	// requireDBEnv turns an absent or unreachable database from a skip into a
	// hard failure. CI lanes that provision PostgreSQL set it so a provisioning
	// regression cannot pass as a green run full of skips.
	requireDBEnv = "PGDESIGN_REQUIRE_DB"

	// requirePartmanEnv is the same declaration for pg_partman, which is a
	// separate provisioning decision: a lane can have PostgreSQL without it.
	requirePartmanEnv = "PGDESIGN_REQUIRE_PARTMAN"
)

// noDatabaseMessage is the one explanation every guard gives when there is no
// database. It names the variable, and it names the two ways to get one --
// deliberately not a host, because there is no host this suite is entitled to
// guess at.
const noDatabaseMessage = ConnectionEnv + " is not set, so there is no PostgreSQL to test against. " +
	"A test binary whose TestMain calls testdb.RunWithCluster boots its own ephemeral cluster " +
	"when the PostgreSQL binaries (initdb, pg_ctl, psql) are installed; otherwise set " +
	ConnectionEnv + " to point the suite at a server you have chosen"

// DatabaseURL reports the configured connection string and whether one exists.
//
// There is deliberately NO default. A suite with no PGDESIGN_DB has no
// database and every database-backed test skips: it never probes localhost, so
// it can never reach, write to, or drop databases from a PostgreSQL server that
// happens to be running on the developer's own machine. The database a test
// runs against is either one the caller named or one the suite booted for
// itself -- never one it stumbled into.
func DatabaseURL() (string, bool) {
	url := os.Getenv(ConnectionEnv)
	if url == "" {
		return "", false
	}
	return url, true
}

// RequireDB reports whether this lane has declared that a database must exist.
func RequireDB() bool {
	return os.Getenv(requireDBEnv) == "1"
}

// RequirePartman reports whether this lane has declared that pg_partman must be
// available.
func RequirePartman() bool {
	return os.Getenv(requirePartmanEnv) == "1"
}

// RequireURL returns the connection string for a test that needs a database,
// skipping t when there is none (or failing it under PGDESIGN_REQUIRE_DB=1).
//
// It is the TB-bound counterpart of [DatabaseURL]: the resolution and the
// verdict live in one place, so no package can grow its own idea of where the
// database is.
func RequireURL(t testing.TB) string {
	t.Helper()
	url, ok := DatabaseURL()
	if ok {
		return url
	}
	if RequireDB() {
		t.Fatalf("PostgreSQL required (%s=1) but %s", requireDBEnv, noDatabaseMessage)
		return ""
	}
	t.Skipf("%s", noDatabaseMessage)
	return ""
}

// SkipIfNoTCPHost skips t when dbURL reaches PostgreSQL over a unix socket
// rather than over a host and port.
//
// It exists for the JDBC lanes and for nothing else. pgjdbc has no unix-socket
// transport at all, so a generated Java or Kotlin wrapper cannot reach the
// ephemeral cluster this suite boots -- the cluster listens on a socket and
// deliberately opens no TCP port. That is a property of the driver, not a
// defect in the generated wrapper, which says the same thing in its own
// toJdbcUrl. Point PGDESIGN_DB at a TCP-reachable server to run these lanes.
func SkipIfNoTCPHost(t testing.TB, dbURL string) {
	t.Helper()
	u, err := url.Parse(dbURL)
	if err != nil {
		t.Fatalf("parsing %s: %v", ConnectionEnv, err)
		return
	}
	if u.Host == "" {
		t.Skipf("the configured database is reached over a unix socket (%s), and the "+
			"PostgreSQL JDBC driver has no unix-socket transport; point %s at a "+
			"TCP-reachable server to run this lane", dbURL, ConnectionEnv)
	}
}

// MainNoDatabase is the exit code for a TestMain that has no database to give
// its tests: 0, which skips the binary, unless PGDESIGN_REQUIRE_DB=1 declares
// that this lane must have one -- then it is 1, because a lane that provisions
// PostgreSQL and silently runs no database tests is the failure this whole
// arrangement exists to prevent.
//
// cause explains what went wrong, and is nil when the DSN was simply never set.
func MainNoDatabase(cause error) int {
	reason := noDatabaseMessage
	if cause != nil {
		reason = cause.Error()
	}
	if RequireDB() {
		fmt.Fprintf(os.Stderr, "PostgreSQL required (%s=1) but %s\n", requireDBEnv, reason)
		return 1
	}
	fmt.Fprintf(os.Stderr, "skipping this package's database-backed tests: %s\n", reason)
	return 0
}

// RunWithCluster boots one ephemeral PostgreSQL cluster for the whole test
// binary, exports its base URL under PGDESIGN_DB, calls run, and shuts the
// cluster down again. It returns run's exit code, so a TestMain is:
//
//	func TestMain(m *testing.M) {
//		os.Exit(testdb.RunWithCluster(func() int { return m.Run() }))
//	}
//
// The cluster is a real postmaster on a private tmpfs directory listening on a
// unix socket and nothing else. It inherits the host's extension library, so
// pg_partman and pgvector are available exactly when the machine has them
// installed -- which is what the CI lane provisions instead of a service
// container.
//
// Three cases, and no fourth:
//
//   - PGDESIGN_DB is already set. The caller named a server; that choice is
//     never second-guessed and no cluster is booted.
//   - PostgreSQL is not usable on this machine. PGDESIGN_DB stays unset and
//     every database-backed test skips -- unless PGDESIGN_REQUIRE_DB=1, which
//     makes it a hard failure.
//   - The cluster fails to start for any other reason. That is a hard failure,
//     never a quiet fall back to skipping.
func RunWithCluster(run func() int) int {
	if _, ok := DatabaseURL(); ok {
		return run()
	}

	cluster, err := pgcluster.Start(ConnectionEnv)
	switch {
	case errors.Is(err, pgcluster.ErrPostgresUnavailable):
		if RequireDB() {
			fmt.Fprintf(os.Stderr,
				"PostgreSQL required (%s=1) but no ephemeral cluster could be started: %v\n",
				requireDBEnv, err)
			return 1
		}
		return run()
	case err != nil:
		fmt.Fprintf(os.Stderr, "starting the ephemeral PostgreSQL cluster: %v\n", err)
		return 1
	}
	defer cluster.Stop()

	return run()
}
