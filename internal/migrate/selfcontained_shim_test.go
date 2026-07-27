package migrate

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
)

// allOpToSQLKinds is every kind OpToSQL(sql_gen.go) handles, plus the DML kinds
// and the 5.1b-minted kinds. The inventory-totality test asserts categoryForKind
// covers all of them — the mechanical check the auditor uses against generate.go.
var allOpToSQLKinds = []string{
	"create_table", "create_partition", "drop_table", "add_column", "drop_column",
	"alter_column_type", "set_not_null", "drop_not_null", "alter_column_default",
	"drop_column_default", "rename_column", "rename_table", "add_fk", "drop_fk",
	"add_fk_not_valid", "validate_constraint", "create_index", "add_index",
	"drop_index", "create_index_concurrently", "drop_index_concurrently",
	"alter_index_set", "add_unique", "drop_unique", "add_check", "drop_check",
	"add_exclusion", "drop_exclusion", "create_enum", "alter_enum_add_value",
	"drop_enum", "set_owner", "create_function", "drop_function", "create_trigger",
	"drop_trigger", "create_view", "drop_view", "create_or_replace_view",
	"create_materialized_view", "drop_materialized_view", "refresh_materialized_view",
	"set_statistics", "create_sequence", "drop_sequence", "alter_sequence",
	"create_composite_type", "drop_composite_type", "create_or_replace_function",
	"create_domain", "drop_domain", "alter_domain_add_constraint",
	"alter_domain_drop_constraint", "alter_domain_set_default",
	"alter_domain_drop_default", "alter_domain_set_not_null",
	"alter_domain_drop_not_null", "create_policy", "drop_policy", "enable_rls",
	"disable_rls", "force_rls", "no_force_rls", "create_sm_trigger_function",
	"create_sm_trigger", "create_partman_parent", "update_partman_retention",
	"update_partman_premake",
	// DML kinds (DMLOp) + 5.1b-minted kinds.
	"backfill", "transform", "schema_meta",
	"create_deny_mutation_function", "create_append_only_trigger",
}

// TestInventoryTotality proves the self-contained inventory covers EVERY op kind
// generate emits (via OpToSQL) plus the DML and 5.1b-minted kinds — no kind is
// left unclassified.
func TestInventoryTotality(t *testing.T) {
	for _, k := range allOpToSQLKinds {
		if _, ok := categoryForKind(k); !ok {
			t.Errorf("kind %q has no simulation category (inventory gap)", k)
		}
	}
}

// richModel is a canonicalized post-state schema containing an owner for every
// op family the shim-render test exercises.
func richModel(t *testing.T) *model.Schema {
	t.Helper()
	s := &model.Schema{
		Name:       "app",
		Extensions: []string{"pgcrypto"},
		PGVersion:  17,
		Enums: []model.Enum{
			{Schema: "app", Name: "status_enum", Values: []string{"active", "inactive"}},
		},
		Domains: []model.Domain{
			{Schema: "app", Name: "email_addr", BaseType: mustParse("text"), Check: "VALUE ~ '@'", Comment: "email"},
		},
		CompositeTypes: []model.CompositeType{
			{Schema: "app", Name: "addr", Fields: []model.CompositeField{{Name: "city", PGType: mustParse("text")}}, Comment: "addr"},
		},
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "app",
			Comment: "users",
			Columns: []model.Column{
				{Name: "id", PGType: mustParse("bigint"), NotNull: true},
				{Name: "email", PGType: mustParse("text"), NotNull: true},
			},
			PK: []string{"id"},
		}},
		Views: []model.View{
			{Schema: "app", Name: "v", Query: "SELECT id FROM app.users", Comment: "v"},
		},
		MaterializedViews: []model.MaterializedView{
			{Schema: "app", Name: "mv", Query: "SELECT count(*) FROM app.users", Comment: "mv"},
		},
		Sequences: []model.Sequence{
			{Schema: "app", Name: "seq", Comment: "seq"},
		},
		Functions: []model.Function{
			{Schema: "app", Name: "fn", Language: "sql", ReturnType: "integer", Args: []model.FunctionArg{{Name: "a", Type: mustParse("integer")}}, Body: "SELECT a", Volatility: "immutable"},
		},
	}
	s.Canonicalize()
	return s
}

