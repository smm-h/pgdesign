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

// commentModel builds a multi-object-kind model (table + column, view, matview,
// sequence, domain, composite, function) with a comment on every object, in a
// NON-public schema so every manifest key equals its raw-schema enc.KeyForX
// (public objects diverge between the diff's bare key and the raw-schema manifest
// key — an orthogonal, pre-existing quirk out of 5.8a's scope). The suffix is
// appended to every comment so a second call yields a comment-only-different B.
func commentModel(suffix string) *model.Schema {
	one := 1.0
	s := &model.Schema{
		Name:      "shop",
		PGVersion: 16,
		Tables: []model.Table{{
			Name: "users", Schema: "shop", PK: []string{"id"},
			Comment: "application users" + suffix,
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
				{Name: "email", PGType: typeinfo.T("text"), NotNull: true, Comment: "the email address" + suffix},
			},
		}},
		Views: []model.View{{
			Name: "v_users", Schema: "shop", Query: "SELECT id FROM shop.users",
			Comment: "user view" + suffix,
		}},
		MaterializedViews: []model.MaterializedView{{
			Name: "mv_users", Schema: "shop", Query: "SELECT count(*) FROM shop.users",
			Comment: "user counts" + suffix,
		}},
		Sequences: []model.Sequence{{
			Name: "order_seq", Schema: "shop", Comment: "order numbers" + suffix,
		}},
		Domains: []model.Domain{{
			Name: "email_dom", Schema: "shop", BaseType: typeinfo.T("text"),
			Check: "VALUE ~ '@'", Comment: "an email domain" + suffix,
		}},
		CompositeTypes: []model.CompositeType{{
			Name: "addr", Schema: "shop",
			Fields:  []model.CompositeField{{Name: "city", PGType: typeinfo.T("text")}},
			Comment: "an address" + suffix,
		}},
		Functions: []model.Function{{
			Name: "f_one", Schema: "shop", Language: "sql", ReturnType: "int4",
			Body: "SELECT 1", Volatility: "IMMUTABLE", Cost: &one,
			Comment: "returns one" + suffix,
		}},
	}
	s.Canonicalize()
	return s
}

