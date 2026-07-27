package migrate

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
	pgsql "github.com/smm-h/pgdesign/internal/sql"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// newTestStore opens a fresh object store in a temp dir at the current codec.
func newTestStore(t *testing.T) *objstore.Store {
	t.Helper()
	store, err := objstore.New(t.TempDir(), enc.CodecVersion)
	if err != nil {
		t.Fatalf("objstore.New: %v", err)
	}
	return store
}

// roundTrip serializes op, re-parses it against the store (which drops all
// in-memory def state and keeps only content ids), verifies the down cache, and
// renders both directions. It is the core self-containment check: everything
// must survive a trip through the store by content id alone.
func roundTrip(t *testing.T, store *objstore.Store, op SelfContainedOp) (up, down string) {
	t.Helper()
	data, err := MarshalOp(op)
	if err != nil {
		t.Fatalf("MarshalOp: %v", err)
	}
	parsed, err := UnmarshalOp(store, data)
	if err != nil {
		t.Fatalf("UnmarshalOp: %v", err)
	}
	// The re-parsed op carries only content ids — no def pointers, no RawSQL.
	if err := VerifyDown(store, parsed); err != nil {
		t.Fatalf("VerifyDown: %v", err)
	}
	up, err = parsed.RenderSQL(store)
	if err != nil {
		t.Fatalf("RenderSQL(up): %v", err)
	}
	inv, ok := parsed.Inverse()
	if !ok {
		t.Fatalf("parsed op %q reports no inverse", parsed.Kind())
	}
	down, err = inv.(SelfContainedOp).RenderSQL(store)
	if err != nil {
		t.Fatalf("RenderSQL(down): %v", err)
	}
	return up, down
}

func mustParse(s string) typeinfo.Type { return typeinfo.Parse(s) }

// mixedTable is the roadmap-mandated fixture: a table with an ENUM column, a
// DOMAIN column, and a VERSION-GATED generated column, in a non-public schema so
// the enum/domain type closure demonstrably changes the rendered SQL.
func mixedTable() (tbl model.Table, enums []model.Enum, domains []model.Domain, schema string, pgVersion int) {
	schema = "app"
	pgVersion = 18 // gates the generated column to VIRTUAL (STORED pre-18)
	enums = []model.Enum{{Schema: "app", Name: "status_enum", Values: []string{"active", "inactive"}}}
	domains = []model.Domain{{Schema: "app", Name: "email_addr", BaseType: mustParse("text"), Check: "VALUE ~ '@'"}}
	tbl = model.Table{
		Name:    "users",
		Schema:  "app",
		Comment: "app users",
		Columns: []model.Column{
			{Name: "id", PGType: mustParse("bigint"), NotNull: true},
			{Name: "status", PGType: mustParse("status_enum"), NotNull: true},
			{Name: "email", PGType: mustParse("email_addr"), NotNull: true},
			// Version-gated generated column: stored=false -> VIRTUAL on PG18+,
			// STORED before. Rendering must use the op's recorded PGVersion (18),
			// never a hardcoded 0 (which would emit STORED).
			{Name: "id_text", PGType: mustParse("text"), Generated: "id::text", Stored: false},
		},
		PK: []string{"id"},
	}
	return
}

