package migrate

import (
	"context"
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// buildReconcileModel builds — directly in the IR, so plain PG types are used and
// no semantic-type domain machinery is involved — a multi-object-kind model that
// round-trips reliably through introspect + DiffLive: two tables, an FK, a
// secondary index, a CHECK (exercising the livenorm round-trip), and — the 1.2
// handoff requirement — a live RLS policy whose USING predicate is round-tripped
// on the introspected side.
func buildReconcileModel(t *testing.T) *model.Schema {
	t.Helper()
	s := &model.Schema{
		Name:      "public",
		PGVersion: 16,
		Tables: []model.Table{
			{
				Name: "users", Schema: "public", PK: []string{"id"}, Comment: "Application users.",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "email", PGType: typeinfo.T("text"), NotNull: true},
				},
			},
			{
				Name: "accounts", Schema: "public", PK: []string{"id"}, Comment: "User accounts with row-level security.",
				EnableRLS: true,
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "user_id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "balance", PGType: typeinfo.T("int8"), NotNull: true},
				},
				FKs: []model.FK{{
					Name: "fk_accounts_user", Columns: []string{"user_id"},
					RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE",
				}},
				Indexes: []model.Index{{Name: "idx_accounts_user", Columns: []string{"user_id"}}},
				Checks:  []model.CheckConstraint{{Name: "ck_balance_nonneg", Expr: "balance >= 0"}},
				Policies: []model.Policy{{
					// Role left empty = PUBLIC (PG stores PUBLIC as the empty role set,
					// which is what introspection reports back).
					Name: "acct_select", Operation: "SELECT", Using: "balance >= 0",
				}},
			},
		},
	}
	s.Canonicalize()
	return s
}

// genesisEdgeFor writes a single genesis edge that creates the whole model.
func genesisEdgeFor(t *testing.T, p *ChainProject, desired *model.Schema) {
	t.Helper()
	base := &model.Schema{Name: desired.Name, PGVersion: desired.PGVersion}
	d := diff.Diff(desired, base)
	m, _ := GenerateMigration(d, desired, "0.1.0", extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, m, desired, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("GenerateEdge: %v", err)
	}
}

// TestReconcileCleanApplyReportsEmpty is 5.8's headline verify item: a clean
// chain apply over a comprehensive-enough model (tables, FK, index, CHECK, RLS
// policy) reconciles EMPTY. Because ApplyChain now runs ReconcileAfterApply
// unconditionally when a dbURL is supplied, a nil return from ApplyChain IS the
// clean-reconcile proof — and it simultaneously proves managed objects are
// invisible (the three pgdesign_* structures exist post-apply, yet reconcile is
// clean because introspection's 0.4 filters exclude them).
func TestReconcileCleanApplyReportsEmpty(t *testing.T) {
	testenv.Isolate(t)
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	desired := buildReconcileModel(t)
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	genesisEdgeFor(t, p, desired)

	applied, err := ApplyChain(ctx, conn, p, ephDB.URL, "5s", nil)
	if err != nil {
		t.Fatalf("apply+reconcile should be clean, got: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("expected 1 applied edge, got %d", len(applied))
	}

	// Managed structures exist, proving reconcile saw them yet stayed clean.
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pgdesign_chain_position").Scan(&n); err != nil {
		t.Fatalf("managed structures should exist: %v", err)
	}
}

// TestReconcileOutOfBandAlterSurfaces: after a clean apply, an out-of-band ALTER
// (a column pgdesign never created) drifts the world off the target model.
// ReconcileAfterApply must surface it as a hard error naming the divergent object.
func TestReconcileOutOfBandAlterSurfaces(t *testing.T) {
	testenv.Isolate(t)
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	desired := buildReconcileModel(t)
	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	genesisEdgeFor(t, p, desired)

	// Apply cleanly (skip the in-apply reconcile so the drift is injected AFTER a
	// known-good landing).
	if _, err := ApplyChain(ctx, conn, p, "", "5s", nil); err != nil {
		t.Fatalf("clean apply: %v", err)
	}

	// Out-of-band drift: a column pgdesign never declared.
	if _, err := conn.Exec(ctx, "ALTER TABLE public.accounts ADD COLUMN injected int NOT NULL DEFAULT 0"); err != nil {
		t.Fatalf("inject drift: %v", err)
	}

	err = ReconcileAfterApply(ctx, ephDB.URL, p)
	if err == nil {
		t.Fatal("reconcile must surface the out-of-band column")
	}
	if !strings.Contains(err.Error(), "injected") || !strings.Contains(err.Error(), "reconcile") {
		t.Fatalf("reconcile error should name the divergent column, got: %v", err)
	}
}
