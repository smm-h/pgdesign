package introspect

import (
	"context"
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/testdb"
)

// partmanTestBaseURL mirrors the base-URL resolution used by the other
// DB-backed suites: PGDESIGN_DB or a clean skip, never a guessed host.
func partmanTestBaseURL(t *testing.T) string {
	t.Helper()
	return testdb.RequireURL(t)
}

// TestIntrospectPartmanMaintenance verifies that a partman-managed parent
// table's maintenance config (interval/premake/retention) is read back from
// partman.part_config into the model. Requires a PostgreSQL server with
// pg_partman available; skips cleanly otherwise.
func TestIntrospectPartmanMaintenance(t *testing.T) {
	testenv.Isolate(t)
	testdb.SkipIfNoPostgres(t)
	testdb.SkipIfNoPartman(t)

	ctx := context.Background()
	mgr := testdb.RequireManager(t)

	setup := strings.NewReader(`
CREATE SCHEMA IF NOT EXISTS partman;
CREATE EXTENSION IF NOT EXISTS pg_partman SCHEMA partman;
CREATE TABLE public.events (
  id bigint NOT NULL,
  created_at timestamptz NOT NULL
) PARTITION BY RANGE (created_at);
SELECT partman.create_parent(
  p_parent_table := 'public.events',
  p_control := 'created_at',
  p_interval := '1 month',
  p_premake := 4
);
UPDATE partman.part_config
SET retention = '6 months', retention_keep_table = false
WHERE parent_table = 'public.events';
`)

	db := mgr.SetupForTest(t, testdb.CreateOptions{DDL: setup})

	schema, diags, err := Introspect(ctx, db.URL, []string{"public"})
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	for _, d := range diags {
		t.Logf("introspect diagnostic: [%s] %s", d.Code, d.Message)
	}

	var events *struct {
		hasMaint bool
		interval string
		premake  int
		retain   string
	}
	for i := range schema.Tables {
		tb := &schema.Tables[i]
		if tb.Name == "events" {
			events = &struct {
				hasMaint bool
				interval string
				premake  int
				retain   string
			}{}
			if tb.Maintenance != nil {
				events.hasMaint = true
				events.interval = tb.Maintenance.Interval
				events.premake = tb.Maintenance.Premake
				events.retain = tb.Maintenance.Retention
			}
			break
		}
	}
	if events == nil {
		t.Fatal("events parent table not found in introspected schema")
	}
	if !events.hasMaint {
		t.Fatal("expected events.Maintenance to be populated from partman.part_config")
	}
	if events.interval == "" {
		t.Error("expected non-empty maintenance interval from part_config")
	}
	if events.premake != 4 {
		t.Errorf("premake = %d, want 4", events.premake)
	}
	if events.retain == "" {
		t.Error("expected non-empty retention from part_config")
	}
}