// TestSelfContainedCreateTableMixed pins the roadmap's mixed fixture: the enum
// column, the domain column, and the version-gated generated column all render
// byte-identically after a store round-trip, and PGVersion is honored (VIRTUAL,
// not STORED).
func TestSelfContainedCreateTableMixed(t *testing.T) {
	store := newTestStore(t)
	tbl, enums, domains, schema, pgVersion := mixedTable()

	wantUp := pgsql.CreateTable(&tbl, schema, false, pgVersion, enums, domains)
	wantDown := "DROP TABLE " + pgsql.QualifiedName(schema, tbl.Name) + ";"

	op, err := BuildCreateTable(store, tbl, schema, pgVersion, enums, domains)
	if err != nil {
		t.Fatalf("BuildCreateTable: %v", err)
	}
	if op.Invertibility() != chain.MechanicallyInvertible {
		t.Errorf("create_table invertibility = %v, want mechanically-invertible", op.Invertibility())
	}
	up, down := roundTrip(t, store, op)
	if up != wantUp {
		t.Errorf("create_table up render mismatch:\n got: %q\nwant: %q", up, wantUp)
	}
	if down != wantDown {
		t.Errorf("create_table down render mismatch:\n got: %q\nwant: %q", down, wantDown)
	}
	// Guard: the recorded PGVersion is actually consumed — a VIRTUAL keyword
	// only appears when pgVersion >= 18. A regression to hardcoded 0 would emit
	// STORED and this substring check would fail.
	if !containsWord(up, "VIRTUAL") {
		t.Errorf("expected VIRTUAL generated column (PGVersion honored), got:\n%s", up)
	}
}

func containsWord(s, w string) bool {
	for i := 0; i+len(w) <= len(s); i++ {
		if s[i:i+len(w)] == w {
			return true
		}
	}
	return false
}

