package serve

import (
	"context"
	"github.com/smm-h/pgdesign/internal/testenv"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProjectMode_LiveStatsHardError verifies that requesting live_stats=true on
// a DB-free (project mode) server is a hard error naming the requirement, never
// a silent no-op that drops the annotation.
func TestProjectMode_LiveStatsHardError(t *testing.T) {
	testenv.Isolate(t)
	schema, reg := buildTestProject(t, projectTOML)
	srv := NewProject(schema, reg, nil, []string{"public"}, "")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	for _, path := range []string{"/api/schema/d2?live_stats=true", "/api/schema/svg?live_stats=true"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("GET %s: expected 400 in project mode, got %d", path, resp.StatusCode)
		}
		if !strings.Contains(string(body), "database") {
			t.Fatalf("GET %s: expected error naming the database requirement, got: %s", path, body)
		}
	}

	// live_stats=false is a no-op: the diagram renders normally.
	resp, err := http.Get(ts.URL + "/api/schema/d2?live_stats=false")
	if err != nil {
		t.Fatalf("GET live_stats=false: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("live_stats=false should be a no-op 200, got %d", resp.StatusCode)
	}
}

// TestLiveStats_DBModeParam is the DB-gated serve happy path: with a database,
// live_stats=true fetches pg_stat_user_tables and renders the row-count
// annotation into the diagram for the served (introspected) schema.
func TestLiveStats_DBModeParam(t *testing.T) {
	testenv.Isolate(t)
	srv := setupServer(t) // skips when no database is configured
	ctx := context.Background()

	cleanup := func() { srv.pool.Exec(ctx, "DROP TABLE IF EXISTS ls_widgets") }
	cleanup()
	defer cleanup()

	if _, err := srv.pool.Exec(ctx,
		`CREATE TABLE ls_widgets (id bigint PRIMARY KEY, name text NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := srv.pool.Exec(ctx,
		`INSERT INTO ls_widgets (id, name) VALUES (1,'a'),(2,'b'),(3,'c')`); err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	if _, err := srv.pool.Exec(ctx, `ANALYZE ls_widgets`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/schema/d2?live_stats=true")
	if err != nil {
		t.Fatalf("GET d2?live_stats=true: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "rows: 3") {
		t.Fatalf("expected live row-count annotation (rows: 3) for ls_widgets, got:\n%s", body)
	}

	// Without the opt-in, no live annotation is fetched or rendered.
	resp2, err := http.Get(ts.URL + "/api/schema/d2")
	if err != nil {
		t.Fatalf("GET d2 (no live_stats): %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if strings.Contains(string(body2), "rows: 3") {
		t.Fatalf("live annotation leaked without live_stats opt-in:\n%s", body2)
	}
}
