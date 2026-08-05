package serve

import (
	"context"
	"encoding/json"
	"github.com/smm-h/pgdesign/internal/testenv"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smm-h/pgdesign/internal/diagnostic"
)

// TestRequestTimeout_ObservesCancellation verifies that the server-side --timeout
// enforcement (SetRequestTimeout) both cancels the request context — so a slow
// handler observes cancellation and can stop early — and returns an explicit 503
// to the client.
func TestRequestTimeout_ObservesCancellation(t *testing.T) {
	testenv.Isolate(t)
	s := &Server{requestTimeout: 50 * time.Millisecond}

	observed := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			close(observed) // the handler saw the deadline-driven cancellation
		case <-time.After(5 * time.Second):
			t.Error("handler was not cancelled by the request timeout")
		}
	})

	ts := httptest.NewServer(s.withTimeout(slow))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on timeout, got %d", resp.StatusCode)
	}
	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("slow handler never observed context cancellation")
	}
}

// TestRequestTimeout_DisabledPassesThrough verifies a zero timeout leaves the
// handler unwrapped (no deadline enforced).
func TestRequestTimeout_DisabledPassesThrough(t *testing.T) {
	testenv.Isolate(t)
	s := &Server{} // requestTimeout == 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	if got := s.withTimeout(h); got == nil {
		t.Fatal("withTimeout returned nil")
	}
	// With enforcement disabled the wrapper must be the exact handler (no
	// TimeoutHandler indirection).
	ts := httptest.NewServer(s.withTimeout(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("expected passthrough 418, got %d", resp.StatusCode)
	}
}

// waitFor polls cond until true or the deadline, failing the test on timeout.
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

// TestAuditJobManager_BoundedAndCancellable verifies the audit job manager bounds
// concurrency (rejecting new jobs at capacity) and that cancelling a running job
// frees capacity — the "cancellable; bounded" contract, tested without a database
// via an injected blocking run function.
func TestAuditJobManager_BoundedAndCancellable(t *testing.T) {
	testenv.Isolate(t)
	m := newAuditJobManager()
	m.maxConcurrent = 2

	// A run that blocks until its context is cancelled, then reports cancellation.
	blockUntilCancel := func(ctx context.Context) ([]diagnostic.Diagnostic, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	j1, err := m.start(blockUntilCancel)
	if err != nil {
		t.Fatalf("start job1: %v", err)
	}
	if _, err := m.start(blockUntilCancel); err != nil {
		t.Fatalf("start job2: %v", err)
	}
	// At capacity: the third start is rejected.
	if _, err := m.start(blockUntilCancel); err != errAuditAtCapacity {
		t.Fatalf("expected errAuditAtCapacity for the 3rd job, got %v", err)
	}

	// Cancel job1; it must transition to cancelled and free a slot.
	if !m.cancel(j1.ID) {
		t.Fatal("cancel job1 returned false")
	}
	waitFor(t, 2*time.Second, func() bool {
		snap, ok := m.snapshot(j1.ID)
		return ok && snap.Status == auditCancelled
	})

	// Capacity freed: a new job starts.
	if _, err := m.start(blockUntilCancel); err != nil {
		t.Fatalf("expected capacity after cancel, got %v", err)
	}
}

// TestAuditJobManager_DoneCarriesDiagnostics verifies a completed job records the
// run's diagnostics and terminal status.
func TestAuditJobManager_DoneCarriesDiagnostics(t *testing.T) {
	testenv.Isolate(t)
	m := newAuditJobManager()
	run := func(ctx context.Context) ([]diagnostic.Diagnostic, error) {
		return []diagnostic.Diagnostic{{Severity: diagnostic.Warning, Code: "W999", Message: "example"}}, nil
	}
	j, err := m.start(run)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		snap, ok := m.snapshot(j.ID)
		return ok && snap.Status == auditDone
	})
	snap, _ := m.snapshot(j.ID)
	if len(snap.Diagnostics) != 1 || snap.Diagnostics[0]["code"] != "W999" {
		t.Fatalf("expected the run's diagnostics on the done job, got %+v", snap.Diagnostics)
	}
}

// TestAuditJob_HTTPLifecycle verifies the HTTP job-start/poll flow end-to-end
// against a live database: POST starts a job (202 + id), GET polls it to a
// terminal state carrying diagnostics.
func TestAuditJob_HTTPLifecycle(t *testing.T) {
	testenv.Isolate(t)
	srv := setupServer(t) // skips when no database is configured
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/audit/jobs", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/audit/jobs: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (body: %s)", resp.StatusCode, body)
	}
	var start struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &start); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if start.ID == "" {
		t.Fatal("expected a job id")
	}

	var final struct {
		Status      string              `json:"status"`
		Diagnostics []map[string]string `json:"diagnostics"`
	}
	waitFor(t, 30*time.Second, func() bool {
		r, err := http.Get(ts.URL + "/api/audit/jobs/" + start.ID)
		if err != nil {
			return false
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		_ = json.Unmarshal(b, &final)
		return final.Status == "done" || final.Status == "failed"
	})
	if final.Status != "done" {
		t.Fatalf("expected the audit job to complete (done), got %q", final.Status)
	}
}