// TestSelfContainedFamilies is the table-driven round-trip over the remaining
// self-contained op families. Each case builds an op, round-trips it through the
// store, and asserts the up and down renders equal generate's SQL (the sql.*
// helpers) byte-for-byte.
func TestSelfContainedFamilies(t *testing.T) {
	store := newTestStore(t)
	schema := "app"

	// --- view ---
	view := model.View{Name: "active_users", Schema: schema, Query: "SELECT * FROM app.users WHERE status = 'active'", Comment: "active users"}
	t.Run("create_view", func(t *testing.T) {
		op, err := BuildCreateView(store, view, schema)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreateView(schema, &view, false))
		assertEq(t, down, pgsql.DropView(schema, view.Name, false))
	})

	// --- create_or_replace_view (declared inverse restores prev) ---
	prevView := model.View{Name: "active_users", Schema: schema, Query: "SELECT id FROM app.users", Comment: "old"}
	t.Run("create_or_replace_view", func(t *testing.T) {
		op, err := BuildCreateOrReplaceView(store, view, &prevView, schema)
		if err != nil {
			t.Fatal(err)
		}
		if op.Invertibility() != chain.DeclaredInverse {
			t.Errorf("invertibility = %v, want declared-inverse", op.Invertibility())
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreateView(schema, &view, true))
		assertEq(t, down, pgsql.CreateView(schema, &prevView, true))
	})

	// --- materialized view ---
	mv := model.MaterializedView{Name: "user_counts", Schema: schema, Query: "SELECT count(*) FROM app.users", Comment: "counts"}
	t.Run("create_materialized_view", func(t *testing.T) {
		op, err := BuildCreateMaterializedView(store, mv, schema)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreateMaterializedView(schema, &mv, false))
		assertEq(t, down, pgsql.DropMaterializedView(schema, mv.Name, false))
	})

	// --- sequence (parameters must survive) ---
	start := int64(100)
	inc := int64(5)
	maxv := int64(9999)
	seq := model.Sequence{Name: "order_seq", Schema: schema, Start: &start, Increment: &inc, MaxValue: &maxv, Cycle: true, Comment: "orders"}
	t.Run("create_sequence", func(t *testing.T) {
		op, err := BuildCreateSequence(store, seq, schema)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreateSequence(schema, &seq, false))
		assertEq(t, down, pgsql.DropSequence(schema, seq.Name, false))
		// Parameters preserved: START/INCREMENT/MAXVALUE/CYCLE all appear.
		for _, want := range []string{"100", "5", "9999", "CYCLE"} {
			if !containsWord(up, want) {
				t.Errorf("sequence parameter %q lost on round-trip:\n%s", want, up)
			}
		}
	})

	// --- alter_sequence (declared inverse restores prev params) ---
	prevSeq := model.Sequence{Name: "order_seq", Schema: schema, Start: &start, Increment: &inc, Comment: "orders"}
	t.Run("alter_sequence", func(t *testing.T) {
		op, err := BuildAlterSequence(store, seq, prevSeq, schema)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.AlterSequence(schema, &seq))
		assertEq(t, down, pgsql.AlterSequence(schema, &prevSeq))
	})

	// --- composite type ---
	comp := model.CompositeType{Name: "addr", Schema: schema, Fields: []model.CompositeField{{Name: "city", PGType: mustParse("text")}, {Name: "zip", PGType: mustParse("text")}}, Comment: "address"}
	t.Run("create_composite_type", func(t *testing.T) {
		op, err := BuildCreateCompositeType(store, comp, schema)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreateCompositeType(schema, comp, false))
		assertEq(t, down, pgsql.DropCompositeType(schema, comp.Name, true))
	})

	// --- domain ---
	dom := model.Domain{Name: "email_addr", Schema: schema, BaseType: mustParse("text"), Check: "VALUE ~ '@'", Comment: "email"}
	t.Run("create_domain", func(t *testing.T) {
		op, err := BuildCreateDomain(store, dom, schema)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreateDomain(schema, dom, false))
		assertEq(t, down, pgsql.DropDomain(schema, dom.Name, true))
	})

	// --- function ---
	fn := model.Function{Name: "add", Schema: schema, Language: "sql", ReturnType: "integer", Args: []model.FunctionArg{{Name: "a", Type: mustParse("integer")}, {Name: "b", Type: mustParse("integer")}}, Body: "SELECT a + b", Volatility: "immutable"}
	t.Run("create_function", func(t *testing.T) {
		op, err := BuildCreateFunction(store, fn, schema)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreateFunction(schema, fn))
		assertEq(t, down, pgsql.DropFunction(schema, fn, false))
	})

	// --- create_or_replace_function (declared inverse restores prev) ---
	prevFn := model.Function{Name: "add", Schema: schema, Language: "sql", ReturnType: "integer", Args: []model.FunctionArg{{Name: "a", Type: mustParse("integer")}, {Name: "b", Type: mustParse("integer")}}, Body: "SELECT b + a", Volatility: "immutable"}
	t.Run("create_or_replace_function", func(t *testing.T) {
		op, err := BuildCreateOrReplaceFunction(store, fn, &prevFn, schema)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreateFunction(schema, fn))
		assertEq(t, down, pgsql.CreateFunction(schema, prevFn))
	})

	// --- trigger ---
	trig := model.Trigger{Name: "audit", Function: "app.audit_fn", Events: []string{"INSERT", "UPDATE"}, Timing: "AFTER", ForEach: "ROW"}
	table := "app.users"
	t.Run("create_trigger", func(t *testing.T) {
		op, err := BuildCreateTrigger(store, trig, table, 17)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreateTrigger(schema, "users", trig, false, 17))
		assertEq(t, down, "DROP TRIGGER IF EXISTS "+pgsql.QuoteIdent(trig.Name)+" ON "+pgsql.QualifiedName(schema, "users")+";")
	})

	// --- policy ---
	pol := model.Policy{Name: "user_isolation", Operation: "SELECT", Role: "app_user", Using: "user_id = current_user_id()"}
	t.Run("create_policy", func(t *testing.T) {
		op, err := BuildCreatePolicy(store, pol, table, 17)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreatePolicy(schema, "users", pol, false, 17))
		assertEq(t, down, pgsql.DropPolicy(schema, "users", pol.Name))
	})

	// --- partition (PartitionChildSpec + ParentTable) ---
	childSpec := model.PartitionSpec{Strategy: "range", Name: "users_2026", Bound: "FROM ('2026-01-01') TO ('2027-01-01')"}
	t.Run("create_partition", func(t *testing.T) {
		op, err := BuildCreatePartition(store, childSpec, "app.users")
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, pgsql.CreatePartitionOf(schema, &childSpec, "users", false))
		assertEq(t, down, "DROP TABLE "+pgsql.QualifiedName(schema, childSpec.Name)+";")
	})

	// --- RawSQL: SM trigger (opaque blob) ---
	t.Run("raw_sm_trigger", func(t *testing.T) {
		upSQL := "CREATE TRIGGER sm_status BEFORE UPDATE ON app.users FOR EACH ROW EXECUTE FUNCTION app.sm_status();"
		downSQL := "DROP TRIGGER IF EXISTS sm_status ON app.users;"
		op, err := BuildRawOp(store, "create_sm_trigger", 3, upSQL, "drop_sm_trigger", 3, downSQL)
		if err != nil {
			t.Fatal(err)
		}
		if op.Target().String() != "raw:3" {
			t.Errorf("raw target = %q, want raw:3", op.Target().String())
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, upSQL)
		assertEq(t, down, downSQL)
	})

	// --- partman config ops (RawSQL) ---
	t.Run("partman_config", func(t *testing.T) {
		upSQL := pgsql.CreatePartmanParent(schema, "events", "created_at", "1 month", 4)
		downSQL := "SELECT partman.undo_partition('app.events');"
		op, err := BuildRawOp(store, "create_partman_parent", 0, upSQL, "undo_partman", 0, downSQL)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, upSQL)
		assertEq(t, down, downSQL)
	})

	t.Run("partman_retention", func(t *testing.T) {
		upSQL := pgsql.UpdatePartmanConfig(schema, "events", "3 months", false)
		downSQL := pgsql.UpdatePartmanConfig(schema, "events", "6 months", false)
		op, err := BuildRawOp(store, "update_partman_retention", 1, upSQL, "update_partman_retention", 1, downSQL)
		if err != nil {
			t.Fatal(err)
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, upSQL)
		assertEq(t, down, downSQL)
	})

	// --- DML (declared, vacuous inverse) ---
	t.Run("dml_backfill", func(t *testing.T) {
		upSQL := "UPDATE app.users SET status = 'active' WHERE status IS NULL;"
		downSQL := "-- irreversible data migration"
		op, err := BuildDMLOp(store, "backfill", 2, upSQL, 2, downSQL)
		if err != nil {
			t.Fatal(err)
		}
		if op.Target().String() != "dml:2" {
			t.Errorf("dml target = %q, want dml:2", op.Target().String())
		}
		up, down := roundTrip(t, store, op)
		assertEq(t, up, upSQL)
		assertEq(t, down, downSQL)
	})
}

