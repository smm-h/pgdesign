package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/config"
)

// liveStatsSchema declares a widgets table in the public schema so the built
// model's TableKey (public.widgets) matches the pg_stat_user_tables row for the
// seeded table — that identity is what joins the live stats to the diagram.
const liveStatsSchema = `format_version = 1
[meta]
version = 16
schema = "public"

[tables.widgets]
comment = "widgets"
pk = ["id"]

[tables.widgets.columns.id]
type = "id"

[tables.widgets.columns.name]
type = "short_text"
`

const liveStatsConfig = `[project]
schemas = ["schema.toml"]

[database]
pg_version = 16

[output.diagram]
format = "d2"
path = "diagram.d2"

[output.diagram.d2]
live_stats = true
`

// writeLiveStatsProject writes the schema + config into a temp dir and returns
// the pgdesign.toml path (used as the configOverride for runBuild).
func writeLiveStatsProject(t *testing.T) string {
	t.Helper()
	config.CodegenModes = SupportedModes()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.toml"), []byte(liveStatsSchema), 0o644); err != nil {
		t.Fatalf("write schema.toml: %v", err)
	}
	cfgPath := filepath.Join(dir, "pgdesign.toml")
	if err := os.WriteFile(cfgPath, []byte(liveStatsConfig), 0o644); err != nil {
		t.Fatalf("write pgdesign.toml: %v", err)
	}
	return cfgPath
}

// TestBuildLiveStatsPopulatesD2 is the DB-gated happy path: a seeded table's
// live row count is fetched from pg_stat_user_tables and rendered into the d2
// output when the output opts in via live_stats=true.
func TestBuildLiveStatsPopulatesD2(t *testing.T) {
	ephDB := cmdEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `CREATE TABLE widgets (id bigint PRIMARY KEY, name text NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO widgets (id, name) VALUES (1,'a'),(2,'b'),(3,'c')`); err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	// ANALYZE populates n_live_tup; without it pg_stat_user_tables reports 0.
	if _, err := conn.Exec(ctx, `ANALYZE widgets`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	// n_live_tup is an ESTIMATE, and its post-INSERT+ANALYZE value is
	// PostgreSQL-version-dependent (PG < 18 double-counts ANALYZE-after-INSERT,
	// reporting 6 for 3 inserted rows; PG 18+ reports 3). The feature under test
	// is "whatever pg_stat_user_tables reports is what gets rendered", so read the
	// count the build will read and assert the diagram carries exactly that — a
	// deterministic, version-independent check.
	var wantRows int64
	if err := conn.QueryRow(ctx,
		`SELECT n_live_tup FROM pg_stat_user_tables WHERE schemaname = 'public' AND relname = 'widgets'`,
	).Scan(&wantRows); err != nil {
		t.Fatalf("read n_live_tup: %v", err)
	}
	if wantRows < 3 {
		t.Fatalf("expected pg_stat_user_tables to report at least the 3 seeded rows, got %d", wantRows)
	}

	cfgPath := writeLiveStatsProject(t)
	if code := runBuild(&cfgPath, true, false, false, ephDB.URL); code != 0 {
		t.Fatalf("build with live_stats failed: exit %d", code)
	}

	out, err := os.ReadFile(filepath.Join(filepath.Dir(cfgPath), "diagram.d2"))
	if err != nil {
		t.Fatalf("read diagram output: %v", err)
	}
	wantAnnotation := fmt.Sprintf("rows: %d", wantRows)
	if !strings.Contains(string(out), wantAnnotation) {
		t.Fatalf("expected live row-count annotation (%q) in d2 output, got:\n%s", wantAnnotation, out)
	}
}

// TestBuildLiveStatsRequiresDB is the hard-error case: live_stats=true with no
// --db/PGDESIGN_DB must fail loudly (no silent DB dependency, no implicit
// fallback to no stats). It needs no database and always runs.
func TestBuildLiveStatsRequiresDB(t *testing.T) {
	cfgPath := writeLiveStatsProject(t)
	if code := runBuild(&cfgPath, true, false, false, ""); code == 0 {
		t.Fatal("expected build to fail when live_stats=true but no --db/PGDESIGN_DB supplied")
	}
}
