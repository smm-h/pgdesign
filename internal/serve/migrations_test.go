package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// writeGenesisEdgeForServe writes a single valid genesis edge (slug "genesis")
// into the chain project, so the serve version endpoint has a real content-
// addressed artifact to return.
func writeGenesisEdgeForServe(t *testing.T, p *migrate.ChainProject) error {
	t.Helper()
	desired := &model.Schema{
		Name: "public", PGVersion: 16,
		Tables: []model.Table{{
			Name: "t", Schema: "public", PK: []string{"id"}, Comment: "t",
			Columns: []model.Column{{Name: "id", PGType: typeinfo.T("bigint"), NotNull: true}},
		}},
	}
	desired.Canonicalize()
	d := diff.Diff(desired, &model.Schema{Name: "public", PGVersion: 16})
	m, _ := migrate.GenerateMigration(d, desired, "", extregistry.NewBuiltinRegistry())
	_, err := migrate.GenerateEdge(p, m, desired, nil, rev.Revision{}, rev.RegistryPresent, "genesis")
	return err
}

// TestGetMigrationsViewPrecedence verifies serve's read-only precedence: the
// legacy pgdesign_migrations table is served when only it exists, and the
// chain-era pgdesign_applied_migrations view TAKES PRECEDENCE once present.
func TestGetMigrationsViewPrecedence(t *testing.T) {
	srv := setupServer(t)
	ctx := context.Background()

	// Clean slate for the tracking structures on the shared ephemeral DB.
	cleanup := func() {
		srv.pool.Exec(ctx, "DROP VIEW IF EXISTS pgdesign_applied_migrations")
		srv.pool.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_migration_ops")
		srv.pool.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_chain_position")
		srv.pool.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_migrations")
	}
	cleanup()
	defer cleanup()

	ts := httptest.NewServer(srv)
	defer ts.Close()

	get := func() []map[string]any {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/migrations")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var out []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// Legacy table only: its row is served.
	if _, err := srv.pool.Exec(ctx,
		`CREATE TABLE pgdesign_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now(), checksum text NOT NULL, description text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.pool.Exec(ctx,
		`INSERT INTO pgdesign_migrations (version, applied_at, checksum, description) VALUES ('0.1.0', now(), 'abc', 'legacy row')`); err != nil {
		t.Fatal(err)
	}
	rows := get()
	if len(rows) != 1 || rows[0]["version"] != "0.1.0" || rows[0]["description"] != "legacy row" {
		t.Fatalf("legacy precedence: expected the legacy row, got %v", rows)
	}

	// Add the chain structures + a confirmed op: the VIEW now takes precedence.
	tx, err := srv.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Roll back on any early t.Fatal between here and Commit — otherwise the tx's
	// connection stays checked out and the deferred pool.Close() (in setupServer)
	// blocks forever, turning a plain test failure into a whole-package hang.
	// After a successful Commit this Rollback is a no-op (ErrTxClosed).
	defer tx.Rollback(ctx)
	if err := migrate.CreateTrackingStructures(ctx, tx); err != nil {
		t.Fatal(err)
	}
	// down_op must be present for invertible ops (pgdesign_migration_ops_down_presence:
	// (invertibility = 'non-invertible') = (down_op IS NULL)).
	if _, err := tx.Exec(ctx,
		`INSERT INTO pgdesign_migration_ops (edge_id, seq, phase, op_kind, target, invertibility, down_op, status, confirmed_at, version_label, description, checksum)
		 VALUES ('e1', 0, '', 'create_table', 'table:t', 'mechanically-invertible', '{"kind":"drop_table","target":"table:t","invertibility":"mechanically-invertible","payload_id":null}'::jsonb, 'confirmed', now(), 'chain-edge', 'from the view', 'xyz')`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rows = get()
	if len(rows) != 1 || rows[0]["version"] != "chain-edge" || rows[0]["description"] != "from the view" {
		t.Fatalf("view precedence: expected the view row, got %v", rows)
	}
}

// TestGetMigrationVersionChainEdge verifies the version endpoint serves the raw
// chain edge artifact by its content-hash prefix for a chain-mode project.
func TestGetMigrationVersionChainEdge(t *testing.T) {
	if testDB == nil {
		t.Skip("no database configured (set PGDESIGN_DB); skipping database-backed test")
	}
	ctx := context.Background()
	pool, err := testDB.Pool(ctx)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	// A chain-mode project with a single genesis edge on disk.
	dir := t.TempDir()
	p, err := migrate.OpenChainProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeGenesisEdgeForServe(t, p); err != nil {
		t.Fatal(err)
	}
	edges, err := p.LoadLiveEdges()
	if err != nil || len(edges) != 1 {
		t.Fatalf("expected one live edge, got %d (err=%v)", len(edges), err)
	}
	prefix := edges[0].ID()[:12]

	srv := NewFromPool(pool, []string{"public"}, dir)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/migrations/" + prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for edge %s, got %d", prefix, resp.StatusCode)
	}
	var edge map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&edge); err != nil {
		t.Fatalf("decode edge JSON: %v", err)
	}
	if edge["slug"] != "genesis" {
		t.Errorf("expected the served edge to be the genesis edge, got slug=%v", edge["slug"])
	}

	// A bogus reference 404s; a traversal attempt is rejected.
	if r, _ := http.Get(ts.URL + "/api/migrations/doesnotexist"); r != nil && r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown edge should 404, got %d", r.StatusCode)
	}

}
