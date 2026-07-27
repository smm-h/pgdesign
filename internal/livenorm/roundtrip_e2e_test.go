package livenorm

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// TestDiffLiveCleanEndToEnd is the "diff --live clean" verify item at expression
// scope: a real table carries a CHECK whose stored form materializes a
// catalog-dependent cast, plus a partial index written in = ANY form. We
// introspect it, then build a desired model IDENTICAL to the introspected one
// except that the expression fields are re-spelled equivalently (the residue
// cast dropped; = ANY rewritten to IN). DiffLive must be CLEAN — every
// difference is an expression-spelling difference N + the round-trip resolve.
// The comprehensive end-to-end round-trip (all object kinds) is 5.8's job; this
// isolates 1.2's expression contract from unrelated introspection gaps.
func TestDiffLiveCleanEndToEnd(t *testing.T) {
	testdb.SkipIfNoPostgres(t)
	ctx := context.Background()
	url := testDBURL()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	defer admin.Close(ctx)

	stmts := []string{
		`DROP TABLE IF EXISTS ln_e2e`,
		`CREATE TABLE ln_e2e (
			id int PRIMARY KEY,
			status text NOT NULL,
			kind int NOT NULL,
			CONSTRAINT ck_status CHECK (status = 'active')
		)`,
		`CREATE INDEX ix_kind ON ln_e2e (id) WHERE kind = ANY(ARRAY[1, 2, 3])`,
	}
	for _, s := range stmts {
		if _, err := admin.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	defer admin.Exec(ctx, `DROP TABLE IF EXISTS ln_e2e`)

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
		if tbl.Name != "ln_e2e" {
			continue
		}
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
	}
	if respelled != 2 {
		t.Fatalf("expected to re-spell 2 expressions, did %d (introspection shape changed?)", respelled)
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
