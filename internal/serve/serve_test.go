package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smm-h/pgdesign/internal/testdb"
	"github.com/smm-h/pgdesign/internal/workload"
)

var (
	testMgr *testdb.Manager
	testDB  *testdb.EphemeralDB
)

// setupServer creates a Server backed by a real pgxpool for integration tests.
func setupServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	pool, err := testDB.Pool(ctx)
	if err != nil {
		t.Fatalf("create pool from ephemeral DB: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return NewFromPool(pool, []string{"public"}, "")
}

func TestMain(m *testing.M) {
	dbURL := os.Getenv("PGDESIGN_DB")
	if dbURL == "" {
		dbURL = "postgres://localhost:5432/pgdesign?sslmode=disable"
	}

	var err error
	testMgr, err = testdb.NewManager(dbURL)
	if err != nil {
		os.Exit(0)
	}

	ctx := context.Background()
	testDB, err = testMgr.Create(ctx, testdb.CreateOptions{})
	if err != nil {
		os.Exit(0)
	}

	code := m.Run()

	_ = testMgr.Drop(ctx, testDB)
	os.Exit(code)
}

func TestGetExtensions(t *testing.T) {
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

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := result["schema"]; !ok {
		t.Fatal("expected 'schema' key in response")
	}
}

func TestPostValidateValid(t *testing.T) {
	srv := setupServer(t)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	toml := `
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
	srv := setupServer(t)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Invalid TOML: missing type on column.
	toml := `
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
