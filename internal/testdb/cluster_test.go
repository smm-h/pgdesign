package testdb

import (
	"os"
	"testing"

	"github.com/smm-h/pgdesign/internal/testenv"
)

// TestRunWithClusterHonorsAnExplicitDSN pins the one case in which no cluster is
// booted: the caller already named a server. That is explicit mode selection,
// not a fallback -- the named DSN reaches the run unchanged, and nothing about
// it is second-guessed.
func TestRunWithClusterHonorsAnExplicitDSN(t *testing.T) {
	testenv.Isolate(t)

	const sentinel = "postgres://someone@example.invalid:6543/theirdb?sslmode=disable"
	t.Setenv(ConnectionEnv, sentinel)

	var observed string
	ran := false
	code := RunWithCluster(func() int {
		ran = true
		observed = os.Getenv(ConnectionEnv)
		return 7
	})

	if !ran {
		t.Fatal("RunWithCluster did not call run")
	}
	if code != 7 {
		t.Errorf("RunWithCluster returned %d; want run's own exit code 7", code)
	}
	if observed != sentinel {
		t.Errorf("run saw %s=%q; want the caller's own value %q", ConnectionEnv, observed, sentinel)
	}
}
