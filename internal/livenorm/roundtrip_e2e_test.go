package livenorm

import (
	"context"
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// TestDiffLiveCleanEndToEnd is 5.8's broadened "diff --live clean" verify item
// (the 1.2 handoff). It spans MULTIPLE object kinds — two tables with a foreign
// key, a partial index in = ANY form, a CHECK whose stored form materializes a
// catalog-dependent cast, and a LIVE RLS POLICY whose USING predicate carries the
// same cast residue — and asserts DiffLive is CLEAN across all of them, so every
// residual difference is an expression-spelling difference N + the round-trip
// resolve, on the introspected side, for every expression-bearing object kind.
//
// HANDOFF NOTE (5.8): the shared comprehensive fixture testdata/schemas/
// comprehensive.toml could NOT be used verbatim here — applying its full DDL and
// re-introspecting reveals several OUT-OF-SCOPE introspect/diff normalization gaps
// (domain type names introspect schema-qualified as `app.short_text` vs the
// desired `short_text`; a policy's default PERMISSIVE type is not normalized;
// `json_schema` is a pgdesign-only column attribute the catalog cannot carry;
// partman child partitions are read as removed rather than excluded; the partman
// maintenance interval normalizes `1 month` -> `1 mon`). These are flagged for a
// dedicated round-trip-hardening pass. To keep the expression contract green and
// object-kind-broad, this test builds the desired model FROM introspection and
// re-spells only the expression fields — so non-expression attributes match by
// construction and the introspect gaps do not intrude.
func TestDiffLiveCleanEndToEnd(t *testing.T) {
	testenv.Isolate(t)
	testdb.SkipIfNoPostgres(t)
	ctx := context.Background()

	// A pristine ephemeral database: introspecting the whole `public` schema must
	// see ONLY this test's objects, so any residual DiffLive difference is an
	// expression-spelling difference, never leftover state from another test.
	mgr, err := testdb.NewManager(testdb.RequireURL(t))
	if err != nil {
		t.Skipf("no database manager: %v", err)
	}
	ephDB := mgr.SetupForTest(t, testdb.CreateOptions{})
	url := ephDB.URL

	admin, err := ephDB.Connect(ctx)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	defer admin.Close(ctx)

	stmts := []string{
		`CREATE TABLE ln_e2e (
			id int PRIMARY KEY,
			status text NOT NULL,
			kind int NOT NULL,
			CONSTRAINT ck_status CHECK (status = 'active')
		)`,
		`CREATE INDEX ix_kind ON ln_e2e (id) WHERE kind = ANY(ARRAY[1, 2, 3])`,
		// A second table with a foreign key back to the first — multi-object breadth.
		`CREATE TABLE ln_e2e_child (
			id int PRIMARY KEY,
			parent_id int NOT NULL REFERENCES ln_e2e (id) ON DELETE CASCADE,
			state text NOT NULL
		)`,
		// A LIVE RLS POLICY whose USING predicate carries a cast residue
		// (state = 'open' -> state = 'open'::text once stored).
		`ALTER TABLE ln_e2e_child ENABLE ROW LEVEL SECURITY`,
		`CREATE POLICY p_open ON ln_e2e_child USING (state = 'open')`,
	}
	for _, s := range stmts {
		if _, err := admin.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	// No manual DROPs — the ephemeral database is torn down by SetupForTest.

	actual, diags, err := introspect.Introspect(ctx, url, []string{"public"})
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		t.Fatalf("introspect diagnostics: %v", diags)
	}

	// Build desired by re-introspecting, then re-spell the expressions into
	// equivalent forms so every non-expression attribute matches exactly.
	desired, _, err := introspect.Introspect(ctx, url, []string{"public"})
	if err != nil {
		t.Fatalf("introspect (desired): %v", err)
	}
	respelled := 0
	for ti := range desired.Tables {
		tbl := &desired.Tables[ti]
		switch tbl.Name {
		case "ln_e2e":
			for ci := range tbl.Checks {
				if tbl.Checks[ci].Name == "ck_status" {
					tbl.Checks[ci].Expr = "status = 'active'" // drop the ::text the DB stored
					respelled++
				}
			}
			for ii := range tbl.Indexes {
				if tbl.Indexes[ii].Where != "" {
					tbl.Indexes[ii].Where = "kind IN (1, 2, 3)" // = ANY -> IN
					respelled++
				}
			}
		case "ln_e2e_child":
			for pi := range tbl.Policies {
				if tbl.Policies[pi].Using != "" {
					tbl.Policies[pi].Using = "state = 'open'" // drop the ::text residue
					respelled++
				}
			}
		}
	}
	if respelled != 3 {
		t.Fatalf("expected to re-spell 3 expressions (CHECK, partial index, RLS policy), did %d (introspection shape changed?)", respelled)
	}

	// Pure diff must see drift (the ::text residue N alone cannot reach).
	if diff.Diff(desired, actual).IsEmpty() {
		t.Fatal("expected pure diff to report the catalog-cast residue as drift")
	}

	// Live diff must be clean.
	ln, err := New(ctx, url)
	if err != nil {
		t.Fatalf("new normalizer: %v", err)
	}
	defer ln.Close()

	if d := diff.DiffLive(desired, actual, ln); !d.IsEmpty() {
		t.Fatalf("diff --live not clean on expression fixture: %s", d.Summary())
	}
}
