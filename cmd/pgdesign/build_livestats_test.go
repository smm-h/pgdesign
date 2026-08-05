package main

import (
	"context"
	"github.com/smm-h/pgdesign/internal/testenv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	testenv.Isolate(t)
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

	cfgPath := writeLiveStatsProject(t)
	if code := runBuild(&cfgPath, true, false, false, ephDB.URL); code != 0 {
		t.Fatalf("build with live_stats failed: exit %d", code)
	}

	out, err := os.ReadFile(filepath.Join(filepath.Dir(cfgPath), "diagram.d2"))
	if err != nil {
		t.Fatalf("read diagram output: %v", err)
	}
	// The feature under test is "the live row count is fetched from
	// pg_stat_user_tables and rendered into the d2 output". The exact value is NOT
	// asserted: n_live_tup is an ESTIMATE that PostgreSQL updates asynchronously,
	// and its post-INSERT+ANALYZE value is version-dependent (PG < 18 double-counts
	// ANALYZE-after-INSERT, reporting 6 for 3 rows; PG 18+ reports 3) and can even
	// change between two reads on the same table. So assert a "rows: N" annotation
	// is present with a plausible count (>= the 3 truly-seeded rows).
	m := regexp.MustCompile(`rows: (\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("expected a live row-count annotation (rows: N) in d2 output, got:\n%s", out)
	}
	got, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse row count %q: %v", m[1], err)
	}
	if got < 3 {
		t.Fatalf("expected rendered row count >= 3 seeded rows, got %d in:\n%s", got, out)
	}
}

// TestBuildLiveStatsRequiresDB is the hard-error case: live_stats=true with no
// --db/PGDESIGN_DB must fail loudly (no silent DB dependency, no implicit
// fallback to no stats). It needs no database and always runs.
func TestBuildLiveStatsRequiresDB(t *testing.T) {
	testenv.Isolate(t)
	cfgPath := writeLiveStatsProject(t)
	if code := runBuild(&cfgPath, true, false, false, ""); code == 0 {
		t.Fatal("expected build to fail when live_stats=true but no --db/PGDESIGN_DB supplied")
	}
}