func assertEq(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("render mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestParseHardFailsOnMissingPayload proves an op whose payload does not resolve
// is unrepresentable: ParseOp is a hard error, never a silent degraded op.
func TestParseHardFailsOnMissingPayload(t *testing.T) {
	store := newTestStore(t)
	j := OpJSON{
		Kind:          "create_table",
		Target:        keyJSON{Kind: "table", Schema: "app", Name: "ghost"},
		Invertibility: chain.MechanicallyInvertible.String(),
		PayloadID:     "0000000000000000000000000000000000000000000000000000000000000000",
	}
	if _, err := ParseOp(store, j); err == nil {
		t.Fatal("ParseOp resolved a nonexistent payload; want hard error")
	}
}

// TestVerifyDownRejectsTamper proves the load-time down-cache verifier catches a
// down that is not the re-derivation of the up payload.
func TestVerifyDownRejectsTamper(t *testing.T) {
	store := newTestStore(t)
	view := model.View{Name: "v", Schema: "app", Query: "SELECT 1", Comment: "c"}
	op, err := BuildCreateView(store, view, "app")
	if err != nil {
		t.Fatal(err)
	}
	// Tamper: swap the down for an unrelated op's down.
	other, err := BuildCreateSequence(store, model.Sequence{Name: "s", Schema: "app"}, "app")
	if err != nil {
		t.Fatal(err)
	}
	tampered := op
	od, _ := other.Inverse()
	odc := od.(SelfContainedOp)
	tampered.down = &odc
	if err := VerifyDown(store, tampered); err == nil {
		t.Fatal("VerifyDown accepted a tampered down; want hard error")
	}
}