// TestCommentOnlyChangeProducesEdgeAndReconcilesInManifest is 5.8a's no-DB
// red-green certification. RED before the fix: a comment-only delta lowered to
// ZERO ops (ErrNoEdgeOps) and comments never reached the manifest simulation, so
// endpoint consistency could not certify them. GREEN now: every diff-reported
// comment change (table/column/view/matview/sequence/domain/composite/function)
// lowers to a comment_on op, the delta edge is non-empty, and the whole chain is
// endpoint-consistent — which proves each comment_on op maps its OWNING object's
// manifest key to the correct post-state id for every object kind.
func TestCommentOnlyChangeProducesEdgeAndReconcilesInManifest(t *testing.T) {
	testenv.Isolate(t)
	a := commentModel("")
	b := commentModel(" (v2)")

	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Genesis edge creates A.
	dGen := diff.Diff(a, &model.Schema{Name: a.Name, PGVersion: a.PGVersion})
	mGen, _ := GenerateMigration(dGen, a, "0.1.0", extregistry.NewBuiltinRegistry())
	revA, err := rev.Compute(a, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateEdge(p, mGen, a, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("genesis GenerateEdge: %v", err)
	}

	// Comment-only delta A -> B.
	dDelta := diff.Diff(b, a)
	if dDelta.IsEmpty() {
		t.Fatal("expected a non-empty comment-only diff")
	}
	mDelta, _ := GenerateMigration(dDelta, b, "0.2.0", extregistry.NewBuiltinRegistry())

	// The comment-only change no longer trips the zero-op guard, and it produced a
	// comment_on op for every object kind whose comment changed.
	nCommentOps := 0
	for _, op := range mDelta.DDLOps {
		if op.Op == "comment_on" {
			nCommentOps++
		}
	}
	if nCommentOps < 8 { // table, column, view, matview, sequence, domain, composite, function
		t.Fatalf("expected >=8 comment_on ops (one per commented object kind), got %d", nCommentOps)
	}

	if _, err := GenerateEdge(p, mDelta, b, a, revA, rev.RegistryPresent, "comment-only"); err != nil {
		if err == ErrNoEdgeOps {
			t.Fatal("comment-only change tripped ErrNoEdgeOps (5.8a regression)")
		}
		t.Fatalf("delta GenerateEdge: %v", err)
	}

	// Endpoint consistency certifies that each comment_on op reproduces revision(B)'s
	// manifest object-by-object from revision(A) — the manifest-side proof that
	// comments now flow through the chain for every object kind.
	if err := VerifyChainConsistency(p); err != nil {
		t.Fatalf("VerifyChainConsistency after comment-only delta: %v", err)
	}
}

// TestReconcileCertifiesCommentChange is 5.8a's DB red-green certification: a
// comment-only TOML change (a table comment AND a column comment) produces a
// non-empty edge, applies, and reconciles CLEAN — proving the emitted COMMENT ON
// statements actually landed in the live database. RED before the fix: the change
// lowered to zero ops, and even a chain-created table carried no comment because
// reconcile's stripComments papered over the hole.
func TestReconcileCertifiesCommentChange(t *testing.T) {
	testenv.Isolate(t)
	ephDB := chainEphemeralDB(t)
	ctx := context.Background()
	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	// A: a table with a table comment and a column comment.
	base := func(tableComment, colComment string) *model.Schema {
		s := &model.Schema{
			Name: "public", PGVersion: 16,
			Tables: []model.Table{{
				Name: "widgets", Schema: "public", PK: []string{"id"}, Comment: tableComment,
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "label", PGType: typeinfo.T("text"), NotNull: true, Comment: colComment},
				},
			}},
		}
		s.Canonicalize()
		return s
	}
	a := base("Widgets table.", "The widget label.")
	b := base("Widgets, revised.", "The widget display label.")

	p, err := OpenChainProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Genesis creates A; ApplyChain runs ReconcileAfterApply (live URL), so a clean
	// return already certifies A's comments landed via create_table folding.
	dGen := diff.Diff(a, &model.Schema{Name: a.Name, PGVersion: a.PGVersion})
	mGen, _ := GenerateMigration(dGen, a, "0.1.0", extregistry.NewBuiltinRegistry())
	revA, err := rev.Compute(a, rev.RegistryPresent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateEdge(p, mGen, a, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("genesis GenerateEdge: %v", err)
	}
	if _, err := ApplyChain(ctx, conn, p, ephDB.URL, "5s", nil); err != nil {
		t.Fatalf("apply A + reconcile (create_table comment folding): %v", err)
	}

	// Comment-only delta A -> B; apply it and reconcile clean (the COMMENT ON ops
	// updated the live comments, so introspect matches target B exactly).
	dDelta := diff.Diff(b, a)
	mDelta, _ := GenerateMigration(dDelta, b, "0.2.0", extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, mDelta, b, a, revA, rev.RegistryPresent, "recomment"); err != nil {
		t.Fatalf("delta GenerateEdge (comment-only): %v", err)
	}
	if _, err := ApplyChain(ctx, conn, p, ephDB.URL, "5s", nil); err != nil {
		t.Fatalf("apply comment-only delta + reconcile: %v", err)
	}

	// Directly confirm the live comments are B's (belt-and-braces over reconcile).
	var tableComment, colComment string
	if err := conn.QueryRow(ctx, "SELECT obj_description('public.widgets'::regclass, 'pg_class')").Scan(&tableComment); err != nil {
		t.Fatalf("read table comment: %v", err)
	}
	if tableComment != "Widgets, revised." {
		t.Errorf("live table comment = %q, want %q", tableComment, "Widgets, revised.")
	}
	if err := conn.QueryRow(ctx,
		"SELECT col_description('public.widgets'::regclass, 2)").Scan(&colComment); err != nil {
		t.Fatalf("read column comment: %v", err)
	}
	if !strings.Contains(colComment, "display label") {
		t.Errorf("live column comment = %q, want it to contain %q", colComment, "display label")
	}
}
