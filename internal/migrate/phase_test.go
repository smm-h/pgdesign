package migrate

import (
	"strings"
	"testing"
)

func TestClassifyPhase_MixedPhases(t *testing.T) {
	m := &Migration{
		Description: "mixed phases",
		DDLOps: []DDLOp{
			{Op: "create_table", Table: "users", PK: []string{"id"}, Comment: "Users"},
			{Op: "add_column", Table: "users", Column: "email", NotNull: false},
			{Op: "drop_column", Table: "users", Column: "legacy"},
			{Op: "drop_table", Table: "old_users"},
		},
	}

	AnnotatePhases(m, 0)

	if !HasPhases(m) {
		t.Fatal("expected HasPhases to return true for mixed expand/contract migration")
	}

	want := []string{PhaseExpand, PhaseExpand, PhaseContract, PhaseContract}
	for i, op := range m.DDLOps {
		if op.Phase != want[i] {
			t.Errorf("DDLOps[%d] (%s): got phase %q, want %q", i, op.Op, op.Phase, want[i])
		}
	}
}

func TestClassifyPhase_AllSafe_Collapsed(t *testing.T) {
	m := &Migration{
		Description: "all safe ops",
		DDLOps: []DDLOp{
			{Op: "create_table", Table: "users", PK: []string{"id"}, Comment: "Users"},
			{Op: "add_column", Table: "users", Column: "email", NotNull: false},
			{Op: "create_view", Table: "active_users"},
			{Op: "create_function", Name: "get_user"},
		},
	}

	AnnotatePhases(m, 0)

	if HasPhases(m) {
		t.Fatal("expected HasPhases to return false for all-expand migration (should be collapsed)")
	}

	for i, op := range m.DDLOps {
		if op.Phase != "" {
			t.Errorf("DDLOps[%d] (%s): got phase %q, want empty (collapsed)", i, op.Op, op.Phase)
		}
	}
}

func TestClassifyPhase_DMLOps(t *testing.T) {
	m := &Migration{
		Description: "ddl and dml",
		DDLOps: []DDLOp{
			{Op: "create_table", Table: "users", PK: []string{"id"}, Comment: "Users"},
		},
		DMLOps: []DMLOp{
			{Op: "backfill", SQL: "UPDATE users SET x = 1"},
		},
	}

	AnnotatePhases(m, 0)

	if !HasPhases(m) {
		t.Fatal("expected HasPhases to return true when DML ops are present")
	}

	if m.DDLOps[0].Phase != PhaseExpand {
		t.Errorf("DDLOps[0] (create_table): got phase %q, want %q", m.DDLOps[0].Phase, PhaseExpand)
	}
	if m.DMLOps[0].Phase != PhaseMigrate {
		t.Errorf("DMLOps[0] (backfill): got phase %q, want %q", m.DMLOps[0].Phase, PhaseMigrate)
	}
}

func TestClassifyPhase_ValidateConstraint(t *testing.T) {
	m := &Migration{
		Description: "fk with validate",
		DDLOps: []DDLOp{
			{Op: "add_fk_not_valid", Table: "orders", Name: "fk_user", Columns: []string{"user_id"}, RefTable: "users", RefCols: []string{"id"}},
			{Op: "validate_constraint", Table: "orders", Name: "fk_user"},
		},
	}

	AnnotatePhases(m, 0)

	if !HasPhases(m) {
		t.Fatal("expected HasPhases to return true when validate_constraint is present")
	}

	if m.DDLOps[0].Phase != PhaseExpand {
		t.Errorf("DDLOps[0] (add_fk_not_valid): got phase %q, want %q", m.DDLOps[0].Phase, PhaseExpand)
	}
	if m.DDLOps[1].Phase != PhaseMigrate {
		t.Errorf("DDLOps[1] (validate_constraint): got phase %q, want %q", m.DDLOps[1].Phase, PhaseMigrate)
	}
}

