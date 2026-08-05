package testdb

import (
	"context"
	"github.com/smm-h/pgdesign/internal/testenv"
	"net/url"
	"testing"
	"time"
)

// TestNewManagerBoundsConnectTimeout pins the fix for the serve/discover/introspect
// TestMain hang: NewManager must inject a connect_timeout into the maintenance URL
// when the caller has not set one, so an unreachable-but-not-refused host fails fast
// instead of hanging the test binary until the go-test timeout.
func TestNewManagerBoundsConnectTimeout(t *testing.T) {
	testenv.Isolate(t)
	mgr, err := NewManager("postgres://localhost:5432/pgdesign?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(mgr.maintenanceURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("connect_timeout"); got == "" {
		t.Fatalf("maintenance URL missing connect_timeout (unbounded dial regresses the TestMain hang): %q", mgr.maintenanceURL)
	}
}

// TestNewManagerPreservesConnectTimeout verifies a caller-provided connect_timeout
// is respected, never overridden by the injected default.
func TestNewManagerPreservesConnectTimeout(t *testing.T) {
	testenv.Isolate(t)
	mgr, err := NewManager("postgres://localhost:5432/pgdesign?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(mgr.maintenanceURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("connect_timeout"); got != "2" {
		t.Fatalf("caller connect_timeout overridden: got %q want 2", got)
	}
}

// TestManagerCreateFailsFastOnUnreachable is the behavioral regression guard: Create
// against an unroutable host (192.0.2.1 is TEST-NET-1, RFC 5737 — guaranteed to hang
// rather than refuse) must return within a bound, proving the INJECTED default dial
// timeout takes effect. The URL deliberately carries no connect_timeout so the test
// exercises the fix rather than a caller-supplied bound. Before the fix an unbounded
// pgx.Connect hung here for the full go-test timeout — exactly how serve's TestMain
// stalled for 10 minutes. No real PostgreSQL is needed; the connect must fail.
func TestManagerCreateFailsFastOnUnreachable(t *testing.T) {
	testenv.Isolate(t)
	mgr, err := NewManager("postgres://192.0.2.1:5432/pgdesign?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, e := mgr.Create(context.Background(), CreateOptions{})
		done <- e
	}()
	// Bound = injected default (defaultConnectTimeoutSeconds) plus margin.
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("expected Create to fail against an unroutable host")
		}
	case <-time.After(time.Duration(defaultConnectTimeoutSeconds+8) * time.Second):
		t.Fatal("Create did not return within the injected timeout: the dial is not bounded (the TestMain hang bug)")
	}
}
