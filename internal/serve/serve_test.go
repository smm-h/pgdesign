package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/testdb"
	"github.com/smm-h/pgdesign/internal/testenv"
	"github.com/smm-h/pgdesign/internal/workload"
)

var (
	testMgr *testdb.Manager
	testDB  *testdb.EphemeralDB
)

// requireTestDB ends t when this binary has no ephemeral database.
//
// That state has exactly one cause: no DSN was configured at all. runTests
// fails the whole binary for every OTHER setup outcome, so an absent database
// is the only thing left to decide here -- and the verdict is the same one
// every database-backed test in this repository gets: skip, or fail under
// PGDESIGN_REQUIRE_DB=1.
func requireTestDB(t *testing.T) {
	t.Helper()
	if testDB != nil {
		return
	}
	testdb.RequireURL(t) // skips, or fails under the require gate
	t.Fatal("this test binary has no ephemeral database even though a DSN is configured")
}

// setupServer creates a Server backed by a real pgxpool for integration tests.
// The DB-free project-mode tests in this package run regardless; only these
// need a database.
func setupServer(t *testing.T) *Server {
	t.Helper()
	requireTestDB(t)
	ctx := context.Background()
	pool, err := testDB.Pool(ctx)
	if err != nil {
		t.Fatalf("create pool from ephemeral DB: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return NewFromPool(pool, []string{"public"}, "")
}

// TestMain boots one ephemeral PostgreSQL cluster for this test binary and
// exports its base URL under PGDESIGN_DB. There is no fallback: on a machine
// without the PostgreSQL binaries the variable stays unset, the DB-backed tests
// skip themselves through requireTestDB, and the DB-free project-mode tests
// still run.
func TestMain(m *testing.M) {
	os.Exit(testdb.RunWithCluster(func() int { return runTests(m) }))
}

// runTests sets this package's ephemeral database up around m.Run. It returns
// the exit code instead of calling os.Exit so that RunWithCluster always gets
// to stop the cluster it started.
//
// Setup failures are FAILURES. This used to be a best-effort block that
// swallowed every error and left testDB nil, so a database that was named but
// unusable produced a run where all ten database-backed serve tests skipped and
// the package still reported ok -- green CI with zero serve database coverage,
// even under PGDESIGN_REQUIRE_DB=1. An absent DSN is the only outcome that may
// end in a skip, and that verdict is testdb.MainManager's to give.
func runTests(m *testing.M) int {
	ctx := context.Background()

	mgr, code, ok := testdb.MainManager()
	if !ok {
		if code != 0 {
			return code
		}
		// No database named at all: the DB-free project-mode tests still run.
		return m.Run()
	}
	testMgr = mgr

	db, err := testMgr.Create(ctx, testdb.CreateOptions{})
	if err != nil {
		return testdb.MainFailed(fmt.Errorf("creating the ephemeral database: %w", err))
	}
	testDB = db

	code = m.Run()

	_ = testMgr.Drop(ctx, testDB)
	return code
}

func TestGetExtensions(t *testing.T) {
	testenv.Isolate(t)
	srv := setupServer(t)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/extensions")
	if err != nil {
		t.Fatalf("GET /api/extensions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var extensions []map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&extensions); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// extensions is a JSON array (may be empty, that's fine)
}

func TestGetStats(t *testing.T) {
	testenv.Isolate(t)
	srv := setupServer(t)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/stats")
	if err != nil {
		t.Fatalf("GET /api/stats: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := result["tables"]; !ok {
		t.Fatal("expected 'tables' key in response")
	}
}

func TestGetSchema(t *testing.T) {
	testenv.Isolate(t)
	srv := setupServer(t)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/schema")
	if err != nil {
		t.Fatalf("GET /api/schema: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// The /schema response is now the canonical whole-model envelope
	// {format_version, revision, model, diagnostics?} (roadmap 1.5), not the old
	// {schema, diagnostics} shape. rev.Parse verifies the embedded model bytes
	// hash to the revision; the introspect path is registry-absent (L7).
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	env, err := rev.Parse(body)
	if err != nil {
		t.Fatalf("envelope Parse failed: %v\nbody: %s", err, body)
	}
	if env.Revision.Class() != rev.RegistryAbsent {
		t.Errorf("expected registry-absent class on the introspect path, got %s", env.Revision.Class())
	}
	if len(env.Model) == 0 {
		t.Fatal("expected non-empty embedded model bytes")
	}
}

func TestPostValidateValid(t *testing.T) {
	testenv.Isolate(t)
	srv := setupServer(t)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	toml := `
format_version = 1
[meta]
version = 1

[tables.users]
comment = "User accounts"
pk = ["id"]

[tables.users.columns.id]
type = "id"

[tables.users.columns.name]
type = "short_text"
`

	resp, err := http.Post(ts.URL+"/api/validate", "application/toml", strings.NewReader(toml))
	if err != nil {
		t.Fatalf("POST /api/validate: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if valid, ok := result["valid"].(bool); !ok || !valid {
		t.Fatalf("expected valid=true, got %v", result["valid"])
	}
}

func TestPostValidateInvalid(t *testing.T) {
	testenv.Isolate(t)
	srv := setupServer(t)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Invalid TOML: missing type on column.
	toml := `
format_version = 1
[meta]
version = 1

[tables.users]
pk = ["id"]

[tables.users.columns.id]
`

	resp, err := http.Post(ts.URL+"/api/validate", "application/toml", strings.NewReader(toml))
	if err != nil {
		t.Fatalf("POST /api/validate: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if valid, ok := result["valid"].(bool); !ok || valid {
		t.Fatalf("expected valid=false, got %v", result["valid"])
	}
	diags, ok := result["diagnostics"].([]any)
	if !ok || len(diags) == 0 {
		t.Fatal("expected non-empty diagnostics for invalid schema")
	}
}

func TestPoolConfigApplied(t *testing.T) {
	testenv.Isolate(t)
	requireTestDB(t)
	// Verify that PoolConfig values are applied to pgxpool.Config when non-zero,
	// and pgxpool defaults are preserved when zero.
	connStr := testDB.URL
	poolCfg := PoolConfig{MaxConns: 20, MinConns: 3}
	pgxCfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	defaultMax := pgxCfg.MaxConns
	defaultMin := pgxCfg.MinConns

	// Apply non-zero values.
	if poolCfg.MaxConns > 0 {
		pgxCfg.MaxConns = poolCfg.MaxConns
	}
	if poolCfg.MinConns > 0 {
		pgxCfg.MinConns = poolCfg.MinConns
	}
	if pgxCfg.MaxConns != 20 {
		t.Errorf("MaxConns = %d, want 20", pgxCfg.MaxConns)
	}
	if pgxCfg.MinConns != 3 {
		t.Errorf("MinConns = %d, want 3", pgxCfg.MinConns)
	}

	// Verify zero values preserve defaults.
	zeroCfg := PoolConfig{}
	pgxCfg2, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if zeroCfg.MaxConns > 0 {
		pgxCfg2.MaxConns = zeroCfg.MaxConns
	}
	if zeroCfg.MinConns > 0 {
		pgxCfg2.MinConns = zeroCfg.MinConns
	}
	if pgxCfg2.MaxConns != defaultMax {
		t.Errorf("MaxConns with zero config = %d, want default %d", pgxCfg2.MaxConns, defaultMax)
	}
	if pgxCfg2.MinConns != defaultMin {
		t.Errorf("MinConns with zero config = %d, want default %d", pgxCfg2.MinConns, defaultMin)
	}
}

func TestFindDuplicateIndexes(t *testing.T) {
	testenv.Isolate(t)
	tests := []struct {
		name    string
		indexes []workload.IndexInfo
		want    int
	}{
		{
			name:    "no indexes",
			indexes: nil,
			want:    0,
		},
		{
			name: "no duplicates",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "t", Name: "idx_a", Columns: []string{"x", "y"}},
				{Schema: "public", Table: "t", Name: "idx_b", Columns: []string{"z"}},
			},
			want: 0,
		},
		{
			name: "prefix duplicate",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "t", Name: "idx_a", Columns: []string{"x"}},
				{Schema: "public", Table: "t", Name: "idx_b", Columns: []string{"x", "y"}},
			},
			want: 1,
		},
		{
			name: "exact same columns not duplicate",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "t", Name: "idx_a", Columns: []string{"x", "y"}},
				{Schema: "public", Table: "t", Name: "idx_b", Columns: []string{"x", "y"}},
			},
			want: 0,
		},
		{
			name: "multiple duplicates",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "t", Name: "idx_a", Columns: []string{"x"}},
				{Schema: "public", Table: "t", Name: "idx_b", Columns: []string{"x", "y"}},
				{Schema: "public", Table: "t", Name: "idx_c", Columns: []string{"x", "y", "z"}},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workload.FindDuplicateIndexes(tt.indexes)
			if len(got) != tt.want {
				t.Errorf("FindDuplicateIndexes() returned %d pairs, want %d", len(got), tt.want)
			}
		})
	}
}