func TestPhase_TOMLRoundTrip(t *testing.T) {
	m := &Migration{
		Description: "test phases",
		DDLOps: []DDLOp{
			{Op: "create_table", Phase: PhaseExpand, Table: "users", PK: []string{"id"}, Comment: "User accounts"},
			{Op: "drop_column", Phase: PhaseContract, Table: "users", Column: "legacy"},
		},
		DMLOps: []DMLOp{
			{Op: "backfill", Phase: PhaseMigrate, SQL: "UPDATE users SET x = 1"},
		},
	}

	toml := FormatMigration(m)

	// Verify TOML contains the phase strings.
	if !strings.Contains(toml, `phase = "expand"`) {
		t.Errorf("TOML output missing expand phase:\n%s", toml)
	}
	if !strings.Contains(toml, `phase = "contract"`) {
		t.Errorf("TOML output missing contract phase:\n%s", toml)
	}
	if !strings.Contains(toml, `phase = "migrate"`) {
		t.Errorf("TOML output missing migrate phase:\n%s", toml)
	}

	// Parse back and verify round-trip.
	parsed, err := ParseMigration(toml)
	if err != nil {
		t.Fatalf("ParseMigration failed: %v", err)
	}

	if len(parsed.DDLOps) != 2 {
		t.Fatalf("expected 2 DDLOps, got %d", len(parsed.DDLOps))
	}
	if len(parsed.DMLOps) != 1 {
		t.Fatalf("expected 1 DMLOp, got %d", len(parsed.DMLOps))
	}

	if parsed.DDLOps[0].Phase != PhaseExpand {
		t.Errorf("parsed DDLOps[0] phase: got %q, want %q", parsed.DDLOps[0].Phase, PhaseExpand)
	}
	if parsed.DDLOps[1].Phase != PhaseContract {
		t.Errorf("parsed DDLOps[1] phase: got %q, want %q", parsed.DDLOps[1].Phase, PhaseContract)
	}
	if parsed.DMLOps[0].Phase != PhaseMigrate {
		t.Errorf("parsed DMLOps[0] phase: got %q, want %q", parsed.DMLOps[0].Phase, PhaseMigrate)
	}
}

func TestPhase_TOMLRoundTrip_NoPhases(t *testing.T) {
	m := &Migration{
		Description: "no phases",
		DDLOps: []DDLOp{
			{Op: "create_table", Table: "users", PK: []string{"id"}, Comment: "Users"},
		},
	}

	toml := FormatMigration(m)

	if strings.Contains(toml, "phase =") {
		t.Errorf("TOML output should not contain phase key for single-phase migration:\n%s", toml)
	}

	parsed, err := ParseMigration(toml)
	if err != nil {
		t.Fatalf("ParseMigration failed: %v", err)
	}

	if len(parsed.DDLOps) != 1 {
		t.Fatalf("expected 1 DDLOp, got %d", len(parsed.DDLOps))
	}
	if parsed.DDLOps[0].Phase != "" {
		t.Errorf("parsed DDLOps[0] phase: got %q, want empty", parsed.DDLOps[0].Phase)
	}
}

func TestClassifyPhase_AddColumnRisk(t *testing.T) {
	// Nullable add_column is Safe -> expand.
	// Pair with drop_table to prevent collapse.
	m := &Migration{
		Description: "nullable add_column",
		DDLOps: []DDLOp{
			{Op: "add_column", Table: "users", Column: "email", NotNull: false},
			{Op: "drop_table", Table: "old_users"},
		},
	}

	AnnotatePhases(m, 0)

	if m.DDLOps[0].Phase != PhaseExpand {
		t.Errorf("nullable add_column: got phase %q, want %q", m.DDLOps[0].Phase, PhaseExpand)
	}

	// NOT NULL add_column without default is Dangerous -> contract.
	// Pair with create_table to prevent collapse.
	m2 := &Migration{
		Description: "not-null add_column without default",
		DDLOps: []DDLOp{
			{Op: "add_column", Table: "users", Column: "name", NotNull: true},
			{Op: "create_table", Table: "new_table", PK: []string{"id"}, Comment: "New"},
		},
	}

	AnnotatePhases(m2, 0)

	if m2.DDLOps[0].Phase != PhaseContract {
		t.Errorf("NOT NULL add_column without default: got phase %q, want %q", m2.DDLOps[0].Phase, PhaseContract)
	}
	if m2.DDLOps[1].Phase != PhaseExpand {
		t.Errorf("create_table paired with contract op: got phase %q, want %q", m2.DDLOps[1].Phase, PhaseExpand)
	}
}

func TestHasPhases(t *testing.T) {
	// Empty migration -> false.
	empty := &Migration{Description: "empty"}
	if HasPhases(empty) {
		t.Error("HasPhases should return false for empty migration")
	}

	// DDLOp with phase set -> true.
	withDDLPhase := &Migration{
		Description: "ddl phase",
		DDLOps:      []DDLOp{{Op: "create_table", Phase: PhaseExpand}},
	}
	if !HasPhases(withDDLPhase) {
		t.Error("HasPhases should return true when DDLOp has phase set")
	}

	// DMLOp with phase set -> true.
	withDMLPhase := &Migration{
		Description: "dml phase",
		DMLOps:      []DMLOp{{Op: "backfill", Phase: PhaseMigrate, SQL: "UPDATE x SET y = 1"}},
	}
	if !HasPhases(withDMLPhase) {
		t.Error("HasPhases should return true when DMLOp has phase set")
	}

	// DDLOp without phase -> false.
	noPhase := &Migration{
		Description: "no phase",
		DDLOps:      []DDLOp{{Op: "create_table"}},
	}
	if HasPhases(noPhase) {
		t.Error("HasPhases should return false when DDLOp has no phase set")
	}
}
