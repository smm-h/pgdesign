package testdb

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smm-h/pgdesign/internal/testenv"
)

// recordingTB stands in for a real testing.TB so a test can observe what a
// guard did to its caller instead of being skipped or failed by it.
//
// testing.TB cannot be implemented outside the testing package (it carries an
// unexported method), so the real TB is embedded and only the methods under
// test are overridden. Every guard here returns after calling Skipf or Fatalf
// rather than relying on runtime.Goexit, which is what makes the embedding
// approach honest: control really does come back.
type recordingTB struct {
	testing.TB
	skipped  bool
	skipMsg  string
	failed   bool
	failMsg  string
	logLines []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Skipf(format string, args ...any) {
	r.skipped = true
	r.skipMsg = fmt.Sprintf(format, args...)
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.failed = true
	r.failMsg = fmt.Sprintf(format, args...)
}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.failed = true
	r.failMsg = fmt.Sprintf(format, args...)
}

func (r *recordingTB) Logf(format string, args ...any) {
	r.logLines = append(r.logLines, fmt.Sprintf(format, args...))
}

// scrubAmbientPostgres removes PGDESIGN_DB and every ambient PG* variable for
// the duration of t. PG* matters because libpq (and therefore pgx) fills in
// every connection field a DSN leaves unspecified from the environment: an
// ambient PGHOST/PGPORT is a second, quieter route to a developer's own server.
func scrubAmbientPostgres(t *testing.T) {
	t.Helper()
	testenv.Unset(t, ConnectionEnv)
	testenv.Unset(t, requireDBEnv)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "PG") {
			testenv.Unset(t, name)
		}
	}
}

// TestDatabaseURLHasNoDefault pins the absence of a fallback DSN. A default
// here is what made this suite connect to whatever PostgreSQL happened to be
// listening on the developer's own machine, create databases in it, and drop
// them again.
func TestDatabaseURLHasNoDefault(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)

	url, ok := DatabaseURL()
	if ok {
		t.Fatalf("DatabaseURL reported a connection string with PGDESIGN_DB unset: %q", url)
	}
	if url != "" {
		t.Fatalf("DatabaseURL returned %q with PGDESIGN_DB unset; want the empty string", url)
	}
}

// TestSkipIfNoPostgresSkipsWhenUnset is the behavioral half: an absent
// PGDESIGN_DB skips, and the message says which variable is missing rather than
// reporting a connection failure against a host nobody asked for.
func TestSkipIfNoPostgresSkipsWhenUnset(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)

	rec := &recordingTB{TB: t}
	SkipIfNoPostgres(rec)

	if rec.failed {
		t.Fatalf("SkipIfNoPostgres failed instead of skipping: %s", rec.failMsg)
	}
	if !rec.skipped {
		t.Fatal("SkipIfNoPostgres neither skipped nor failed with PGDESIGN_DB unset")
	}
	if !strings.Contains(rec.skipMsg, ConnectionEnv) {
		t.Errorf("skip message does not name %s: %s", ConnectionEnv, rec.skipMsg)
	}
	if strings.Contains(rec.skipMsg, "localhost") {
		t.Errorf("skip message names a default host, so a default DSN still exists: %s", rec.skipMsg)
	}
}

// TestSkipIfNoPartmanSkipsWhenUnset is the same pin for the partman guard,
// which resolved the DSN through the same dead default.
func TestSkipIfNoPartmanSkipsWhenUnset(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)

	rec := &recordingTB{TB: t}
	if info := SkipIfNoPartman(rec); info != nil {
		t.Fatalf("SkipIfNoPartman returned %+v with PGDESIGN_DB unset", info)
	}
	if rec.failed {
		t.Fatalf("SkipIfNoPartman failed instead of skipping: %s", rec.failMsg)
	}
	if !rec.skipped {
		t.Fatal("SkipIfNoPartman neither skipped nor failed with PGDESIGN_DB unset")
	}
	if !strings.Contains(rec.skipMsg, ConnectionEnv) {
		t.Errorf("skip message does not name %s: %s", ConnectionEnv, rec.skipMsg)
	}
}

// TestRequireDBTurnsAnAbsentDSNFatal pins the CI half of the contract: a lane
// that declares PGDESIGN_REQUIRE_DB=1 must fail loudly when there is no
// database, never skip its way to a green run.
func TestRequireDBTurnsAnAbsentDSNFatal(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)
	t.Setenv(requireDBEnv, "1")

	rec := &recordingTB{TB: t}
	SkipIfNoPostgres(rec)

	if rec.skipped {
		t.Fatalf("SkipIfNoPostgres skipped under %s=1: %s", requireDBEnv, rec.skipMsg)
	}
	if !rec.failed {
		t.Fatalf("SkipIfNoPostgres did not fail under %s=1 with no database", requireDBEnv)
	}
	if !strings.Contains(rec.failMsg, ConnectionEnv) {
		t.Errorf("failure message does not name %s: %s", ConnectionEnv, rec.failMsg)
	}
}

// TestNoDialWithoutADSN is the meta-test for the whole adoption: with
// PGDESIGN_DB unset, the guards must not open a connection to ANYTHING --
// not to a hardcoded default, and not to whatever an ambient PGHOST/PGPORT
// names.
//
// The proof is a real listener. Every ambient PG* connection variable is
// pointed at it, so any dial that libpq-style environment resolution could
// still produce lands here and is counted. A count above zero means the suite
// reached a server it was never told about.
func TestNoDialWithoutADSN(t *testing.T) {
	testenv.Isolate(t)
	scrubAmbientPostgres(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on loopback: %v", err)
	}
	defer listener.Close()

	var accepted atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			conn.Close()
		}
	}()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("splitting the listener address: %v", err)
	}
	t.Setenv("PGHOST", host)
	t.Setenv("PGPORT", port)
	t.Setenv("PGDATABASE", "postgres")
	t.Setenv("PGUSER", "postgres")

	rec := &recordingTB{TB: t}
	SkipIfNoPostgres(rec)
	partmanRec := &recordingTB{TB: t}
	SkipIfNoPartman(partmanRec)

	listener.Close()
	<-done

	if n := accepted.Load(); n != 0 {
		t.Fatalf("the guards dialed the ambient PG* target %d time(s) with %s unset; "+
			"the suite can still reach a server nobody pointed it at",
			n, ConnectionEnv)
	}
	if !rec.skipped || !partmanRec.skipped {
		t.Fatalf("guards did not skip: postgres skipped=%v partman skipped=%v",
			rec.skipped, partmanRec.skipped)
	}
	if _, err := strconv.Atoi(port); err != nil {
		t.Fatalf("listener port %q is not numeric", port)
	}
}