// shimCase is a shim-render case: the legacy op, whether OpToSQL is the render
// oracle for the up (true for all covered kinds), and whether to byte-compare the
// down against OpToSQL of the recorded legacy down.
type shimCase struct {
	name       string
	op         DDLOp
	wantInv    chain.InvertibilityClass
	assertDown bool // byte-compare down render against OpToSQL(op.Down.Ops[0])
}

// TestShimRendersLikeOpToSQL is point 6's table: every kind generate emits
// converts through the shim and renders byte-identically to OpToSQL. Down renders
// are byte-compared for the delta families (where OpToSQL is the down oracle).
func TestShimRendersLikeOpToSQL(t *testing.T) {
	desired := richModel(t)
	view := &desired.Views[0]
	mv := &desired.MaterializedViews[0]
	seq := &desired.Sequences[0]
	comp := &desired.CompositeTypes[0]
	dom := &desired.Domains[0]
	fn := &desired.Functions[0]

	stat := 200
	cases := []shimCase{
		// whole-object creates (mechanically-invertible; down is a plain drop)
		{"create_view", DDLOp{Op: "create_view", Name: "app.v", ViewDef: view}, chain.MechanicallyInvertible, false},
		{"create_materialized_view", DDLOp{Op: "create_materialized_view", Name: "app.mv", MaterializedViewDef: mv}, chain.MechanicallyInvertible, false},
		{"create_sequence", DDLOp{Op: "create_sequence", Name: "seq", Schema: "app", SequenceDef: seq}, chain.MechanicallyInvertible, false},
		{"create_composite_type", DDLOp{Op: "create_composite_type", Name: "addr", Schema: "app", CompositeTypeDef: comp}, chain.MechanicallyInvertible, false},
		{"create_domain", DDLOp{Op: "create_domain", Name: "email_addr", Schema: "app", DomainDef: dom}, chain.MechanicallyInvertible, false},
		{"create_function", DDLOp{Op: "create_function", Name: "fn", Schema: "app", FunctionDef: fn}, chain.MechanicallyInvertible, false},
		{"create_enum", DDLOp{Op: "create_enum", Name: "status_enum", Schema: "app", Values: []string{"active", "inactive"}}, chain.MechanicallyInvertible, false},
		// declared-inverse creates
		{"create_or_replace_view", DDLOp{Op: "create_or_replace_view", Name: "app.v", ViewDef: view,
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_view", Name: "app.v"}}}}, chain.DeclaredInverse, false},
		{"alter_sequence", DDLOp{Op: "alter_sequence", Name: "seq", Schema: "app", SequenceDef: seq,
			Down: &DownOp{Ops: []DDLOp{{Op: "alter_sequence", Name: "seq", Schema: "app", SequenceDef: seq}}}}, chain.DeclaredInverse, false},

		// whole-object drops
		{"drop_table", DDLOp{Op: "drop_table", Table: "app.old", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"drop_view", DDLOp{Op: "drop_view", Name: "app.v", Down: &DownOp{Ops: []DDLOp{{Op: "create_view", Name: "app.v", ViewDef: view}}}}, chain.DeclaredInverse, false},
		{"drop_materialized_view", DDLOp{Op: "drop_materialized_view", Name: "app.mv", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"drop_sequence", DDLOp{Op: "drop_sequence", Name: "app.seq", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"drop_composite_type", DDLOp{Op: "drop_composite_type", Name: "addr", Schema: "app", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"drop_domain", DDLOp{Op: "drop_domain", Name: "email_addr", Schema: "app", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"drop_function", DDLOp{Op: "drop_function", Name: "fn", Schema: "app", FunctionDef: fn, Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"drop_enum", DDLOp{Op: "drop_enum", Name: "status_enum", Schema: "app", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},

		// column nested-modifiers
		{"add_column", DDLOp{Op: "add_column", Table: "app.users", Column: "nick", Type: "text", NotNull: true,
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_column", Table: "app.users", Column: "nick"}}}}, chain.DeclaredInverse, true},
		{"drop_column", DDLOp{Op: "drop_column", Table: "app.users", Column: "email",
			Down: &DownOp{Ops: []DDLOp{{Op: "add_column", Table: "app.users", Column: "email", Type: "text", NotNull: true}}}}, chain.DeclaredInverse, true},
		{"drop_column_irreversible", DDLOp{Op: "drop_column", Table: "app.users", Column: "email", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"alter_column_type", DDLOp{Op: "alter_column_type", Table: "app.users", Column: "id", Type: "numeric", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"set_not_null", DDLOp{Op: "set_not_null", Table: "app.users", Column: "email",
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_not_null", Table: "app.users", Column: "email"}}}}, chain.DeclaredInverse, true},
		{"drop_not_null", DDLOp{Op: "drop_not_null", Table: "app.users", Column: "email",
			Down: &DownOp{Ops: []DDLOp{{Op: "set_not_null", Table: "app.users", Column: "email"}}}}, chain.DeclaredInverse, true},
		{"alter_column_default", DDLOp{Op: "alter_column_default", Table: "app.users", Column: "email", Default: "x", Type: "text", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"drop_column_default", DDLOp{Op: "drop_column_default", Table: "app.users", Column: "email", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"set_statistics", DDLOp{Op: "set_statistics", Table: "app.users", Column: "email", Statistics: &stat, Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"set_owner", DDLOp{Op: "set_owner", Table: "app.users", Name: "app_owner", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"rename_column", DDLOp{Op: "rename_column", Table: "app.users", Column: "email", Name: "email2"}, chain.MechanicallyInvertible, false},

		// constraint / index nested-modifiers
		{"add_fk", DDLOp{Op: "add_fk", Table: "app.users", Name: "fk_u", Columns: []string{"id"}, RefTable: "app.users", RefCols: []string{"id"},
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_fk", Table: "app.users", Name: "fk_u"}}}}, chain.DeclaredInverse, true},
		{"add_fk_not_valid", DDLOp{Op: "add_fk_not_valid", Table: "app.users", Name: "fk_u", Columns: []string{"id"}, RefTable: "app.users", RefCols: []string{"id"},
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_fk", Table: "app.users", Name: "fk_u"}}}}, chain.DeclaredInverse, true},
		{"drop_fk", DDLOp{Op: "drop_fk", Table: "app.users", Name: "fk_u", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"validate_constraint", DDLOp{Op: "validate_constraint", Table: "app.users", Name: "fk_u", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"create_index", DDLOp{Op: "create_index", Table: "app.users", Name: "ix_email", Columns: []string{"email"},
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_index", Table: "app.users", Name: "ix_email"}}}}, chain.DeclaredInverse, true},
		{"add_index", DDLOp{Op: "add_index", Table: "app.users", Name: "ix_email", Columns: []string{"email"},
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_index", Table: "app.users", Name: "ix_email"}}}}, chain.DeclaredInverse, true},
		{"drop_index", DDLOp{Op: "drop_index", Table: "app.users", Name: "ix_email", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"create_index_concurrently", DDLOp{Op: "create_index_concurrently", Table: "app.users", Name: "ix_email", Columns: []string{"email"},
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_index_concurrently", Table: "app.users", Name: "ix_email"}}}}, chain.DeclaredInverse, true},
		{"drop_index_concurrently", DDLOp{Op: "drop_index_concurrently", Table: "app.users", Name: "ix_email", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"alter_index_set", DDLOp{Op: "alter_index_set", Table: "app.users", Name: "ix_email", With: map[string]string{"fillfactor": "70"}, Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"add_unique", DDLOp{Op: "add_unique", Table: "app.users", Name: "uq_email", Columns: []string{"email"},
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_unique", Table: "app.users", Name: "uq_email"}}}}, chain.DeclaredInverse, true},
		{"drop_unique", DDLOp{Op: "drop_unique", Table: "app.users", Name: "uq_email", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"add_check", DDLOp{Op: "add_check", Table: "app.users", Name: "ck_email", Expr: "email <> ''",
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_check", Table: "app.users", Name: "ck_email"}}}}, chain.DeclaredInverse, true},
		{"drop_check", DDLOp{Op: "drop_check", Table: "app.users", Name: "ck_email", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"add_exclusion", DDLOp{Op: "add_exclusion", Table: "app.users", Name: "ex_email", Columns: []string{"email"}, Operators: []string{"="}, Method: "gist",
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_exclusion", Table: "app.users", Name: "ex_email"}}}}, chain.DeclaredInverse, true},
		{"drop_exclusion", DDLOp{Op: "drop_exclusion", Table: "app.users", Name: "ex_email", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},

		// enum / domain modifiers
		{"alter_enum_add_value", DDLOp{Op: "alter_enum_add_value", Schema: "app", Name: "status_enum", Values: []string{"archived"}, Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"alter_domain_add_constraint", DDLOp{Op: "alter_domain_add_constraint", Schema: "app", Name: "email_addr", Column: "c1", Expr: "VALUE <> ''",
			Down: &DownOp{Ops: []DDLOp{{Op: "alter_domain_drop_constraint", Schema: "app", Name: "email_addr", Column: "c1"}}}}, chain.DeclaredInverse, true},
		{"alter_domain_drop_constraint", DDLOp{Op: "alter_domain_drop_constraint", Schema: "app", Name: "email_addr", Column: "c1", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"alter_domain_set_default", DDLOp{Op: "alter_domain_set_default", Schema: "app", Name: "email_addr", Default: "x", Type: "text", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"alter_domain_drop_default", DDLOp{Op: "alter_domain_drop_default", Schema: "app", Name: "email_addr", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"alter_domain_set_not_null", DDLOp{Op: "alter_domain_set_not_null", Schema: "app", Name: "email_addr",
			Down: &DownOp{Ops: []DDLOp{{Op: "alter_domain_drop_not_null", Schema: "app", Name: "email_addr"}}}}, chain.DeclaredInverse, true},
		{"alter_domain_drop_not_null", DDLOp{Op: "alter_domain_drop_not_null", Schema: "app", Name: "email_addr",
			Down: &DownOp{Ops: []DDLOp{{Op: "alter_domain_set_not_null", Schema: "app", Name: "email_addr"}}}}, chain.DeclaredInverse, true},

		// trigger / policy / rls
		{"create_trigger", DDLOp{Op: "create_trigger", Table: "app.users", Name: "trg", PGVersion: 17,
			TriggerDef: &model.Trigger{Name: "trg", Function: "app.fn", Events: []string{"INSERT"}, Timing: "AFTER", ForEach: "ROW"},
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_trigger", Table: "app.users", Name: "trg"}}}}, chain.MechanicallyInvertible, false},
		{"drop_trigger", DDLOp{Op: "drop_trigger", Table: "app.users", Name: "trg", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"create_policy", DDLOp{Op: "create_policy", Table: "app.users", Name: "pol", PGVersion: 17,
			PolicyDef: &model.Policy{Name: "pol", Operation: "SELECT", Role: "app_user", Using: "id > 0"},
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_policy", Table: "app.users", Name: "pol"}}}}, chain.MechanicallyInvertible, false},
		{"drop_policy", DDLOp{Op: "drop_policy", Table: "app.users", Name: "pol", Down: &DownOp{Irreversible: true}}, chain.NonInvertible, false},
		{"enable_rls", DDLOp{Op: "enable_rls", Table: "app.users", Schema: "app",
			Down: &DownOp{Ops: []DDLOp{{Op: "disable_rls", Table: "app.users", Schema: "app"}}}}, chain.DeclaredInverse, true},
		{"disable_rls", DDLOp{Op: "disable_rls", Table: "app.users", Schema: "app",
			Down: &DownOp{Ops: []DDLOp{{Op: "enable_rls", Table: "app.users", Schema: "app"}}}}, chain.DeclaredInverse, true},
		{"force_rls", DDLOp{Op: "force_rls", Table: "app.users", Schema: "app",
			Down: &DownOp{Ops: []DDLOp{{Op: "no_force_rls", Table: "app.users", Schema: "app"}}}}, chain.DeclaredInverse, true},
		{"no_force_rls", DDLOp{Op: "no_force_rls", Table: "app.users", Schema: "app",
			Down: &DownOp{Ops: []DDLOp{{Op: "force_rls", Table: "app.users", Schema: "app"}}}}, chain.DeclaredInverse, true},

		// refresh (manifest no-op, non-invertible)
		{"refresh_materialized_view", DDLOp{Op: "refresh_materialized_view", Name: "app.mv"}, chain.NonInvertible, false},

		// machinery: nil-def create -> distinct raw kind
		{"deny_mutation_fn", DDLOp{Op: "create_function", Table: "app.users",
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_function", Table: "app.users", Name: "pgdesign_deny_mutation"}}}}, chain.DeclaredInverse, false},
		{"append_only_trigger", DDLOp{Op: "create_trigger", Table: "app.users", PGVersion: 17,
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_trigger", Table: "app.users", Name: "deny_mutation"}}}}, chain.DeclaredInverse, false},

		// raw: sm/partman
		{"create_sm_trigger", DDLOp{Op: "create_sm_trigger", Table: "app.users", RawSQL: "CREATE TRIGGER x ...;",
			Down: &DownOp{Ops: []DDLOp{{Op: "drop_trigger", Table: "app.users", Name: "x", RawSQL: "DROP TRIGGER x;"}}}}, chain.DeclaredInverse, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			sc, err := DDLOpToSelfContained(store, tc.op, desired, 0)
			if err != nil {
				t.Fatalf("shim: %v", err)
			}
			if sc.Invertibility() != tc.wantInv {
				t.Errorf("invertibility = %v, want %v", sc.Invertibility(), tc.wantInv)
			}
			// Round-trip through the store, verify the down cache, and render.
			data, err := MarshalOp(sc)
			if err != nil {
				t.Fatalf("MarshalOp: %v", err)
			}
			parsed, err := UnmarshalOp(store, data)
			if err != nil {
				t.Fatalf("UnmarshalOp: %v", err)
			}
			if err := VerifyDown(store, parsed); err != nil {
				t.Fatalf("VerifyDown: %v", err)
			}
			up, err := parsed.RenderSQL(store)
			if err != nil {
				t.Fatalf("RenderSQL(up): %v", err)
			}
			if want := OpToSQL(tc.op); up != want {
				t.Errorf("up render mismatch:\n got: %q\nwant: %q", up, want)
			}
			if tc.assertDown {
				inv, ok := parsed.Inverse()
				if !ok {
					t.Fatalf("expected an inverse")
				}
				down, err := inv.(SelfContainedOp).RenderSQL(store)
				if err != nil {
					t.Fatalf("RenderSQL(down): %v", err)
				}
				if want := OpToSQL(tc.op.Down.Ops[0]); down != want {
					t.Errorf("down render mismatch:\n got: %q\nwant: %q", down, want)
				}
			}
		})
	}
}

// TestShimRenameTableRoundTrips proves rename_table converts, renders, and its
// structural (swapped) inverse round-trips.
func TestShimRenameTableRoundTrips(t *testing.T) {
	store := newTestStore(t)
	desired := richModel(t)
	op := DDLOp{Op: "rename_table", Table: "app.users", Name: "members"}
	// The post-state table is named "members" in desired.
	desired.Tables[0].Name = "members"
	desired.Canonicalize()

	sc, err := DDLOpToSelfContained(store, op, desired, 0)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	if sc.Invertibility() != chain.MechanicallyInvertible {
		t.Fatalf("rename_table invertibility = %v", sc.Invertibility())
	}
	up, _ := sc.RenderSQL(store)
	assertEq(t, up, OpToSQL(op))
	inv, ok := sc.Inverse()
	if !ok {
		t.Fatal("rename_table has no inverse")
	}
	down, _ := inv.(SelfContainedOp).RenderSQL(store)
	// The inverse renames members back to users.
	assertEq(t, down, OpToSQL(DDLOp{Op: "rename_table", Table: "app.members", Name: "users"}))
	if err := VerifyDown(store, sc); err != nil {
		t.Fatalf("VerifyDown: %v", err)
	}
}

// TestSchemaMetaRoundTrip proves the schema-meta op round-trips and renders the
// extension DDL, with a declared inverse restoring the prior meta.
func TestSchemaMetaRoundTrip(t *testing.T) {
	store := newTestStore(t)
	prev := &model.Schema{Name: "app", Extensions: []string{"pgcrypto"}, PGVersion: 17}
	desired := &model.Schema{Name: "app", Extensions: []string{"pgcrypto", "uuid-ossp"}, PGVersion: 17}
	op, err := BuildSchemaMeta(store, desired, prev)
	if err != nil {
		t.Fatalf("BuildSchemaMeta: %v", err)
	}
	if op.Target().String() != "schema:app" {
		t.Errorf("target = %q, want schema:app", op.Target().String())
	}
	up, down := roundTrip(t, store, op)
	if !containsWord(up, "uuid-ossp") || !containsWord(up, "pgcrypto") {
		t.Errorf("schema-meta up render missing extensions:\n%s", up)
	}
	if !containsWord(down, "pgcrypto") {
		t.Errorf("schema-meta down render missing prior extension:\n%s", down)
	}
}

// TestShimEndToEndSimulation is the 5.2-foundation fixture the earlier layer
// could not write: model A -> model B (one added column + one new index + a
// changed extension list). The edge ops are built via the shim/builders and the
// simulator must carry from-manifest(A) EXACTLY to to-manifest(B).
func TestShimEndToEndSimulation(t *testing.T) {
	store := newTestStore(t)

	modelA := &model.Schema{
		Name:       "app",
		Extensions: []string{"pgcrypto"},
		PGVersion:  17,
		Tables: []model.Table{{
			Name: "users", Schema: "app", Comment: "users",
			Columns: []model.Column{{Name: "id", PGType: mustParse("bigint"), NotNull: true}},
			PK:      []string{"id"},
		}},
	}
	modelA.Canonicalize()

	modelB := &model.Schema{
		Name:       "app",
		Extensions: []string{"pgcrypto", "uuid-ossp"}, // changed extension list
		PGVersion:  17,
		Tables: []model.Table{{
			Name: "users", Schema: "app", Comment: "users",
			Columns: []model.Column{
				{Name: "id", PGType: mustParse("bigint"), NotNull: true},
				{Name: "email", PGType: mustParse("text"), NotNull: true}, // added column
			},
			PK:      []string{"id"},
			Indexes: []model.Index{{Name: "ix_users_email", Columns: []string{"email"}}}, // new index
		}},
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

	// The edge ops (as generate would emit them), converted via the shim/builders
	// against the POST-STATE model B.
	addCol := DDLOp{Op: "add_column", Table: "app.users", Column: "email", Type: "text", NotNull: true,
		Down: &DownOp{Ops: []DDLOp{{Op: "drop_column", Table: "app.users", Column: "email"}}}}
	addIdx := DDLOp{Op: "create_index", Table: "app.users", Name: "ix_users_email", Columns: []string{"email"},
		Down: &DownOp{Ops: []DDLOp{{Op: "drop_index", Table: "app.users", Name: "ix_users_email"}}}}

	scCol, err := DDLOpToSelfContained(store, addCol, modelB, 0)
	if err != nil {
		t.Fatalf("shim add_column: %v", err)
	}
	scIdx, err := DDLOpToSelfContained(store, addIdx, modelB, 1)
	if err != nil {
		t.Fatalf("shim create_index: %v", err)
	}
	scMeta, err := BuildSchemaMeta(store, modelB, modelA)
	if err != nil {
		t.Fatalf("BuildSchemaMeta: %v", err)
	}

	ops := []chain.Op{scCol, scIdx, scMeta}
	sim := opSimulator{store: store}
	got, err := sim.Simulate(fromManifest, ops)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if !got.Equal(toManifest) {
		d := toManifest.Diff(got)
		t.Fatalf("simulated manifest != to-manifest (added=%v removed=%v changed=%v)", d.Added, d.Removed, d.Changed)
	}
}

var _ = objstore.ID
var _ = enc.KindTable
