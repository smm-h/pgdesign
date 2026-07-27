package migrate

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/modelgen"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/testdb"
)

// L10 — THE ROUND-TRIP THEOREM as a DB-backed property test (roadmap 5.8 / L10).
//
// For a generated model pair (a, b): bring a fresh world to revision(a) by
// applying gen(diff(empty, a)), then apply gen(diff(a, b)) and verify the world
// lands at revision(b). The oracles are SPLIT by soundness domain (L10):
//
//   - MANIFEST oracle (not lossy; the UNRESTRICTED domain): after applying the
//     a->b edge, chain_position advanced to revision(b), and the recorded
//     to-revision manifest reconstructs — object-by-object — to b (compared by
//     content id). This certifies landing without introspection, so it extends to
//     SM types once modelgen emits them.
//   - RE-INTROSPECTION oracle (the INJECTIVE, bridge-proven domain only): introspect
//     the applied database and diff it against b, expecting empty. This is exactly
//     ApplyChain's built-in ReconcileAfterApply, which runs because the apply is
//     driven with the live dbURL. It is sound ONLY on the injective fragment — and
//     modelgen emits precisely that fragment today (no SM types), so every pair here
//     is in its domain.
//
// Bounded for CI: a small N by default (-short safe); PGDESIGN_L10_N raises it.

// l10Registry builds the shared type registry the pair models resolve against.
func l10Registry(raws []*parse.RawSchema) *semtype.Registry {
	reg := semtype.NewBuiltinRegistry()
	for _, raw := range raws {
		if uts := parse.CollectUserTypes(raw); len(uts) > 0 {
			reg.LoadUserTypes(uts)
		}
	}
	return reg
}

// l10Build builds a model from generated raws (the modelgen oracle guarantees it
// builds and validates cleanly, so a build error here is a real defect).
func l10Build(t *testing.T, raws []*parse.RawSchema) *model.Schema {
	t.Helper()
	s, diags := model.BuildMulti(raws, l10Registry(raws))
	if diagnostic.Diagnostics(diags).HasErrors() {
		t.Fatalf("l10: build generated model: %v", diags)
	}
	return s
}

// l10WriteGenesis writes the genesis edge that creates model a. Returns false when
// a lowers to no ops (impossible for a non-empty model, but guarded).
func l10WriteGenesis(t *testing.T, p *ChainProject, a *model.Schema) {
	t.Helper()
	base := &model.Schema{Name: a.Name, PGVersion: a.PGVersion}
	d := diff.Diff(a, base)
	m, _ := GenerateMigration(d, a, "0.1.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, m, a, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("l10: genesis edge: %v", err)
	}
}

// l10WriteDelta writes the a->b edge parented at revision(a). Returns false when
// diff(a,b) is empty (b == a — the diff(a,a)=empty corollary; nothing to apply).
func l10WriteDelta(t *testing.T, p *ChainProject, a, b *model.Schema, parentA rev.Revision) bool {
	t.Helper()
	d := diff.Diff(b, a)
	if d.IsEmpty() {
		return false
	}
	m, _ := GenerateMigration(d, b, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if _, err := GenerateEdge(p, m, b, a, parentA, rev.RegistryPresent, "a-to-b"); err != nil {
		if err == ErrNoEdgeOps {
			return false
		}
		t.Fatalf("l10: a->b edge: %v", err)
	}
	return true
}

// l10ManifestOracle asserts the world landed at revision(b): chain_position holds
// revision(b), and the recorded revision(b) manifest reconstructs to b (content-id
// equal). No introspection — this is the not-lossy oracle.
func l10ManifestOracle(t *testing.T, ctx context.Context, conn *pgx.Conn, p *ChainProject, b *model.Schema) {
	t.Helper()
	revB, err := rev.Compute(b, rev.RegistryPresent)
	if err != nil {
		t.Fatalf("l10: compute revision(b): %v", err)
	}
	pos, ok, err := readChainPosition(ctx, conn)
	if err != nil || !ok {
		t.Fatalf("l10: read position: ok=%v err=%v", ok, err)
	}
	if pos.CurrentRevision != revB.String() {
		t.Fatalf("l10 manifest oracle: position %s != revision(b) %s", pos.CurrentRevision, revB)
	}
	// Object-by-object: the recorded manifest reconstructs to a model with the same
	// content id as b (rev.Compute folds every object into the id).
	got, err := ReconstructModel(p, revB)
	if err != nil {
		t.Fatalf("l10: reconstruct revision(b): %v", err)
	}
	gotRev, err := rev.Compute(got, rev.RegistryPresent)
	if err != nil {
		t.Fatalf("l10: compute reconstructed revision: %v", err)
	}
	if gotRev.String() != revB.String() {
		t.Fatalf("l10 manifest oracle: reconstructed revision %s != revision(b) %s", gotRev, revB)
	}
}

// l10EnsureSchemas pre-creates the non-public schemas the model spans. modelgen
// names schemas schema_0.., and chain apply does not itself emit CREATE SCHEMA
// (schema provisioning is outside the edge-apply scope), so the harness creates
// them — the same way a real project would provision its schemas before migrating.
func l10EnsureSchemas(t *testing.T, ctx context.Context, conn *pgx.Conn, a *model.Schema) {
	t.Helper()
	seen := map[string]bool{"": true, "public": true}
	for i := range a.Tables {
		s := a.Tables[i].Schema
		if seen[s] {
			continue
		}
		seen[s] = true
		if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", s)); err != nil {
			t.Fatalf("l10: create schema %q: %v", s, err)
		}
	}
}

