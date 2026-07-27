package migrate

// 5.1b audit riders (roadmap 5.2, item 7): additional shim-render byte-identity
// cases the earlier table left uncovered (create_table / create_partition /
// partman / DML), an end-to-end manifest-simulation case exercising a whole-drop
// AND a rename in one edge, and a VerifyDown tamper case over a nested-modifier
// kind. These close the gaps the 5.1b foundation-round audit surfaced.

import (
	"encoding/json"
	"testing"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/model"
)

// TestShimRendersCreateTablePartitionPartmanDML covers the four families the
// 5.1b render table did not: create_table (with a POST-STATE table def),
// create_partition (child spec + parent), a partman-config op (opaque RawSQL),
// and a DML op (opaque SQL blob). Each must render byte-identically to its
// generate-side oracle after a full trip through the store.
func TestShimRendersCreateTablePartitionPartmanDML(t *testing.T) {
	desired := richModel(t)

	t.Run("create_table", func(t *testing.T) {
		store := newTestStore(t)
		op := DDLOp{Op: "create_table", Table: "app.users", TableDef: &desired.Tables[0], PGVersion: 17,
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_table", Table: "app.users"}}}}
		sc, err := DDLOpToSelfContained(store, op, desired, 0)
		if err != nil {
			t.Fatalf("shim: %v", err)
		}
		data, _ := MarshalOp(sc)
		parsed, err := UnmarshalOp(store, data)
		if err != nil {
			t.Fatalf("UnmarshalOp: %v", err)
		}
		up, err := parsed.RenderSQL(store)
		if err != nil {
			t.Fatalf("RenderSQL: %v", err)
		}
		assertEq(t, up, OpToSQL(op))
	})

	t.Run("create_partition", func(t *testing.T) {
		store := newTestStore(t)
		spec := &model.PartitionSpec{
			Strategy: "range",
			Name:     "events_2024",
			Bound:    "FROM ('2024-01-01') TO ('2025-01-01')",
		}
		op := DDLOp{Op: "create_partition", Table: "app.events", ParentTable: "app.events", PartitionChildSpec: spec,
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_table", Table: "app.events_2024"}}}}
		sc, err := DDLOpToSelfContained(store, op, desired, 0)
		if err != nil {
			t.Fatalf("shim: %v", err)
		}
		data, _ := MarshalOp(sc)
		parsed, err := UnmarshalOp(store, data)
		if err != nil {
			t.Fatalf("UnmarshalOp: %v", err)
		}
		up, err := parsed.RenderSQL(store)
		if err != nil {
			t.Fatalf("RenderSQL: %v", err)
		}
		assertEq(t, up, OpToSQL(op))
	})

	t.Run("partman", func(t *testing.T) {
		store := newTestStore(t)
		raw := "SELECT partman.create_parent('app.events', 'created_at', '1 month');"
		op := DDLOp{Op: "create_partman_parent", Table: "app.events", RawSQL: raw,
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_partman", Table: "app.events", RawSQL: "SELECT partman.undo_partition('app.events');"}}}}
		sc, err := DDLOpToSelfContained(store, op, desired, 0)
		if err != nil {
			t.Fatalf("shim: %v", err)
		}
		data, _ := MarshalOp(sc)
		parsed, err := UnmarshalOp(store, data)
		if err != nil {
			t.Fatalf("UnmarshalOp: %v", err)
		}
		up, err := parsed.RenderSQL(store)
		if err != nil {
			t.Fatalf("RenderSQL: %v", err)
		}
		assertEq(t, up, OpToSQL(op)) // == raw
		assertEq(t, up, raw)
	})

	t.Run("dml", func(t *testing.T) {
		// DML ops (backfill/transform) are opaque SQL blobs; generate-to-edge
		// routes them through BuildDMLOp (the shim converts DDLOps only). The blob
		// echoes the input SQL byte-for-byte.
		store := newTestStore(t)
		upSQL := "UPDATE app.users SET email = lower(email) WHERE email <> lower(email);"
		sc, err := BuildDMLOp(store, "backfill", 0, upSQL, 1, "-- vacuous: data not restored")
		if err != nil {
			t.Fatalf("BuildDMLOp: %v", err)
		}
		if sc.Target().String() != "dml:0" {
			t.Errorf("dml target = %q, want dml:0", sc.Target().String())
		}
		data, _ := MarshalOp(sc)
		parsed, err := UnmarshalOp(store, data)
		if err != nil {
			t.Fatalf("UnmarshalOp: %v", err)
		}
		up, err := parsed.RenderSQL(store)
		if err != nil {
			t.Fatalf("RenderSQL: %v", err)
		}
		assertEq(t, up, upSQL)
	})
}

// TestShimEndToEndDropAndRename is the manifest-simulation rider: one edge that
// DROPS a whole table AND RENAMES another. The simulator must carry
// from-manifest(A) EXACTLY to to-manifest(B) with both a catWholeDrop and a
// catRenameTable op in play.
func TestShimEndToEndDropAndRename(t *testing.T) {
	store := newTestStore(t)

	modelA := &model.Schema{
		Name:      "app",
		PGVersion: 17,
		Tables: []model.Table{
			{Name: "orders", Schema: "app", Comment: "orders",
				Columns: []model.Column{{Name: "id", PGType: mustParse("bigint"), NotNull: true}}, PK: []string{"id"}},
			{Name: "users", Schema: "app", Comment: "users",
				Columns: []model.Column{{Name: "id", PGType: mustParse("bigint"), NotNull: true}}, PK: []string{"id"}},
		},
	}
	modelA.Canonicalize()

	modelB := &model.Schema{
		Name:      "app",
		PGVersion: 17,
		Tables: []model.Table{
			// orders dropped; users renamed to members.
			{Name: "members", Schema: "app", Comment: "users",
				Columns: []model.Column{{Name: "id", PGType: mustParse("bigint"), NotNull: true}}, PK: []string{"id"}},
		},
	}
	modelB.Canonicalize()

	fromManifest, err := chain.BuildManifest(modelA)
	if err != nil {
		t.Fatalf("BuildManifest(A): %v", err)
	}
	toManifest, err := chain.BuildManifest(modelB)
	if err != nil {
		t.Fatalf("BuildManifest(B): %v", err)
	}

	dropOrders := DDLOp{Op: "drop_table", Table: "app.orders", Down: &DownOp{Irreversible: true}}
	renameUsers := DDLOp{Op: "rename_table", Table: "app.users", Name: "members"}

	scDrop, err := DDLOpToSelfContained(store, dropOrders, modelB, 0)
	if err != nil {
		t.Fatalf("shim drop_table: %v", err)
	}
	scRename, err := DDLOpToSelfContained(store, renameUsers, modelB, 1)
	if err != nil {
		t.Fatalf("shim rename_table: %v", err)
	}

	sim := opSimulator{store: store}
	got, err := sim.Simulate(fromManifest, []chain.Op{scDrop, scRename})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if !got.Equal(toManifest) {
		d := toManifest.Diff(got)
		t.Fatalf("simulated manifest != to-manifest (added=%v removed=%v changed=%v)", d.Added, d.Removed, d.Changed)
	}
}

// TestShimVerifyDownTamperNestedModifier proves the LOAD-time down-cache verifier
// (edge_format.md TENSION 1) catches a tampered down over a nested-modifier op:
// add_column's recorded inverse (drop_column) is relabeled to a bogus kind, and
// VerifyDown re-derives the true down from the up payload and rejects the mismatch.
func TestShimVerifyDownTamperNestedModifier(t *testing.T) {
	store := newTestStore(t)
	desired := richModel(t)
	op := DDLOp{Op: "add_column", Table: "app.users", Column: "nick", Type: "text", NotNull: true,
		Down: &DownOp{Ops: []DDLOp{{Op: "drop_column", Table: "app.users", Column: "nick"}}}}
	sc, err := DDLOpToSelfContained(store, op, desired, 0)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	if sc.Invertibility() != chain.DeclaredInverse {
		t.Fatalf("add_column invertibility = %v, want declared-inverse", sc.Invertibility())
	}

	// Serialize, relabel the down kind (payload id untouched), re-parse.
	data, err := MarshalOp(sc)
	if err != nil {
		t.Fatalf("MarshalOp: %v", err)
	}
	var j OpJSON
	if err := json.Unmarshal(data, &j); err != nil {
		t.Fatalf("unmarshal OpJSON: %v", err)
	}
	if j.Down == nil {
		t.Fatal("expected a down cache to tamper")
	}
	j.Down.Kind = "drop_view" // bogus: the true derivation is drop_column
	tampered, err := canonicalOpJSON(j)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	parsed, err := UnmarshalOp(store, tampered)
	if err != nil {
		t.Fatalf("UnmarshalOp: %v", err)
	}
	if err := VerifyDown(store, parsed); err == nil {
		t.Fatal("expected VerifyDown to reject the tampered down cache, got nil")
	}
}