// l10Iterations returns the bounded iteration count (small for CI/-short; raised
// via PGDESIGN_L10_N).
func l10Iterations() int {
	n := 12
	if v := os.Getenv("PGDESIGN_L10_N"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	return n
}

// TestL10RoundTrip is the central property: applying gen(diff(a,b)) to a world at
// revision(a) lands it at revision(b), verified by both split oracles.
func TestL10RoundTrip(t *testing.T) {
	mgr := l10Manager(t)
	cfg := l10Config()

	nonTrivial := 0
	for i := 0; i < l10Iterations(); i++ {
		rawsA, rawsB := modelgen.ExamplePair(cfg, i)
		a := l10Build(t, rawsA)
		b := l10Build(t, rawsB)

		i := i
		t.Run(fmt.Sprintf("pair_%d", i), func(t *testing.T) {
			ctx := context.Background()
			ephDB := mgr.SetupForTest(t, testdb.CreateOptions{})
			conn, err := ephDB.Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close(ctx)

			p, err := OpenChainProject(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}

			// Phase 1: world -> revision(a). The built-in reconcile (re-introspection
			// oracle) runs because we pass the live URL, and a is injective.
			l10EnsureSchemas(t, ctx, conn, a)
			l10WriteGenesis(t, p, a)
			if _, err := ApplyChain(ctx, conn, p, ephDB.URL, "5s", nil); err != nil {
				t.Fatalf("l10: apply a (genesis): %v", err)
			}
			revA, err := rev.Compute(a, rev.RegistryPresent)
			if err != nil {
				t.Fatal(err)
			}

			// Phase 2: apply gen(diff(a,b)). Empty diff (b==a) is the trivial
			// corollary — the world is already at revision(b).
			if !l10WriteDelta(t, p, a, b, revA) {
				l10ManifestOracle(t, ctx, conn, p, b) // b == a: already landed
				return
			}
			nonTrivial++
			if _, err := ApplyChain(ctx, conn, p, ephDB.URL, "5s", nil); err != nil {
				t.Fatalf("l10: apply a->b: %v", err)
			}

			// MANIFEST oracle (explicit) + RE-INTROSPECTION oracle (already run inside
			// the ApplyChain above via ReconcileAfterApply — a nil return is its pass).
			l10ManifestOracle(t, ctx, conn, p, b)
		})
	}
}

// TestL10DiffMinimality is the non-normative quality property (bounded sampling):
// deleting any single op from a generated a->b edge must make an oracle FAIL. With
// reconcile built into apply, a mutated edge either errors during apply (a dropped
// prerequisite) or lands a database that fails the re-introspection oracle.
func TestL10DiffMinimality(t *testing.T) {
	mgr := l10Manager(t)
	cfg := l10Config()

	checked := 0
	for i := 0; i < l10Iterations() && checked < 6; i++ {
		rawsA, rawsB := modelgen.ExamplePair(cfg, i)
		a := l10Build(t, rawsA)
		b := l10Build(t, rawsB)
		revA, err := rev.Compute(a, rev.RegistryPresent)
		if err != nil {
			t.Fatal(err)
		}

		// Peek the a->b op count on a scratch project (no DB, no apply).
		scratch, err := OpenChainProject(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		l10WriteGenesis(t, scratch, a)
		if !l10WriteDelta(t, scratch, a, b, revA) {
			continue // no delta to mutate
		}
		nOps := len(l10DeltaEdge(t, scratch).Ops)
		if nOps == 0 {
			continue
		}
		checked++

		// Delete each op in turn (bounded to the first few). Each mutation gets a
		// fresh DB brought to revision(a), then the MUTATED a->b edge applied directly
		// (applyEdge, so the path-finder does not substitute the unmutated on-disk
		// edge). The op payloads live in THIS project's store, so build+apply share it.
		for j := 0; j < nOps && j < 3; j++ {
			j := j
			t.Run(fmt.Sprintf("pair_%d_drop_op_%d", i, j), func(t *testing.T) {
				ctx := context.Background()
				ephDB := mgr.SetupForTest(t, testdb.CreateOptions{})
				conn, err := ephDB.Connect(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close(ctx)

				p, err := OpenChainProject(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				// World -> revision(a): only genesis exists on disk, so ApplyChain lands
				// exactly there.
				l10EnsureSchemas(t, ctx, conn, a)
				l10WriteGenesis(t, p, a)
				if _, err := ApplyChain(ctx, conn, p, "", "5s", nil); err != nil {
					t.Fatalf("bring to revision(a): %v", err)
				}
				// Now write the a->b delta so ChainHead == b (the reconcile target), and
				// load it (payloads seeded in p.store).
				l10WriteDelta(t, p, a, b, revA)
				edge := l10DeltaEdge(t, p)
				if j >= len(edge.Ops) {
					t.Skip("op index beyond edge")
				}

				mutated := l10EdgeWithoutOp(edge, j)
				if applyErr := applyEdge(ctx, conn, p, mutated, &ApplyHooks{}); applyErr != nil {
					return // oracle failed at apply time — a dropped prerequisite
				}
				// Apply succeeded: the re-introspection oracle must catch the missing op.
				if recErr := ReconcileAfterApply(ctx, ephDB.URL, p); recErr == nil {
					t.Fatalf("minimality: dropping op %d of the a->b edge was caught by no oracle", j)
				}
			})
		}
	}
	if checked == 0 {
		t.Skip("no non-trivial deltas generated to mutate")
	}
}

// l10DeltaEdge loads the single non-genesis (a->b) edge from p.
func l10DeltaEdge(t *testing.T, p *ChainProject) Edge {
	t.Helper()
	edges, err := p.LoadLiveEdges()
	if err != nil {
		t.Fatalf("l10: load edges: %v", err)
	}
	for _, e := range edges {
		if !e.IsGenesis() {
			return e
		}
	}
	t.Fatalf("l10: no a->b edge found")
	return Edge{}
}

// l10EdgeWithoutOp returns a copy of e with op index j removed.
func l10EdgeWithoutOp(e Edge, j int) Edge {
	ops := make([]SelfContainedOp, 0, len(e.Ops)-1)
	ops = append(ops, e.Ops[:j]...)
	ops = append(ops, e.Ops[j+1:]...)
	m := e
	m.Ops = ops
	return m
}

// l10Manager builds the ephemeral-DB manager, skipping cleanly without Postgres.
func l10Manager(t *testing.T) *testdb.Manager {
	t.Helper()
	dbURL := os.Getenv("PGDESIGN_DB")
	if dbURL == "" {
		dbURL = "postgres://localhost:5432/pgdesign?sslmode=disable"
	}
	ctx := context.Background()
	probe, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Skipf("no database available: %v", err)
	}
	probe.Close(ctx)
	mgr, err := testdb.NewManager(dbURL)
	if err != nil {
		t.Skipf("no database manager: %v", err)
	}
	return mgr
}

// l10Config is the small generator config the L10 tests draw from: a single
// schema, a handful of tables and columns — big enough for non-trivial diffs,
// small enough to keep the per-iteration DB apply fast.
func l10Config() modelgen.Config {
	return modelgen.Config{
		MinSchemas: 1, MaxSchemas: 1,
		MinTables: 1, MaxTables: 3,
		MinExtraColumns: 0, MaxExtraColumns: 3,
		PGVersion: 16,
		MinGroups: 0, MaxGroups: 0,
		// Restrict to builtin types with NO domain (no CHECK) and an IMMUTABLE or
		// absent default: counter (int8 DEFAULT 0) and flag (bool). This keeps the
		// generated pairs squarely in the injective, round-trippable fragment —
		//   - no domain to create ahead of a delta table (CHECK-bearing scalars omitted);
		//   - no volatile default (timestamp's now() introspects as a fast-default
		//     constant on ADD COLUMN, diverging from the model's now());
		//   - the pair derivation keeps shared types fixed, so column ops are add/drop
		//     only (never a type-change ALTER needing a USING cast).
		ColumnTypes: []string{"counter", "flag"},
	}
}
