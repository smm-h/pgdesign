package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sql"
	"github.com/smm-h/pgdesign/internal/testdb"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// --- Unit tests (no DB required) ---

func TestGenerateMigration_AddTable(t *testing.T) {
	desired := &model.Schema{
		Name: "game",
		Tables: []model.Table{
			{
				Name:   "players",
				Schema: "game",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
				},
				Comment: "Player accounts",
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesAdded: []string{"game.players"},
	}

	m, diags := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}
	if m.Version != "0.1.0" {
		t.Errorf("version = %q, want %q", m.Version, "0.1.0")
	}

	// Should have a create_table op.
	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_table" && op.Table == "game.players" {
			found = true
			if op.Down == nil {
				t.Error("create_table op has no down op")
			} else if op.Down.Irreversible {
				t.Error("create_table should be reversible (down = drop_table)")
			} else if len(op.Down.Ops) == 0 {
				t.Error("create_table down has no ops")
			} else if op.Down.Ops[0].Op != "drop_table" {
				t.Errorf("create_table down op = %q, want drop_table", op.Down.Ops[0].Op)
			}
			break
		}
	}
	if !found {
		t.Error("expected create_table op for game.players")
	}

	// Diagnostics should not contain errors for create_table (it's safe).
	for _, d := range diags {
		if d.Table == "game.players" && d.Code == "MIGRATE_RISK" && strings.Contains(d.Message, "create_table") {
			t.Errorf("unexpected diagnostic for create_table: %s", d.Message)
		}
	}
}

func TestGenerateMigration_AddColumn(t *testing.T) {
	desired := &model.Schema{
		Name: "game",
		Tables: []model.Table{
			{
				Name:   "players",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "level", PGType: typeinfo.T("int4"), NotNull: true, Default: model.StrPtr("1")},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsAdded: []model.Column{
					{Name: "level", PGType: typeinfo.T("int4"), NotNull: true, Default: model.StrPtr("1")},
				},
			},
		},
	}

	m, _ := GenerateMigration(d, desired, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "add_column" && op.Column == "level" {
			found = true
			if op.Type != "int4" {
				t.Errorf("add_column type = %q, want %q", op.Type, "int4")
			}
			if op.Down == nil || len(op.Down.Ops) == 0 {
				t.Error("add_column has no down ops")
			} else if op.Down.Ops[0].Op != "drop_column" {
				t.Errorf("add_column down op = %q, want drop_column", op.Down.Ops[0].Op)
			}
			break
		}
	}
	if !found {
		t.Error("expected add_column op for level")
	}
}

func TestGenerateMigration_AddColumnPGVersionRisk(t *testing.T) {
	// When desired schema has PGVersion=11, add_column with NOT NULL + default
	// should be safe (metadata-only on PG11+), so no risk diagnostic.
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 11,
		Tables: []model.Table{
			{
				Name:   "players",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "level", PGType: typeinfo.T("int4"), NotNull: true, Default: model.StrPtr("1")},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsAdded: []model.Column{
					{Name: "level", PGType: typeinfo.T("int4"), NotNull: true, Default: model.StrPtr("1")},
				},
			},
		},
	}

	_, diags := GenerateMigration(d, desired, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

	// PG11 with constant default: should be safe, no risk diagnostics.
	for _, diag := range diags {
		if diag.Code == "MIGRATE_RISK" && strings.Contains(diag.Message, "add_column") {
			t.Errorf("PGVersion=11: expected no risk diagnostic for add_column with default, got: %s", diag.Message)
		}
	}
}

func TestGenerateMigration_AddColumnPrePG11Risk(t *testing.T) {
	// When desired schema has PGVersion=9, add_column with NOT NULL + default
	// should be dangerous (table rewrite on pre-PG11).
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 9,
		Tables: []model.Table{
			{
				Name:   "players",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "level", PGType: typeinfo.T("int4"), NotNull: true, Default: model.StrPtr("1")},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsAdded: []model.Column{
					{Name: "level", PGType: typeinfo.T("int4"), NotNull: true, Default: model.StrPtr("1")},
				},
			},
		},
	}

	_, diags := GenerateMigration(d, desired, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

	// PG9 with constant default: should be dangerous, expect risk diagnostic.
	hasDangerous := false
	for _, diag := range diags {
		if diag.Code == "MIGRATE_RISK" && strings.Contains(diag.Message, "add_column") {
			hasDangerous = true
			break
		}
	}
	if !hasDangerous {
		t.Error("PGVersion=9: expected dangerous risk diagnostic for add_column with NOT NULL default")
	}
}

func TestGenerateMigration_DropTable(t *testing.T) {
	desired := &model.Schema{Name: "game"}
	d := &diff.SchemaDiff{
		TablesRemoved: []string{"game.old_table"},
	}

	m, diags := GenerateMigration(d, desired, "0.3.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_table" && op.Table == "game.old_table" {
			found = true
			if op.Down == nil || !op.Down.Irreversible {
				t.Error("drop_table should have irreversible down")
			}
			break
		}
	}
	if !found {
		t.Error("expected drop_table op for game.old_table")
	}

	// Should have a dangerous diagnostic.
	hasDangerous := false
	for _, d := range diags {
		if strings.Contains(d.Message, "drop_table") {
			hasDangerous = true
			break
		}
	}
	if !hasDangerous {
		t.Error("expected dangerous diagnostic for drop_table")
	}
}

func TestGenerateMigration_PartitionChildAdded(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "public",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
				},
				Partitioning: &model.PartitionSpec{
					Strategy: "range",
					Columns:  []string{"created_at"},
					Children: []model.PartitionSpec{
						{Name: "events_2024", Bound: "2024-01-01"},
						{Name: "events_2025", Bound: "2025-01-01"},
					},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "events",
				PartitioningChanged: &diff.PartitionDiff{
					ChildrenAdded: []string{"events_2025:2025-01-01"},
				},
			},
		},
	}

	m, diags := GenerateMigration(d, desired, "0.4.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	// Should have a create_partition op.
	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_partition" && op.ParentTable == "events" {
			found = true
			if op.PartitionChildSpec == nil {
				t.Error("create_partition op has no PartitionChildSpec")
			} else if op.PartitionChildSpec.Name != "events_2025" {
				t.Errorf("child spec name = %q, want events_2025", op.PartitionChildSpec.Name)
			}
			if op.Down == nil {
				t.Error("create_partition op has no down op")
			} else if len(op.Down.Ops) == 0 {
				t.Error("create_partition down has no ops")
			} else if op.Down.Ops[0].Op != "drop_table" {
				t.Errorf("create_partition down op = %q, want drop_table", op.Down.Ops[0].Op)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected create_partition op, got ops: %v", opsDebug(m.DDLOps))
	}

	// Should not have error diagnostics (creating a partition is safe).
	for _, diag := range diags {
		if diag.Code == "MIGRATE_RISK" && diag.Severity == 0 { // Error
			t.Errorf("unexpected error diagnostic: %s", diag.Message)
		}
	}
}

func TestGenerateMigration_PartitionChildRemoved(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "public",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true},
				},
				Partitioning: &model.PartitionSpec{
					Strategy: "range",
					Columns:  []string{"created_at"},
					Children: []model.PartitionSpec{
						{Name: "events_2024", Bound: "2024-01-01"},
					},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "events",
				PartitioningChanged: &diff.PartitionDiff{
					ChildrenRemoved: []string{"events_2023:2023-01-01"},
				},
			},
		},
	}

	m, diags := GenerateMigration(d, desired, "0.5.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	// Should have a drop_table op for the removed child.
	found := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_table" && op.Table == "events_2023" {
			found = true
			if op.Down == nil || !op.Down.Irreversible {
				t.Error("drop_table for partition child should have irreversible down")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected drop_table op for events_2023, got ops: %v", opsDebug(m.DDLOps))
	}

	// Should have a dangerous diagnostic for drop_table.
	hasDangerous := false
	for _, diag := range diags {
		if strings.Contains(diag.Message, "drop_table") {
			hasDangerous = true
			break
		}
	}
	if !hasDangerous {
		t.Error("expected dangerous diagnostic for drop_table on partition child")
	}
}

func TestGenerateMigration_PartitionStrategyChanged(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "public",
				Partitioning: &model.PartitionSpec{
					Strategy: "hash",
					Columns:  []string{"id"},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "events",
				PartitioningChanged: &diff.PartitionDiff{
					StrategyChanged: &[2]string{"range", "hash"},
				},
			},
		},
	}

	_, diags := GenerateMigration(d, desired, "0.6.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

	// Should have an error about strategy change.
	hasError := false
	for _, diag := range diags {
		if diag.Code == "PARTITION_STRATEGY_CHANGE" {
			hasError = true
			if diag.Severity != diagnostic.Error {
				t.Errorf("expected Error severity for PARTITION_STRATEGY_CHANGE, got %d", diag.Severity)
			}
			if !strings.Contains(diag.Message, "requires table rebuild") {
				t.Errorf("expected 'requires table rebuild' in message, got: %s", diag.Message)
			}
			break
		}
	}
	if !hasError {
		t.Error("expected PARTITION_STRATEGY_CHANGE diagnostic")
	}
}

func TestGenerateMigration_MaintenanceRetentionChange(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "public",
				Maintenance: &model.MaintenanceConfig{
					Interval: "1 month", Premake: 4, Retention: "12 months",
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "events",
				MaintenanceChanged: &diff.MaintenanceDiff{
					RetentionChanged: &[2]string{"6 months", "12 months"},
				},
			},
		},
	}

	m, diags := GenerateMigration(d, desired, "0.6.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if diagnostic.Diagnostics(diags).HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	// Should produce an update_partman_retention op with safe SQL.
	found := false
	for _, op := range m.DDLOps {
		if op.Op == "update_partman_retention" {
			found = true
			if !strings.Contains(op.RawSQL, "retention = '12 months'") {
				t.Errorf("expected retention = '12 months' in SQL, got: %s", op.RawSQL)
			}
			break
		}
	}
	if !found {
		t.Error("expected update_partman_retention op")
	}
}

func TestGenerateMigration_MaintenancePremakeChange(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "public",
				Maintenance: &model.MaintenanceConfig{
					Interval: "1 month", Premake: 6, Retention: "6 months",
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "events",
				MaintenanceChanged: &diff.MaintenanceDiff{
					PremakeChanged: &[2]int{4, 6},
				},
			},
		},
	}

	m, diags := GenerateMigration(d, desired, "0.6.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if diagnostic.Diagnostics(diags).HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	found := false
	for _, op := range m.DDLOps {
		if op.Op == "update_partman_premake" {
			found = true
			if !strings.Contains(op.RawSQL, "premake = 6") {
				t.Errorf("expected premake = 6 in SQL, got: %s", op.RawSQL)
			}
			break
		}
	}
	if !found {
		t.Error("expected update_partman_premake op")
	}
}

func TestGenerateMigration_MaintenanceIntervalChangeError(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:   "events",
				Schema: "public",
				Maintenance: &model.MaintenanceConfig{
					Interval: "1 week", Premake: 4, Retention: "6 months",
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "events",
				MaintenanceChanged: &diff.MaintenanceDiff{
					IntervalChanged: &[2]string{"1 month", "1 week"},
				},
			},
		},
	}

	_, diags := GenerateMigration(d, desired, "0.6.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	hasError := false
	for _, diag := range diags {
		if diag.Code == "MAINTENANCE_INTERVAL_CHANGE" {
			hasError = true
			if diag.Severity != diagnostic.Error {
				t.Errorf("expected Error severity, got %d", diag.Severity)
			}
			if !strings.Contains(diag.Message, "requires repartitioning") {
				t.Errorf("expected 'requires repartitioning' in message, got: %s", diag.Message)
			}
			break
		}
	}
	if !hasError {
		t.Error("expected MAINTENANCE_INTERVAL_CHANGE error diagnostic")
	}
}

func TestOpToSQL_CreatePartition(t *testing.T) {
	childSpec := &model.PartitionSpec{
		Name:  "events_2025",
		Bound: "FROM ('2025-01-01') TO ('2026-01-01')",
	}
	op := DDLOp{
		Op:                 "create_partition",
		Table:              "public.events",
		ParentTable:        "public.events",
		PartitionChildSpec: childSpec,
	}

	result := OpToSQL(op)
	if !strings.Contains(result, "CREATE TABLE") {
		t.Errorf("expected CREATE TABLE, got: %s", result)
	}
	if !strings.Contains(result, "PARTITION OF") {
		t.Errorf("expected PARTITION OF, got: %s", result)
	}
	if !strings.Contains(result, "events_2025") {
		t.Errorf("expected child table name, got: %s", result)
	}
	if !strings.Contains(result, "FOR VALUES") {
		t.Errorf("expected FOR VALUES, got: %s", result)
	}
}

// opsDebug returns a string summary of ops for test failure messages.
func opsDebug(ops []DDLOp) string {
	var parts []string
	for _, op := range ops {
		parts = append(parts, fmt.Sprintf("{Op:%s Table:%s}", op.Op, op.Table))
	}
	return strings.Join(parts, ", ")
}

func TestParseMigrationRoundtrip(t *testing.T) {
	original := &Migration{
		Description: "Add game_like table and player level",
		DDLOps: []DDLOp{
			{
				Op:      "create_table",
				Table:   "game.game_like",
				PK:      []string{"player_id", "game_id"},
				Comment: "Player likes on games",
				Down: &DownOp{
					Ops: []DDLOp{{Op: "drop_table", Table: "game.game_like"}},
				},
			},
			{
				Op:      "add_column",
				Table:   "game.players",
				Column:  "level",
				Type:    "integer",
				Default: int64(1),
				NotNull: true,
				Down: &DownOp{
					Ops: []DDLOp{{Op: "drop_column", Table: "game.players", Column: "level"}},
				},
			},
		},
		DMLOps: []DMLOp{
			{
				Op:  "backfill",
				SQL: "UPDATE game.players SET level = 1 WHERE level IS NULL",
				Down: &DownOp{
					Irreversible: true,
				},
			},
		},
	}

	// Write to temp file.
	dir := t.TempDir()
	path := filepath.Join(dir, "0.1.0.toml")
	if err := WriteMigrationFile(path, original); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read back.
	parsed, err := ParseMigrationFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if parsed.Description != original.Description {
		t.Errorf("description = %q, want %q", parsed.Description, original.Description)
	}
	if len(parsed.DDLOps) != len(original.DDLOps) {
		t.Fatalf("DDL ops count = %d, want %d", len(parsed.DDLOps), len(original.DDLOps))
	}
	if parsed.DDLOps[0].Op != "create_table" {
		t.Errorf("DDL[0].Op = %q, want create_table", parsed.DDLOps[0].Op)
	}
	if parsed.DDLOps[0].Table != "game.game_like" {
		t.Errorf("DDL[0].Table = %q, want game.game_like", parsed.DDLOps[0].Table)
	}
	if parsed.DDLOps[1].Op != "add_column" {
		t.Errorf("DDL[1].Op = %q, want add_column", parsed.DDLOps[1].Op)
	}
	if parsed.DDLOps[1].Column != "level" {
		t.Errorf("DDL[1].Column = %q, want level", parsed.DDLOps[1].Column)
	}
	if !parsed.DDLOps[1].NotNull {
		t.Error("DDL[1].NotNull should be true")
	}
	if len(parsed.DMLOps) != 1 {
		t.Fatalf("DML ops count = %d, want 1", len(parsed.DMLOps))
	}
	if parsed.DMLOps[0].Op != "backfill" {
		t.Errorf("DML[0].Op = %q, want backfill", parsed.DMLOps[0].Op)
	}
	if parsed.DMLOps[0].Down == nil || !parsed.DMLOps[0].Down.Irreversible {
		t.Error("DML[0] should have irreversible down")
	}
}

func TestMigrationRoundTrip_Desc(t *testing.T) {
	original := &Migration{
		Description: "Add index with desc columns",
		DDLOps: []DDLOp{
			{
				Op:      "create_index",
				Table:   "public.events",
				Name:    "idx_events_ts",
				Columns: []string{"created_at", "id"},
				Desc:    []bool{true, false},
				Down: &DownOp{
					Ops: []DDLOp{{Op: "drop_index", Name: "idx_events_ts"}},
				},
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "desc.toml")
	if err := WriteMigrationFile(path, original); err != nil {
		t.Fatalf("write: %v", err)
	}

	parsed, err := ParseMigrationFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(parsed.DDLOps) != 1 {
		t.Fatalf("DDL ops count = %d, want 1", len(parsed.DDLOps))
	}
	op := parsed.DDLOps[0]
	if len(op.Desc) != 2 {
		t.Fatalf("Desc length = %d, want 2", len(op.Desc))
	}
	if !op.Desc[0] {
		t.Error("Desc[0] should be true")
	}
	if op.Desc[1] {
		t.Error("Desc[1] should be false")
	}
}

func TestMigrationRoundTrip_Operators(t *testing.T) {
	original := &Migration{
		Description: "Add exclusion constraint",
		DDLOps: []DDLOp{
			{
				Op:                "add_exclusion",
				Table:             "public.reservations",
				Name:              "excl_reservation",
				Columns:           []string{"room_id", "during"},
				Operators:         []string{"=", "&&"},
				Deferrable:        true,
				InitiallyDeferred: true,
				Down: &DownOp{
					Ops: []DDLOp{{Op: "drop_constraint", Table: "public.reservations", Name: "excl_reservation"}},
				},
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "exclusion.toml")
	if err := WriteMigrationFile(path, original); err != nil {
		t.Fatalf("write: %v", err)
	}

	parsed, err := ParseMigrationFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(parsed.DDLOps) != 1 {
		t.Fatalf("DDL ops count = %d, want 1", len(parsed.DDLOps))
	}
	op := parsed.DDLOps[0]
	if len(op.Operators) != 2 || op.Operators[0] != "=" || op.Operators[1] != "&&" {
		t.Errorf("Operators = %v, want [= &&]", op.Operators)
	}
	if !op.Deferrable {
		t.Error("Deferrable should be true")
	}
	if !op.InitiallyDeferred {
		t.Error("InitiallyDeferred should be true")
	}
}

func TestMigrationRoundTrip_DownInline_Columns(t *testing.T) {
	original := &Migration{
		Description: "Drop exclusion with inline down",
		DDLOps: []DDLOp{
			{
				Op:    "drop_constraint",
				Table: "public.reservations",
				Name:  "excl_reservation",
				Down: &DownOp{
					Ops: []DDLOp{{
						Op:                "add_exclusion",
						Table:             "public.reservations",
						Name:              "excl_reservation",
						Columns:           []string{"room_id", "during"},
						Operators:         []string{"=", "&&"},
						Deferrable:        true,
						InitiallyDeferred: true,
					}},
				},
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "down_inline.toml")
	if err := WriteMigrationFile(path, original); err != nil {
		t.Fatalf("write: %v", err)
	}

	parsed, err := ParseMigrationFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(parsed.DDLOps) != 1 {
		t.Fatalf("DDL ops count = %d, want 1", len(parsed.DDLOps))
	}
	downOps := parsed.DDLOps[0].Down.Ops
	if len(downOps) != 1 {
		t.Fatalf("down ops count = %d, want 1", len(downOps))
	}
	dop := downOps[0]
	if len(dop.Columns) != 2 || dop.Columns[0] != "room_id" {
		t.Errorf("down Columns = %v, want [room_id during]", dop.Columns)
	}
	if len(dop.Operators) != 2 || dop.Operators[0] != "=" {
		t.Errorf("down Operators = %v, want [= &&]", dop.Operators)
	}
	if !dop.Deferrable {
		t.Error("down Deferrable should be true")
	}
	if !dop.InitiallyDeferred {
		t.Error("down InitiallyDeferred should be true")
	}
}

func TestOpToSQL_CreateTable(t *testing.T) {
	table := &model.Table{
		Name:   "players",
		Schema: "game",
		PK:     []string{"id"},
		Columns: []model.Column{
			{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
			{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
		},
	}
	op := DDLOp{
		Op:       "create_table",
		Table:    "game.players",
		TableDef: table,
	}

	sql := OpToSQL(op)
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Errorf("expected CREATE TABLE, got: %s", sql)
	}
	if !strings.Contains(sql, "game") {
		t.Errorf("expected schema name, got: %s", sql)
	}
	if !strings.Contains(sql, "players") {
		t.Errorf("expected table name, got: %s", sql)
	}
}

func TestOpToSQL_AddColumn(t *testing.T) {
	op := DDLOp{
		Op:      "add_column",
		Table:   "game.players",
		Column:  "level",
		Type:    "integer",
		Default: int64(1),
		NotNull: true,
	}

	sql := OpToSQL(op)
	if !strings.Contains(sql, "ALTER TABLE") {
		t.Errorf("expected ALTER TABLE, got: %s", sql)
	}
	if !strings.Contains(sql, "ADD COLUMN") {
		t.Errorf("expected ADD COLUMN, got: %s", sql)
	}
	if !strings.Contains(sql, "NOT NULL") {
		t.Errorf("expected NOT NULL, got: %s", sql)
	}
	if !strings.Contains(sql, "DEFAULT 1") {
		t.Errorf("expected DEFAULT 1, got: %s", sql)
	}
}

func TestOpToSQL_AddFK(t *testing.T) {
	op := DDLOp{
		Op:       "add_fk",
		Table:    "game.scores",
		Name:     "fk_scores_player",
		Columns:  []string{"player_id"},
		RefTable: "game.players",
		RefCols:  []string{"id"},
		OnDelete: "CASCADE",
	}

	sql := OpToSQL(op)
	if !strings.Contains(sql, "FOREIGN KEY") {
		t.Errorf("expected FOREIGN KEY, got: %s", sql)
	}
	if !strings.Contains(sql, "REFERENCES") {
		t.Errorf("expected REFERENCES, got: %s", sql)
	}
	if !strings.Contains(sql, "ON DELETE CASCADE") {
		t.Errorf("expected ON DELETE CASCADE, got: %s", sql)
	}
}

func TestOpToSQL_AddFKNotValid(t *testing.T) {
	op := DDLOp{
		Op:       "add_fk_not_valid",
		Table:    "public.orders",
		Name:     "fk_orders_user",
		Columns:  []string{"user_id"},
		RefTable: "public.users",
		RefCols:  []string{"id"},
		OnDelete: "cascade",
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.orders ADD CONSTRAINT fk_orders_user FOREIGN KEY (user_id) REFERENCES public.users (id) ON DELETE CASCADE NOT VALID;`
	if got != want {
		t.Errorf("OpToSQL(add_fk_not_valid)\ngot:  %s\nwant: %s", got, want)
	}
}

func TestOpToSQL_ValidateConstraint(t *testing.T) {
	op := DDLOp{
		Op:    "validate_constraint",
		Table: "public.orders",
		Name:  "fk_orders_user",
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.orders VALIDATE CONSTRAINT fk_orders_user;`
	if got != want {
		t.Errorf("OpToSQL(validate_constraint)\ngot:  %s\nwant: %s", got, want)
	}
}

func TestOpToSQL_DropTable(t *testing.T) {
	op := DDLOp{
		Op:    "drop_table",
		Table: "game.old_table",
	}
	sql := OpToSQL(op)
	if sql != `DROP TABLE game.old_table;` {
		t.Errorf("unexpected SQL: %s", sql)
	}
}

func TestOpToSQL_CreateIndex(t *testing.T) {
	op := DDLOp{
		Op:      "create_index",
		Table:   "game.players",
		Name:    "idx_players_name",
		Columns: []string{"name"},
	}
	sql := OpToSQL(op)
	if !strings.Contains(sql, "CREATE INDEX") {
		t.Errorf("expected CREATE INDEX, got: %s", sql)
	}
	if !strings.Contains(sql, "idx_players_name") {
		t.Errorf("expected index name, got: %s", sql)
	}
}

func TestOpToSQL_AlterEnumAddValue(t *testing.T) {
	op := DDLOp{
		Op:     "alter_enum_add_value",
		Schema: "game",
		Name:   "status",
		Values: []string{"archived", "it's new"},
	}
	got := OpToSQL(op)
	// IF NOT EXISTS (PG 9.3+) makes failed-then-retried migrations safe.
	if !strings.Contains(got, "ALTER TYPE game.status ADD VALUE IF NOT EXISTS 'archived';") {
		t.Errorf("expected ADD VALUE IF NOT EXISTS for archived, got:\n%s", got)
	}
	if !strings.Contains(got, "ALTER TYPE game.status ADD VALUE IF NOT EXISTS 'it''s new';") {
		t.Errorf("expected escaped ADD VALUE IF NOT EXISTS for it's new, got:\n%s", got)
	}
}

func TestOpToSQL_CreateIndexConcurrently(t *testing.T) {
	op := DDLOp{
		Op:      "create_index_concurrently",
		Table:   "game.players",
		Name:    "idx_players_name",
		Columns: []string{"name"},
	}
	sql := OpToSQL(op)
	if !strings.Contains(sql, "CREATE INDEX CONCURRENTLY") {
		t.Errorf("expected CREATE INDEX CONCURRENTLY, got: %s", sql)
	}
}

func TestIsNonTransactional(t *testing.T) {
	tests := []struct {
		name      string
		op        string
		pgVersion int
		want      bool
	}{
		{"concurrently_create", "create_index_concurrently", 0, true},
		{"concurrently_create_pg16", "create_index_concurrently", 16, true},
		{"concurrently_drop", "drop_index_concurrently", 0, true},
		{"concurrently_drop_pg16", "drop_index_concurrently", 16, true},
		{"enum_add_unknown_version", "alter_enum_add_value", 0, true},
		{"enum_add_pg11", "alter_enum_add_value", 11, true},
		{"enum_add_pg12", "alter_enum_add_value", 12, false},
		{"enum_add_pg16", "alter_enum_add_value", 16, false},
		{"create_table", "create_table", 0, false},
		{"add_column", "add_column", 0, false},
		{"create_index", "create_index", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNonTransactional(DDLOp{Op: tt.op, PGVersion: tt.pgVersion})
			if got != tt.want {
				t.Errorf("IsNonTransactional(%q, pgVersion=%d) = %v, want %v", tt.op, tt.pgVersion, got, tt.want)
			}
		})
	}
}

func TestSemverCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.2.0", -1},
		{"0.2.0", "0.1.0", 1},
		{"1.0.0", "1.0.0", 0},
		{"0.1.0", "0.1.1", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.10.0", "0.9.0", 1},
	}
	for _, tt := range tests {
		got := compareSemver(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestInSemverRange(t *testing.T) {
	tests := []struct {
		version, from, to string
		want              bool
	}{
		// In range.
		{"0.2.0", "0.1.0", "0.3.0", true},
		{"0.1.0", "0.1.0", "0.3.0", true}, // inclusive lower bound
		{"0.3.0", "0.1.0", "0.3.0", true}, // inclusive upper bound
		{"1.0.0", "1.0.0", "1.0.0", true}, // single-version range
		// Out of range.
		{"0.0.9", "0.1.0", "0.3.0", false},
		{"0.3.1", "0.1.0", "0.3.0", false},
		{"2.0.0", "0.1.0", "1.0.0", false},
		// Multi-digit components.
		{"0.10.0", "0.9.0", "0.11.0", true},
		{"0.8.0", "0.9.0", "0.11.0", false},
	}
	for _, tt := range tests {
		got := InSemverRange(tt.version, tt.from, tt.to)
		if got != tt.want {
			t.Errorf("InSemverRange(%q, %q, %q) = %v, want %v", tt.version, tt.from, tt.to, got, tt.want)
		}
	}
}

func TestSplitQualifiedName(t *testing.T) {
	tests := []struct {
		input      string
		wantSchema string
		wantName   string
	}{
		{"game.players", "game", "players"},
		{"players", "public", "players"},
		{"my_schema.my_table", "my_schema", "my_table"},
	}
	for _, tt := range tests {
		schema, name := splitQualifiedName(tt.input)
		if schema != tt.wantSchema || name != tt.wantName {
			t.Errorf("splitQualifiedName(%q) = (%q, %q), want (%q, %q)",
				tt.input, schema, name, tt.wantSchema, tt.wantName)
		}
	}
}

func TestCheckReversibility(t *testing.T) {
	// Reversible migration.
	m := &Migration{
		DDLOps: []DDLOp{
			{
				Op:    "create_table",
				Table: "game.players",
				Down:  &DownOp{Ops: []DDLOp{{Op: "drop_table", Table: "game.players"}}},
			},
		},
	}
	if err := checkReversibility(m); err != nil {
		t.Errorf("expected reversible, got: %v", err)
	}

	// Irreversible migration.
	m.DDLOps = append(m.DDLOps, DDLOp{
		Op:    "drop_table",
		Table: "game.old_table",
		Down:  &DownOp{Irreversible: true},
	})
	if err := checkReversibility(m); err == nil {
		t.Error("expected irreversible error")
	}
}

func TestDiscoverMigrations(t *testing.T) {
	dir := t.TempDir()

	// Create some migration files.
	for _, v := range []string{"0.1.0", "0.3.0", "0.2.0"} {
		content := fmt.Sprintf("description = %q\n", "Migration "+v)
		if err := os.WriteFile(filepath.Join(dir, v+".toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Create a non-migration file (should be skipped).
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Migrations\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := discoverMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("found %d migrations, want 3", len(files))
	}
	// Should be sorted by semver.
	if files[0].version != "0.1.0" {
		t.Errorf("files[0].version = %q, want 0.1.0", files[0].version)
	}
	if files[1].version != "0.2.0" {
		t.Errorf("files[1].version = %q, want 0.2.0", files[1].version)
	}
	if files[2].version != "0.3.0" {
		t.Errorf("files[2].version = %q, want 0.3.0", files[2].version)
	}
}

func TestParseMigrationFromDesignExample(t *testing.T) {
	// Parse the example from DESIGN.md.
	input := `description = "Add game_like table and player level"

[[ddl]]
op = "create_table"
table = "game.game_like"
pk = ["player_id", "game_id"]
comment = "Player likes on games"
down = { op = "drop_table", table = "game.game_like" }

[[ddl]]
op = "add_column"
table = "game.players"
column = "level"
type = "integer"
default = 1
not_null = true
down = { op = "drop_column", table = "game.players", column = "level" }

[[ddl]]
op = "create_index_concurrently"
table = "game.game_like"
name = "idx_game_like_game_id"
columns = ["game_id"]
down = { op = "drop_index_concurrently", name = "idx_game_like_game_id" }

[[dml]]
op = "backfill"
sql = "UPDATE game.players SET level = 1 WHERE level IS NULL"
down = { irreversible = true }
`

	m, err := ParseMigration(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if m.Description != "Add game_like table and player level" {
		t.Errorf("description = %q", m.Description)
	}
	if len(m.DDLOps) != 3 {
		t.Fatalf("DDL ops = %d, want 3", len(m.DDLOps))
	}

	// Op 0: create_table
	if m.DDLOps[0].Op != "create_table" {
		t.Errorf("DDL[0].Op = %q", m.DDLOps[0].Op)
	}
	if m.DDLOps[0].Table != "game.game_like" {
		t.Errorf("DDL[0].Table = %q", m.DDLOps[0].Table)
	}
	if m.DDLOps[0].Down == nil || len(m.DDLOps[0].Down.Ops) == 0 {
		t.Error("DDL[0] has no down ops")
	} else if m.DDLOps[0].Down.Ops[0].Op != "drop_table" {
		t.Errorf("DDL[0].Down.Ops[0].Op = %q", m.DDLOps[0].Down.Ops[0].Op)
	}

	// Op 1: add_column
	if m.DDLOps[1].Op != "add_column" {
		t.Errorf("DDL[1].Op = %q", m.DDLOps[1].Op)
	}
	if m.DDLOps[1].Column != "level" {
		t.Errorf("DDL[1].Column = %q", m.DDLOps[1].Column)
	}
	if !m.DDLOps[1].NotNull {
		t.Error("DDL[1].NotNull should be true")
	}

	// Op 2: create_index_concurrently
	if m.DDLOps[2].Op != "create_index_concurrently" {
		t.Errorf("DDL[2].Op = %q", m.DDLOps[2].Op)
	}

	// DML
	if len(m.DMLOps) != 1 {
		t.Fatalf("DML ops = %d, want 1", len(m.DMLOps))
	}
	if m.DMLOps[0].Op != "backfill" {
		t.Errorf("DML[0].Op = %q", m.DMLOps[0].Op)
	}
	if m.DMLOps[0].Down == nil || !m.DMLOps[0].Down.Irreversible {
		t.Error("DML[0] should have irreversible down")
	}
}

func TestGenerateMigration_ViewAdded(t *testing.T) {
	desired := &model.Schema{
		Name: "app",
		Views: []model.View{
			{
				Name:   "active_users",
				Schema: "app",
				Query:  "SELECT id, name FROM users WHERE active = true",
			},
		},
	}

	d := &diff.SchemaDiff{
		ViewsAdded: []string{"app.active_users"},
	}

	m, _ := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_view" && op.Name == "app.active_users" {
			found = true
			if op.ViewDef == nil {
				t.Error("create_view op has no ViewDef")
			} else if op.ViewDef.Query != "SELECT id, name FROM users WHERE active = true" {
				t.Errorf("ViewDef.Query = %q, unexpected", op.ViewDef.Query)
			}
			if op.Down == nil {
				t.Error("create_view op has no down op")
			} else if len(op.Down.Ops) == 0 {
				t.Error("create_view down has no ops")
			} else if op.Down.Ops[0].Op != "drop_view" {
				t.Errorf("create_view down op = %q, want drop_view", op.Down.Ops[0].Op)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected create_view op for app.active_users, got ops: %v", opsDebug(m.DDLOps))
	}
}

func TestGenerateMigration_ViewRemoved(t *testing.T) {
	desired := &model.Schema{Name: "app"}
	d := &diff.SchemaDiff{
		ViewsRemoved: []string{"app.old_view"},
	}

	m, _ := GenerateMigration(d, desired, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_view" && op.Name == "app.old_view" {
			found = true
			if op.Down == nil || !op.Down.Irreversible {
				t.Error("drop_view should have irreversible down")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected drop_view op for app.old_view, got ops: %v", opsDebug(m.DDLOps))
	}
}

func TestGenerateMigration_ViewQueryChanged(t *testing.T) {
	desired := &model.Schema{
		Name: "app",
		Views: []model.View{
			{
				Name:   "active_users",
				Schema: "app",
				Query:  "SELECT id, name, email FROM users WHERE active = true",
			},
		},
	}

	d := &diff.SchemaDiff{
		ViewsChanged: []diff.ViewDiff{
			{
				Name:         "app.active_users",
				QueryChanged: &[2]string{"SELECT id, name FROM users WHERE active = true", "SELECT id, name, email FROM users WHERE active = true"},
			},
		},
	}

	m, _ := GenerateMigration(d, desired, "0.3.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_or_replace_view" && op.Name == "app.active_users" {
			found = true
			if op.ViewDef == nil {
				t.Error("create_or_replace_view op has no ViewDef")
			}
			if op.Down == nil {
				t.Error("create_or_replace_view op has no down op")
			} else if len(op.Down.Ops) == 0 {
				t.Error("create_or_replace_view down has no ops")
			} else {
				downOp := op.Down.Ops[0]
				if downOp.Op != "create_or_replace_view" {
					t.Errorf("down op = %q, want create_or_replace_view", downOp.Op)
				}
				if downOp.ViewDef == nil {
					t.Error("down op has no ViewDef")
				} else if downOp.ViewDef.Query != "SELECT id, name FROM users WHERE active = true" {
					t.Errorf("down ViewDef.Query = %q, want old query", downOp.ViewDef.Query)
				}
			}
			break
		}
	}
	if !found {
		t.Errorf("expected create_or_replace_view op for app.active_users, got ops: %v", opsDebug(m.DDLOps))
	}
}

// --- Integration tests (require local PostgreSQL) ---

func testManager(t *testing.T) *testdb.Manager {
	t.Helper()
	dbURL := os.Getenv("PGDESIGN_DB")
	if dbURL == "" {
		dbURL = "postgres://localhost:5432/pgdesign?sslmode=disable"
	}
	mgr, err := testdb.NewManager(dbURL)
	if err != nil {
		t.Fatalf("create testdb manager: %v", err)
	}
	return mgr
}

func setupEphemeralDB(t *testing.T) *testdb.EphemeralDB {
	t.Helper()
	mgr := testManager(t)
	return mgr.SetupForTest(t, testdb.CreateOptions{})
}

func TestIntegration_StateTracking(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}

	// Ensure table.
	if err := EnsureMigrationsTable(ctx, conn); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	// Record a migration.
	if err := RecordMigration(ctx, conn, "0.1.0", "abc123", "Initial migration"); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Query applied versions.
	versions, err := AppliedVersions(ctx, conn)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(versions) != 1 || versions[0] != "0.1.0" {
		t.Errorf("versions = %v, want [0.1.0]", versions)
	}

	// Record another.
	if err := RecordMigration(ctx, conn, "0.2.0", "def456", "Second migration"); err != nil {
		t.Fatalf("record: %v", err)
	}
	versions, err = AppliedVersions(ctx, conn)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions count = %d, want 2", len(versions))
	}
	if versions[0] != "0.1.0" || versions[1] != "0.2.0" {
		t.Errorf("versions = %v, want [0.1.0, 0.2.0]", versions)
	}

	// Remove a migration.
	if err := RemoveMigration(ctx, conn, "0.2.0"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	versions, err = AppliedVersions(ctx, conn)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(versions) != 1 || versions[0] != "0.1.0" {
		t.Errorf("versions = %v, want [0.1.0]", versions)
	}
}

func TestIntegration_AdvisoryLock(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}

	acquired, err := AcquireAdvisoryLock(ctx, conn)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !acquired {
		t.Error("expected lock to be acquired")
	}

	// Release.
	if err := ReleaseAdvisoryLock(ctx, conn); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestIntegration_ApplyAndRollback(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}

	// Create a migrations directory with one migration.
	dir := t.TempDir()
	migration := `description = "Create test table"

[[ddl]]
op = "create_table"
table = "public.pgdesign_test_table"
down = { op = "drop_table", table = "public.pgdesign_test_table" }
`
	if err := os.WriteFile(filepath.Join(dir, "0.1.0.toml"), []byte(migration), 0o644); err != nil {
		t.Fatal(err)
	}

	// Apply.
	applied, err := Apply(ctx, conn, dir, "")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied) != 1 || applied[0] != "0.1.0" {
		t.Errorf("applied = %v, want [0.1.0]", applied)
	}

	// Verify table exists (the create_table op without TableDef just does
	// CREATE TABLE schema.name () which creates an empty table).
	var exists bool
	err = conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'pgdesign_test_table')").Scan(&exists)
	if err != nil {
		t.Fatalf("check table: %v", err)
	}
	if !exists {
		t.Error("expected pgdesign_test_table to exist after apply")
	}

	// Rollback.
	rolledBack, err := Rollback(ctx, conn, dir, "")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolledBack != "0.1.0" {
		t.Errorf("rolled back = %q, want 0.1.0", rolledBack)
	}

	// Verify table gone.
	err = conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'pgdesign_test_table')").Scan(&exists)
	if err != nil {
		t.Fatalf("check table: %v", err)
	}
	if exists {
		t.Error("expected pgdesign_test_table to be gone after rollback")
	}
}

func TestIntegration_ApplyIdempotent(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}

	dir := t.TempDir()
	migration := `description = "Create test table 2"

[[ddl]]
op = "create_table"
table = "public.pgdesign_test_table2"
down = { op = "drop_table", table = "public.pgdesign_test_table2" }
`
	if err := os.WriteFile(filepath.Join(dir, "0.1.0.toml"), []byte(migration), 0o644); err != nil {
		t.Fatal(err)
	}

	// Apply twice.
	applied1, err := Apply(ctx, conn, dir, "")
	if err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	if len(applied1) != 1 {
		t.Errorf("apply 1: applied = %v, want [0.1.0]", applied1)
	}

	applied2, err := Apply(ctx, conn, dir, "")
	if err != nil {
		t.Fatalf("apply 2: %v", err)
	}
	if len(applied2) != 0 {
		t.Errorf("apply 2: applied = %v, want []", applied2)
	}
}

func TestAppendOnlyMigration(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{
				Name:       "events",
				Schema:     "app",
				Columns:    []model.Column{{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true}},
				PK:         []string{"id"},
				AppendOnly: true,
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name:              "app.events",
				AppendOnlyChanged: &[2]bool{false, true},
			},
		},
	}

	m, diags := GenerateMigration(d, desired, "001", nil, 0, 0, extregistry.NewBuiltinRegistry())
	_ = diags

	// Should have create_function and create_trigger ops.
	foundFunc := false
	foundTrigger := false
	for _, op := range m.DDLOps {
		if op.Op == "create_function" && op.Name == "pgdesign_deny_mutation" {
			foundFunc = true
		}
		if op.Op == "create_trigger" && op.Name == "deny_mutation" {
			foundTrigger = true
		}
	}
	if !foundFunc {
		t.Error("expected create_function op for pgdesign_deny_mutation")
	}
	if !foundTrigger {
		t.Error("expected create_trigger op for deny_mutation")
	}

	// Test SQL generation for the ops.
	for _, op := range m.DDLOps {
		sqlStr := OpToSQL(op)
		if sqlStr == "" {
			t.Errorf("OpToSQL returned empty for op %q", op.Op)
		}
		if strings.HasPrefix(sqlStr, "-- unknown op:") {
			t.Errorf("OpToSQL returned unknown for op %q: %s", op.Op, sqlStr)
		}
	}
}

func TestGenerateMigration_LargeTableEscalation(t *testing.T) {
	// set_not_null on a table with >1M rows should escalate from Caution to
	// Dangerous (Error severity) via applyTableSizeEscalation.
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "players",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
				},
			},
		},
	}

	// NullableChanged: [old_not_null, new_not_null].
	// {false, true} = was nullable, becoming NOT NULL = set_not_null.
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsChanged: []diff.ColumnChange{
					{Name: "name", NullableChanged: &[2]bool{false, true}},
				},
			},
		},
	}

	stats := TableStats{"players": 2_000_000}
	_, diags := GenerateMigration(d, desired, "0.7.0", stats, 0, 0, extregistry.NewBuiltinRegistry())

	hasDangerous := false
	for _, diag := range diags {
		if diag.Code == "MIGRATE_RISK" && diag.Severity == diagnostic.Error &&
			strings.Contains(diag.Message, "set_not_null") {
			hasDangerous = true
			break
		}
	}
	if !hasDangerous {
		t.Error("expected set_not_null on table with >1M rows to escalate to Dangerous (Error)")
	}

	// drop_not_null (becoming nullable) is Safe and should NOT escalate even
	// with large tables.
	d2 := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsChanged: []diff.ColumnChange{
					{Name: "name", NullableChanged: &[2]bool{true, false}},
				},
			},
		},
	}

	_, diags2 := GenerateMigration(d2, desired, "0.7.1", stats, 0, 0, extregistry.NewBuiltinRegistry())

	for _, diag := range diags2 {
		if diag.Code == "MIGRATE_RISK" && diag.Severity == diagnostic.Error &&
			strings.Contains(diag.Message, "drop_not_null") {
			t.Error("drop_not_null should be Safe, not escalated to Dangerous")
		}
	}
}

func TestGenerateMigration_LargeTableFK_Split(t *testing.T) {
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "scores",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "player_id", PGType: typeinfo.T("int8"), NotNull: true},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.scores",
				FKsAdded: []model.FK{
					{
						Name:       "fk_scores_player",
						Columns:    []string{"player_id"},
						RefTable:   "players",
						RefColumns: []string{"id"},
					},
				},
			},
		},
	}

	stats := TableStats{"scores": 50_000}
	m, diags := GenerateMigration(d, desired, "0.8.0", stats, 10_000, 0, extregistry.NewBuiltinRegistry())

	// No E300 diagnostic should be emitted -- the split replaces the warning.
	for _, diag := range diags {
		if diag.Code == "E300" {
			t.Error("unexpected E300 diagnostic; large-table FK should be auto-split, not warned")
		}
	}

	// Should produce exactly two ops: add_fk_not_valid + validate_constraint.
	if len(m.DDLOps) != 2 {
		t.Fatalf("expected 2 DDL ops, got %d: %v", len(m.DDLOps), opsString(m.DDLOps))
	}

	if m.DDLOps[0].Op != "add_fk_not_valid" {
		t.Errorf("DDLOps[0]: got op %q, want %q", m.DDLOps[0].Op, "add_fk_not_valid")
	}
	if m.DDLOps[0].Table != "game.scores" {
		t.Errorf("DDLOps[0]: got table %q, want %q", m.DDLOps[0].Table, "game.scores")
	}
	if m.DDLOps[0].Name != "fk_scores_player" {
		t.Errorf("DDLOps[0]: got name %q, want %q", m.DDLOps[0].Name, "fk_scores_player")
	}

	if m.DDLOps[1].Op != "validate_constraint" {
		t.Errorf("DDLOps[1]: got op %q, want %q", m.DDLOps[1].Op, "validate_constraint")
	}
	if m.DDLOps[1].Table != "game.scores" {
		t.Errorf("DDLOps[1]: got table %q, want %q", m.DDLOps[1].Table, "game.scores")
	}
	if m.DDLOps[1].Name != "fk_scores_player" {
		t.Errorf("DDLOps[1]: got name %q, want %q", m.DDLOps[1].Name, "fk_scores_player")
	}

	// Verify SQL generation.
	sql0 := OpToSQL(m.DDLOps[0])
	if !strings.Contains(sql0, "NOT VALID") {
		t.Errorf("add_fk_not_valid SQL should contain NOT VALID, got: %s", sql0)
	}
	sql1 := OpToSQL(m.DDLOps[1])
	if !strings.Contains(sql1, "VALIDATE CONSTRAINT") {
		t.Errorf("validate_constraint SQL should contain VALIDATE CONSTRAINT, got: %s", sql1)
	}
}

func TestGenerateMigration_SmallTableFK_NoSplit(t *testing.T) {
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "scores",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "player_id", PGType: typeinfo.T("int8"), NotNull: true},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.scores",
				FKsAdded: []model.FK{
					{
						Name:       "fk_scores_player",
						Columns:    []string{"player_id"},
						RefTable:   "players",
						RefColumns: []string{"id"},
					},
				},
			},
		},
	}

	// 5000 rows, below the 10_000 threshold -- no split.
	stats := TableStats{"scores": 5_000}
	m, _ := GenerateMigration(d, desired, "0.8.0", stats, 10_000, 0, extregistry.NewBuiltinRegistry())

	// Should produce exactly one op: regular add_fk.
	if len(m.DDLOps) != 1 {
		t.Fatalf("expected 1 DDL op, got %d: %v", len(m.DDLOps), opsString(m.DDLOps))
	}

	if m.DDLOps[0].Op != "add_fk" {
		t.Errorf("DDLOps[0]: got op %q, want %q", m.DDLOps[0].Op, "add_fk")
	}

	// Verify SQL does NOT contain NOT VALID.
	sql0 := OpToSQL(m.DDLOps[0])
	if strings.Contains(sql0, "NOT VALID") {
		t.Errorf("small-table FK SQL should not contain NOT VALID, got: %s", sql0)
	}
}

func TestGenerateMigration_NoStats_NoSplit_NoEscalation(t *testing.T) {
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "scores",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "player_id", PGType: typeinfo.T("int8"), NotNull: true},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.scores",
				FKsAdded: []model.FK{
					{
						Name:       "fk_scores_player",
						Columns:    []string{"player_id"},
						RefTable:   "players",
						RefColumns: []string{"id"},
					},
				},
				ColumnsChanged: []diff.ColumnChange{
					{Name: "player_id", NullableChanged: &[2]bool{true, true}},
				},
			},
		},
	}

	// nil stats: no E300, no escalation.
	_, diags := GenerateMigration(d, desired, "0.9.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

	for _, diag := range diags {
		if diag.Code == "E300" {
			t.Error("unexpected E300 when stats are nil")
		}
		// set_not_null is Caution but should NOT escalate without EstimatedRows.
		if diag.Code == "MIGRATE_RISK" && diag.Severity == diagnostic.Error &&
			strings.Contains(diag.Message, "set_not_null") {
			t.Error("unexpected escalation to Error when stats are nil")
		}
	}
}

func TestGenerateMigration_ExpandContract_SetNotNull_LargeTable(t *testing.T) {
	// set_not_null on a table with >10M rows should produce a DML backfill op
	// followed by the set_not_null DDL op.
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "players",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true, Default: model.StrPtr("'unknown'")},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsChanged: []diff.ColumnChange{
					{Name: "name", NullableChanged: &[2]bool{false, true}},
				},
			},
		},
	}

	stats := TableStats{"players": 15_000_000}
	m, _ := GenerateMigration(d, desired, "1.0.0", stats, 0, 0, extregistry.NewBuiltinRegistry())

	// Should have a backfill DML op.
	if len(m.DMLOps) != 1 {
		t.Fatalf("DML ops count = %d, want 1", len(m.DMLOps))
	}
	if m.DMLOps[0].Op != "backfill" {
		t.Errorf("DML[0].Op = %q, want backfill", m.DMLOps[0].Op)
	}
	if !strings.Contains(m.DMLOps[0].SQL, "COALESCE") {
		t.Errorf("backfill SQL should use COALESCE, got: %s", m.DMLOps[0].SQL)
	}
	if !strings.Contains(m.DMLOps[0].SQL, "IS NULL") {
		t.Errorf("backfill SQL should contain IS NULL, got: %s", m.DMLOps[0].SQL)
	}
	if m.DMLOps[0].Down == nil || !m.DMLOps[0].Down.Irreversible {
		t.Error("backfill DML should have irreversible down")
	}

	// Should still have the set_not_null DDL op.
	hasSetNotNull := false
	for _, op := range m.DDLOps {
		if op.Op == "set_not_null" && op.Table == "game.players" && op.Column == "name" {
			hasSetNotNull = true
			break
		}
	}
	if !hasSetNotNull {
		t.Error("expected set_not_null DDL op to still be present")
	}
}

func TestGenerateMigration_ExpandContract_SetNotNull_SmallTable(t *testing.T) {
	// set_not_null on a table with <10M rows should NOT produce a DML backfill op.
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "players",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true, Default: model.StrPtr("'unknown'")},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsChanged: []diff.ColumnChange{
					{Name: "name", NullableChanged: &[2]bool{false, true}},
				},
			},
		},
	}

	stats := TableStats{"players": 5_000_000}
	m, _ := GenerateMigration(d, desired, "1.1.0", stats, 0, 0, extregistry.NewBuiltinRegistry())

	// Should have NO DML ops.
	if len(m.DMLOps) != 0 {
		t.Errorf("DML ops count = %d, want 0 (table has <10M rows)", len(m.DMLOps))
	}

	// Should still have the set_not_null DDL op.
	hasSetNotNull := false
	for _, op := range m.DDLOps {
		if op.Op == "set_not_null" && op.Column == "name" {
			hasSetNotNull = true
			break
		}
	}
	if !hasSetNotNull {
		t.Error("expected set_not_null DDL op")
	}
}

func TestGenerateMigration_ExpandContract_TypeNarrow_LargeTable(t *testing.T) {
	// Type narrowing (e.g., bigint -> integer) on a large table should emit
	// an EXPAND_CONTRACT_TYPE_NARROW warning diagnostic.
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "players",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsChanged: []diff.ColumnChange{
					{Name: "id", TypeChanged: &[2]string{"bigint", "integer"}},
				},
			},
		},
	}

	stats := TableStats{"players": 15_000_000}
	_, diags := GenerateMigration(d, desired, "1.2.0", stats, 0, 0, extregistry.NewBuiltinRegistry())

	hasWarning := false
	for _, diag := range diags {
		if diag.Code == "EXPAND_CONTRACT_TYPE_NARROW" {
			hasWarning = true
			if diag.Severity != diagnostic.Warning {
				t.Errorf("expected Warning severity, got %v", diag.Severity)
			}
			if !strings.Contains(diag.Message, "bigint") || !strings.Contains(diag.Message, "integer") {
				t.Errorf("warning message should mention old and new types, got: %s", diag.Message)
			}
			if !strings.Contains(diag.Message, "expand-contract") {
				t.Errorf("warning message should mention expand-contract, got: %s", diag.Message)
			}
			break
		}
	}
	if !hasWarning {
		t.Error("expected EXPAND_CONTRACT_TYPE_NARROW diagnostic for type narrowing on large table")
	}
}

func TestGenerateMigration_ArrayChanged_ScalarToArray(t *testing.T) {
	desired := &model.Schema{
		Name:      "app",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "posts",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "tags", PGType: typeinfo.T("text"), NotNull: true, Array: true},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "app.posts",
				ColumnsChanged: []diff.ColumnChange{
					{Name: "tags", ArrayChanged: &[2]bool{false, true}},
				},
			},
		},
	}

	mig, _ := GenerateMigration(d, desired, "1.0.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

	var found bool
	for _, op := range mig.DDLOps {
		if op.Op == "alter_column_type" && op.Table == "app.posts" && op.Column == "tags" {
			found = true
			if op.Type != "text[]" {
				t.Errorf("expected target type text[], got %s", op.Type)
			}
			if op.Down == nil || !op.Down.Irreversible {
				t.Error("expected irreversible down op for alter_column_type")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected alter_column_type op for ArrayChanged, got ops: %s", opsDebug(mig.DDLOps))
	}
}

func TestGenerateMigration_ArrayChanged_ArrayToScalar(t *testing.T) {
	desired := &model.Schema{
		Name:      "app",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "posts",
				Schema: "app",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int4"), NotNull: true},
					{Name: "tags", PGType: typeinfo.T("text"), NotNull: true, Array: false},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "app.posts",
				ColumnsChanged: []diff.ColumnChange{
					{Name: "tags", ArrayChanged: &[2]bool{true, false}},
				},
			},
		},
	}

	mig, _ := GenerateMigration(d, desired, "1.0.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

	var found bool
	for _, op := range mig.DDLOps {
		if op.Op == "alter_column_type" && op.Table == "app.posts" && op.Column == "tags" {
			found = true
			if op.Type != "text" {
				t.Errorf("expected target type text, got %s", op.Type)
			}
			if op.Down == nil || !op.Down.Irreversible {
				t.Error("expected irreversible down op for alter_column_type")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected alter_column_type op for ArrayChanged, got ops: %s", opsDebug(mig.DDLOps))
	}
}

func TestGenerateMigration_IndexWithChange(t *testing.T) {
	// Btree with-only change should produce a single alter_index_set, not DROP+CREATE.
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{{
			Name: "t",
			IndexesChanged: []diff.IndexChange{{
				Name: "idx_t",
				Old: model.Index{
					Name:    "idx_t",
					Columns: []string{"id"},
					Method:  "btree",
					With:    map[string]string{"fillfactor": "90"},
				},
				New: model.Index{
					Name:    "idx_t",
					Columns: []string{"id"},
					Method:  "btree",
					With:    map[string]string{"fillfactor": "80"},
				},
			}},
		}},
	}
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:   "t",
			Schema: "public",
		}},
	}
	m, _ := GenerateMigration(d, desired, "001", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if len(m.DDLOps) != 1 {
		t.Fatalf("expected exactly 1 DDL op (alter_index_set), got %d: %s", len(m.DDLOps), opsDebug(m.DDLOps))
	}
	op := m.DDLOps[0]
	if op.Op != "alter_index_set" {
		t.Errorf("expected op alter_index_set, got %q", op.Op)
	}
	if op.Name != "idx_t" {
		t.Errorf("expected name idx_t, got %q", op.Name)
	}
	if op.Table != "t" {
		t.Errorf("expected table t, got %q", op.Table)
	}
	if op.With == nil || op.With["fillfactor"] != "80" {
		t.Errorf("expected With fillfactor=80, got %v", op.With)
	}
	if op.Down == nil {
		t.Fatal("expected Down op for alter_index_set")
	}
	if len(op.Down.Ops) != 1 {
		t.Fatalf("expected 1 down op, got %d", len(op.Down.Ops))
	}
	downOp := op.Down.Ops[0]
	if downOp.Op != "alter_index_set" {
		t.Errorf("expected down op alter_index_set, got %q", downOp.Op)
	}
	if downOp.With == nil || downOp.With["fillfactor"] != "90" {
		t.Errorf("expected down With fillfactor=90 (old value), got %v", downOp.With)
	}
}

func TestGenerateMigration_IndexWithChange_ExtensionMethod(t *testing.T) {
	// Extension methods (hnsw) require DROP+CREATE even for with-only changes,
	// because ALTER INDEX SET does not work for extension-defined parameters.
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{{
			Name: "t",
			IndexesChanged: []diff.IndexChange{{
				Name: "idx_t_embedding",
				Old: model.Index{
					Name:    "idx_t_embedding",
					Columns: []string{"embedding"},
					Method:  "hnsw",
					With:    map[string]string{"m": "16", "ef_construction": "200"},
				},
				New: model.Index{
					Name:    "idx_t_embedding",
					Columns: []string{"embedding"},
					Method:  "hnsw",
					With:    map[string]string{"m": "32", "ef_construction": "200"},
				},
			}},
		}},
	}
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:   "t",
			Schema: "public",
		}},
	}
	m, _ := GenerateMigration(d, desired, "001", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if len(m.DDLOps) != 2 {
		t.Fatalf("expected 2 DDL ops (drop + create) for extension method, got %d: %s", len(m.DDLOps), opsDebug(m.DDLOps))
	}
	foundDrop := false
	foundCreate := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_index" && op.Name == "idx_t_embedding" {
			foundDrop = true
		}
		if op.Op == "create_index" && op.Name == "idx_t_embedding" {
			foundCreate = true
			if op.With == nil || op.With["m"] != "32" || op.With["ef_construction"] != "200" {
				t.Errorf("expected create_index with m=32 ef_construction=200, got %v", op.With)
			}
		}
	}
	if !foundDrop {
		t.Errorf("expected drop_index op for idx_t_embedding, got: %s", opsDebug(m.DDLOps))
	}
	if !foundCreate {
		t.Errorf("expected create_index op for idx_t_embedding, got: %s", opsDebug(m.DDLOps))
	}
}

func TestGenerateMigration_IndexWithChange_ColumnsAlsoChanged(t *testing.T) {
	// When columns change alongside With, DROP+CREATE is required even for builtin methods.
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{{
			Name: "t",
			IndexesChanged: []diff.IndexChange{{
				Name: "idx_t",
				Old: model.Index{
					Name:    "idx_t",
					Columns: []string{"id"},
					Method:  "btree",
					With:    map[string]string{"fillfactor": "90"},
				},
				New: model.Index{
					Name:    "idx_t",
					Columns: []string{"id", "name"},
					Method:  "btree",
					With:    map[string]string{"fillfactor": "70"},
				},
			}},
		}},
	}
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:   "t",
			Schema: "public",
		}},
	}
	m, _ := GenerateMigration(d, desired, "001", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if len(m.DDLOps) != 2 {
		t.Fatalf("expected 2 DDL ops (drop + create) for columns+with change, got %d: %s", len(m.DDLOps), opsDebug(m.DDLOps))
	}
	foundDrop := false
	foundCreate := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_index" && op.Name == "idx_t" {
			foundDrop = true
		}
		if op.Op == "create_index" && op.Name == "idx_t" {
			foundCreate = true
			if len(op.Columns) != 2 || op.Columns[0] != "id" || op.Columns[1] != "name" {
				t.Errorf("expected create_index columns [id, name], got %v", op.Columns)
			}
			if op.With == nil || op.With["fillfactor"] != "70" {
				t.Errorf("expected create_index with fillfactor=70, got %v", op.With)
			}
		}
	}
	if !foundDrop {
		t.Errorf("expected drop_index op for idx_t, got: %s", opsDebug(m.DDLOps))
	}
	if !foundCreate {
		t.Errorf("expected create_index op for idx_t, got: %s", opsDebug(m.DDLOps))
	}
}

func TestOpToSQL_CreateIndexWithParams(t *testing.T) {
	op := DDLOp{
		Op:      "create_index",
		Table:   "public.t",
		Name:    "idx_t",
		Columns: []string{"embedding"},
		Method:  "hnsw",
		With:    map[string]string{"m": "16", "ef_construction": "200"},
	}
	got := OpToSQL(op)
	if !strings.Contains(got, "WITH (ef_construction = 200, m = 16)") {
		t.Errorf("expected WITH clause in SQL, got: %s", got)
	}
}

func TestOpToSQL_AlterIndexSet(t *testing.T) {
	op := DDLOp{
		Op:    "alter_index_set",
		Table: "public.t",
		Name:  "idx_t",
		With:  map[string]string{"fillfactor": "80"},
	}
	got := OpToSQL(op)
	if !strings.Contains(got, "ALTER INDEX") || !strings.Contains(got, "SET (fillfactor = 80)") {
		t.Errorf("expected ALTER INDEX SET SQL, got: %s", got)
	}
}

func TestParseMigration_WithParams(t *testing.T) {
	m := &Migration{
		Description: "test with params",
		DDLOps: []DDLOp{{
			Op:      "create_index",
			Table:   "t",
			Name:    "idx_t",
			Columns: []string{"embedding"},
			Method:  "hnsw",
			With:    map[string]string{"m": "16", "ef_construction": "200"},
		}},
	}
	toml := FormatMigration(m)
	parsed, err := ParseMigration(toml)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(parsed.DDLOps) != 1 {
		t.Fatalf("expected 1 DDL op, got %d", len(parsed.DDLOps))
	}
	op := parsed.DDLOps[0]
	if op.With == nil || op.With["m"] != "16" || op.With["ef_construction"] != "200" {
		t.Errorf("expected with params m=16, ef_construction=200, got %v", op.With)
	}
}

func TestGenerateMigration_MaterializedViewAdded(t *testing.T) {
	desired := &model.Schema{
		Name: "app",
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "app",
				Query:    "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
				WithData: true,
			},
		},
	}
	d := &diff.SchemaDiff{
		MaterializedViewsAdded: []string{"app.monthly_stats"},
	}
	m, _ := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}
	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_materialized_view" && op.Name == "app.monthly_stats" {
			found = true
			if op.MaterializedViewDef == nil {
				t.Error("create_materialized_view op has no MaterializedViewDef")
			} else if op.MaterializedViewDef.Query != "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1" {
				t.Errorf("MaterializedViewDef.Query = %q, want the monthly_stats query", op.MaterializedViewDef.Query)
			}
			if op.Down == nil {
				t.Error("create_materialized_view op has no down op")
			} else if len(op.Down.Ops) == 0 {
				t.Error("create_materialized_view down has no ops")
			} else if op.Down.Ops[0].Op != "drop_materialized_view" {
				t.Errorf("create_materialized_view down op = %q, want drop_materialized_view", op.Down.Ops[0].Op)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected create_materialized_view op for app.monthly_stats, got ops: %v", opsDebug(m.DDLOps))
	}
}

func TestGenerateMigration_MaterializedViewQueryChanged(t *testing.T) {
	desired := &model.Schema{
		Name: "app",
		MaterializedViews: []model.MaterializedView{
			{
				Name:     "monthly_stats",
				Schema:   "app",
				Query:    "SELECT date_trunc('month', created_at) AS month, sum(total) FROM orders GROUP BY 1",
				WithData: true,
			},
		},
	}
	d := &diff.SchemaDiff{
		MaterializedViewsChanged: []diff.MaterializedViewDiff{
			{
				Name: "app.monthly_stats",
				QueryChanged: &[2]string{
					"SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
					"SELECT date_trunc('month', created_at) AS month, sum(total) FROM orders GROUP BY 1",
				},
			},
		},
	}
	m, _ := GenerateMigration(d, desired, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}
	dropIdx := -1
	createIdx := -1
	for i, op := range m.DDLOps {
		if op.Op == "drop_materialized_view" && op.Name == "app.monthly_stats" {
			dropIdx = i
		}
		if op.Op == "create_materialized_view" && op.Name == "app.monthly_stats" {
			createIdx = i
		}
	}
	if dropIdx == -1 {
		t.Errorf("expected drop_materialized_view op for app.monthly_stats, got ops: %v", opsDebug(m.DDLOps))
	}
	if createIdx == -1 {
		t.Errorf("expected create_materialized_view op for app.monthly_stats, got ops: %v", opsDebug(m.DDLOps))
	}
	if dropIdx != -1 && createIdx != -1 && dropIdx >= createIdx {
		t.Errorf("drop_materialized_view (index %d) should appear before create_materialized_view (index %d)", dropIdx, createIdx)
	}
	if createIdx != -1 {
		createOp := m.DDLOps[createIdx]
		if createOp.MaterializedViewDef == nil {
			t.Error("create_materialized_view op has no MaterializedViewDef")
		}
		if createOp.Down == nil {
			t.Error("create_materialized_view op has no down op")
		} else if len(createOp.Down.Ops) == 0 {
			t.Error("create_materialized_view down has no ops")
		} else if createOp.Down.Ops[0].Op != "drop_materialized_view" {
			t.Errorf("create_materialized_view down op = %q, want drop_materialized_view", createOp.Down.Ops[0].Op)
		}
	}
}

func TestGenerateMigration_MaterializedViewRemoved(t *testing.T) {
	desired := &model.Schema{
		Name: "app",
	}
	d := &diff.SchemaDiff{
		MaterializedViewsRemoved: []string{"app.monthly_stats"},
	}
	m, _ := GenerateMigration(d, desired, "0.3.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}
	found := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_materialized_view" && op.Name == "app.monthly_stats" {
			found = true
			if op.Down == nil {
				t.Error("drop_materialized_view op has no down op")
			} else if !op.Down.Irreversible {
				t.Error("drop_materialized_view down should be irreversible")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected drop_materialized_view op for app.monthly_stats, got ops: %v", opsDebug(m.DDLOps))
	}
}

func TestOpToSQL_AddColumnGenerated(t *testing.T) {
	// PG 18 + stored=true -> STORED
	op := DDLOp{
		Op:        "add_column",
		Table:     "public.orders",
		Column:    "total",
		Type:      "integer",
		Generated: "price * quantity",
		Stored:    true,
		PGVersion: 18,
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.orders ADD COLUMN total integer GENERATED ALWAYS AS (price * quantity) STORED;`
	if got != want {
		t.Errorf("OpToSQL add_column stored:\ngot:  %s\nwant: %s", got, want)
	}

	// PG 18 + stored=false -> VIRTUAL
	op.Stored = false
	got = OpToSQL(op)
	if !strings.Contains(got, "VIRTUAL") {
		t.Errorf("PG18 + stored=false: expected VIRTUAL in: %s", got)
	}
	if strings.Contains(got, "STORED") {
		t.Errorf("PG18 + stored=false: unexpected STORED in: %s", got)
	}

	// PG 17 + stored=false -> defensively STORED
	op.PGVersion = 17
	got = OpToSQL(op)
	if !strings.Contains(got, "STORED") {
		t.Errorf("PG17 + stored=false: expected STORED in: %s", got)
	}

	// PG 0 (unknown) + stored=false -> STORED (conservative: pgcap.Has returns false)
	op.PGVersion = 0
	got = OpToSQL(op)
	if !strings.Contains(got, "STORED") {
		t.Errorf("PG0 + stored=false: expected STORED (conservative) in: %s", got)
	}
}

func TestMigrationRoundTrip_GeneratedColumn(t *testing.T) {
	m := &Migration{
		Description: "add generated column",
		DDLOps: []DDLOp{
			{
				Op:        "add_column",
				Table:     "public.orders",
				Column:    "total",
				Type:      "integer",
				Generated: "price * quantity",
				Stored:    true,
				PGVersion: 18,
			},
		},
	}

	// Serialize to TOML.
	content := FormatMigration(m)

	// Verify the TOML content contains the expected fields.
	if !strings.Contains(content, `generated = "price * quantity"`) {
		t.Errorf("serialized TOML missing generated field:\n%s", content)
	}
	if !strings.Contains(content, "stored = true") {
		t.Errorf("serialized TOML missing stored field:\n%s", content)
	}
	if !strings.Contains(content, "pg_version = 18") {
		t.Errorf("serialized TOML missing pg_version field:\n%s", content)
	}

	// Parse back.
	parsed, err := ParseMigration(content)
	if err != nil {
		t.Fatalf("parse round-trip failed: %v", err)
	}

	if len(parsed.DDLOps) != 1 {
		t.Fatalf("expected 1 DDL op, got %d", len(parsed.DDLOps))
	}

	op := parsed.DDLOps[0]
	if op.Generated != "price * quantity" {
		t.Errorf("round-trip Generated = %q, want %q", op.Generated, "price * quantity")
	}
	if !op.Stored {
		t.Error("round-trip Stored = false, want true")
	}
	if op.PGVersion != 18 {
		t.Errorf("round-trip PGVersion = %d, want 18", op.PGVersion)
	}
}

func TestMigrationRoundTrip_VirtualGeneratedColumn(t *testing.T) {
	m := &Migration{
		Description: "add virtual generated column",
		DDLOps: []DDLOp{
			{
				Op:        "add_column",
				Table:     "public.orders",
				Column:    "total",
				Type:      "integer",
				Generated: "price * quantity",
				Stored:    false,
				PGVersion: 18,
			},
		},
	}

	// Serialize to TOML.
	content := FormatMigration(m)

	// Verify the TOML content contains stored = false (not omitted).
	if !strings.Contains(content, "stored = false") {
		t.Errorf("serialized TOML missing stored = false:\n%s", content)
	}
	if !strings.Contains(content, `generated = "price * quantity"`) {
		t.Errorf("serialized TOML missing generated field:\n%s", content)
	}

	// Parse back and verify Stored survives the round-trip as false.
	parsed, err := ParseMigration(content)
	if err != nil {
		t.Fatalf("parse round-trip failed: %v", err)
	}

	if len(parsed.DDLOps) != 1 {
		t.Fatalf("expected 1 DDL op, got %d", len(parsed.DDLOps))
	}

	op := parsed.DDLOps[0]
	if op.Generated != "price * quantity" {
		t.Errorf("round-trip Generated = %q, want %q", op.Generated, "price * quantity")
	}
	if op.Stored {
		t.Error("round-trip Stored = true, want false (virtual column)")
	}
	if op.PGVersion != 18 {
		t.Errorf("round-trip PGVersion = %d, want 18", op.PGVersion)
	}
}

func TestGeneratedStorageKeyword(t *testing.T) {
	tests := []struct {
		stored    bool
		pgVersion int
		want      string
	}{
		{true, 0, "STORED"},
		{true, 17, "STORED"},
		{true, 18, "STORED"},
		{false, 18, "VIRTUAL"},
		{false, 19, "VIRTUAL"},
		{false, 17, "STORED"}, // pre-PG18 defensive
		{false, 12, "STORED"}, // pre-PG18 defensive
		{false, 0, "STORED"},  // unknown version, conservative (pgcap.Has returns false)
	}

	for _, tt := range tests {
		got := sql.GeneratedStorageKeyword(tt.stored, tt.pgVersion)
		if got != tt.want {
			t.Errorf("GeneratedStorageKeyword(stored=%v, pg=%d) = %q, want %q",
				tt.stored, tt.pgVersion, got, tt.want)
		}
	}
}

func intPtr(v int) *int { return &v }

func TestGenerateMigration_CollationChange(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:   "messages",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "content", PGType: typeinfo.T("text"), Collation: "de_DE"},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "public.messages",
				ColumnsChanged: []diff.ColumnChange{
					{Name: "content", CollationChanged: &[2]string{"", "de_DE"}},
				},
			},
		},
	}

	mig, _ := GenerateMigration(d, desired, "1.0.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

	var found bool
	for _, op := range mig.DDLOps {
		if op.Op == "alter_column_type" && op.Table == "public.messages" && op.Column == "content" {
			found = true
			if op.Collation != "de_DE" {
				t.Errorf("Collation = %q, want %q", op.Collation, "de_DE")
			}
			if op.Type != "text" {
				t.Errorf("Type = %q, want %q", op.Type, "text")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected alter_column_type op for collation change, got ops: %s", opsDebug(mig.DDLOps))
	}
}

func TestGenerateMigration_StatisticsChange(t *testing.T) {
	v := 1000
	desired := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), Statistics: &v},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "public.users",
				ColumnsChanged: []diff.ColumnChange{
					{Name: "name", StatisticsChanged: &[2]*int{nil, &v}},
				},
			},
		},
	}

	mig, _ := GenerateMigration(d, desired, "1.0.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

	var found bool
	for _, op := range mig.DDLOps {
		if op.Op == "set_statistics" && op.Table == "public.users" && op.Column == "name" {
			found = true
			if op.Statistics == nil || *op.Statistics != 1000 {
				t.Errorf("Statistics = %v, want *1000", op.Statistics)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected set_statistics op, got ops: %s", opsDebug(mig.DDLOps))
	}
}

func TestGenerateMigration_StatisticsReset(t *testing.T) {
	v := 500
	desired := &model.Schema{
		Name: "public",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text")},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "public.users",
				ColumnsChanged: []diff.ColumnChange{
					{Name: "name", StatisticsChanged: &[2]*int{&v, nil}},
				},
			},
		},
	}

	mig, _ := GenerateMigration(d, desired, "1.0.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

	var found bool
	for _, op := range mig.DDLOps {
		if op.Op == "set_statistics" && op.Table == "public.users" && op.Column == "name" {
			found = true
			if op.Statistics != nil {
				t.Errorf("Statistics = %v, want nil (reset to default)", op.Statistics)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected set_statistics op for reset, got ops: %s", opsDebug(mig.DDLOps))
	}
}

func TestOpSetStatistics(t *testing.T) {
	v := 1000
	op := DDLOp{
		Op:         "set_statistics",
		Table:      "users",
		Column:     "name",
		Statistics: &v,
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.users ALTER COLUMN name SET STATISTICS 1000;`
	if got != want {
		t.Errorf("got:\n  %s\nwant:\n  %s", got, want)
	}
}

func TestOpSetStatisticsReset(t *testing.T) {
	op := DDLOp{
		Op:     "set_statistics",
		Table:  "users",
		Column: "name",
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.users ALTER COLUMN name SET STATISTICS -1;`
	if got != want {
		t.Errorf("got:\n  %s\nwant:\n  %s", got, want)
	}
}

func TestOpAlterColumnTypeWithCollation(t *testing.T) {
	op := DDLOp{
		Op:        "alter_column_type",
		Table:     "messages",
		Column:    "content",
		Type:      "text",
		Collation: "de_DE",
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.messages ALTER COLUMN content TYPE text COLLATE "de_DE";`
	if got != want {
		t.Errorf("got:\n  %s\nwant:\n  %s", got, want)
	}
}

func TestOpCreateIndexWithCollation(t *testing.T) {
	op := DDLOp{
		Op:         "create_index",
		Table:      "messages",
		Name:       "idx_messages_content",
		Columns:    []string{"content"},
		Collations: map[string]string{"content": "C"},
	}
	got := OpToSQL(op)
	if !strings.Contains(got, `content COLLATE "C"`) {
		t.Errorf("expected COLLATE C in index DDL, got:\n%s", got)
	}
}

func TestOpToSQL_AddExclusion(t *testing.T) {
	op := DDLOp{
		Op:        "add_exclusion",
		Table:     "bookings",
		Name:      "no_overlap",
		Method:    "gist",
		Columns:   []string{"room_id", "during"},
		Operators: []string{"=", "&&"},
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.bookings ADD CONSTRAINT no_overlap EXCLUDE USING gist (room_id WITH =, during WITH &&);`
	if got != want {
		t.Errorf("OpToSQL(add_exclusion):\ngot:  %s\nwant: %s", got, want)
	}
}

func TestOpToSQL_AddExclusionWithWhere(t *testing.T) {
	op := DDLOp{
		Op:                "add_exclusion",
		Table:             "bookings",
		Name:              "no_overlap",
		Method:            "gist",
		Columns:           []string{"room_id", "during"},
		Operators:         []string{"=", "&&"},
		Where:             "active = true",
		Deferrable:        true,
		InitiallyDeferred: true,
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.bookings ADD CONSTRAINT no_overlap EXCLUDE USING gist (room_id WITH =, during WITH &&) WHERE (active = true) DEFERRABLE INITIALLY DEFERRED;`
	if got != want {
		t.Errorf("OpToSQL(add_exclusion with where):\ngot:  %s\nwant: %s", got, want)
	}
}

func TestOpToSQL_DropExclusion(t *testing.T) {
	op := DDLOp{
		Op:    "drop_exclusion",
		Table: "bookings",
		Name:  "no_overlap",
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.bookings DROP CONSTRAINT no_overlap;`
	if got != want {
		t.Errorf("OpToSQL(drop_exclusion):\ngot:  %s\nwant: %s", got, want)
	}
}

func TestOpToSQL_AddUnique(t *testing.T) {
	op := DDLOp{
		Op:      "add_unique",
		Table:   "users",
		Name:    "uq_email",
		Columns: []string{"email"},
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.users ADD CONSTRAINT uq_email UNIQUE (email);`
	if got != want {
		t.Errorf("OpToSQL(add_unique):\ngot:  %s\nwant: %s", got, want)
	}
}

func TestOpToSQL_AddUniqueDeferrable(t *testing.T) {
	op := DDLOp{
		Op:                "add_unique",
		Table:             "users",
		Name:              "uq_email",
		Columns:           []string{"email"},
		Deferrable:        true,
		InitiallyDeferred: true,
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.users ADD CONSTRAINT uq_email UNIQUE (email) DEFERRABLE INITIALLY DEFERRED;`
	if got != want {
		t.Errorf("OpToSQL(add_unique deferrable):\ngot:  %s\nwant: %s", got, want)
	}
}

func TestOpToSQL_DropUnique(t *testing.T) {
	op := DDLOp{
		Op:    "drop_unique",
		Table: "users",
		Name:  "uq_email",
	}
	got := OpToSQL(op)
	want := `ALTER TABLE public.users DROP CONSTRAINT uq_email;`
	if got != want {
		t.Errorf("OpToSQL(drop_unique):\ngot:  %s\nwant: %s", got, want)
	}
}

func TestGenerateMigration_SequenceAdded(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Sequences: []model.Sequence{{
			Name:   "order_seq",
			Schema: "public",
			Start:  model.Int64Ptr(100),
		}},
	}

	d := &diff.SchemaDiff{
		SequencesAdded: []string{"order_seq"},
	}

	m, _ := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_sequence" && op.Name == "order_seq" {
			found = true
			if op.SequenceDef == nil {
				t.Error("create_sequence op has no SequenceDef")
			} else if op.SequenceDef.Start == nil || *op.SequenceDef.Start != 100 {
				t.Errorf("SequenceDef.Start = %v, want 100", op.SequenceDef.Start)
			}
			if op.Down == nil {
				t.Error("create_sequence op has no down op")
			} else if len(op.Down.Ops) == 0 {
				t.Error("create_sequence down has no ops")
			} else if op.Down.Ops[0].Op != "drop_sequence" {
				t.Errorf("create_sequence down op = %q, want drop_sequence", op.Down.Ops[0].Op)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected create_sequence op for order_seq, got ops: %v", opsDebug(m.DDLOps))
	}
}

func TestGenerateMigration_SequenceRemoved(t *testing.T) {
	desired := &model.Schema{Name: "public"}
	d := &diff.SchemaDiff{
		SequencesRemoved: []string{"order_seq"},
	}

	m, _ := GenerateMigration(d, desired, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_sequence" && op.Name == "order_seq" {
			found = true
			if op.Down == nil || !op.Down.Irreversible {
				t.Error("drop_sequence should have irreversible down")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected drop_sequence op for order_seq, got ops: %v", opsDebug(m.DDLOps))
	}
}

func TestGenerateMigration_SequenceChanged(t *testing.T) {
	desired := &model.Schema{
		Name: "public",
		Sequences: []model.Sequence{{
			Name:   "order_seq",
			Schema: "public",
			Start:  model.Int64Ptr(500),
			Cache:  model.Int64Ptr(20),
		}},
	}

	d := &diff.SchemaDiff{
		SequencesChanged: []diff.SequenceDiff{{
			Name:         "order_seq",
			StartChanged: &[2]*int64{model.Int64Ptr(100), model.Int64Ptr(500)},
			CacheChanged: &[2]*int64{model.Int64Ptr(10), model.Int64Ptr(20)},
		}},
	}

	m, _ := GenerateMigration(d, desired, "0.3.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "alter_sequence" && op.Name == "order_seq" {
			found = true
			if op.SequenceDef == nil {
				t.Error("alter_sequence op has no SequenceDef")
			} else {
				if op.SequenceDef.Start == nil || *op.SequenceDef.Start != 500 {
					t.Errorf("SequenceDef.Start = %v, want 500", op.SequenceDef.Start)
				}
				if op.SequenceDef.Cache == nil || *op.SequenceDef.Cache != 20 {
					t.Errorf("SequenceDef.Cache = %v, want 20", op.SequenceDef.Cache)
				}
			}
			if op.Down == nil || !op.Down.Irreversible {
				t.Error("alter_sequence should have irreversible down")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected alter_sequence op for order_seq, got ops: %v", opsDebug(m.DDLOps))
	}
}

func TestMigrate_SequenceSQL(t *testing.T) {
	// create_sequence
	createOp := DDLOp{
		Op:     "create_sequence",
		Name:   "order_seq",
		Schema: "public",
		SequenceDef: &model.Sequence{
			Name:  "order_seq",
			Start: model.Int64Ptr(100),
		},
	}
	got := OpToSQL(createOp)
	if !strings.Contains(got, "CREATE SEQUENCE") {
		t.Errorf("create_sequence SQL missing CREATE SEQUENCE: %s", got)
	}
	if !strings.Contains(got, "order_seq") {
		t.Errorf("create_sequence SQL missing sequence name: %s", got)
	}
	if !strings.Contains(got, "START WITH 100") {
		t.Errorf("create_sequence SQL missing START WITH: %s", got)
	}

	// drop_sequence
	dropOp := DDLOp{
		Op:     "drop_sequence",
		Name:   "order_seq",
		Schema: "public",
	}
	got = OpToSQL(dropOp)
	if !strings.Contains(got, "DROP SEQUENCE") {
		t.Errorf("drop_sequence SQL missing DROP SEQUENCE: %s", got)
	}
	if !strings.Contains(got, "order_seq") {
		t.Errorf("drop_sequence SQL missing sequence name: %s", got)
	}

	// alter_sequence
	alterOp := DDLOp{
		Op:     "alter_sequence",
		Name:   "order_seq",
		Schema: "public",
		SequenceDef: &model.Sequence{
			Name:  "order_seq",
			Start: model.Int64Ptr(500),
			Cache: model.Int64Ptr(20),
		},
	}
	got = OpToSQL(alterOp)
	if !strings.Contains(got, "ALTER SEQUENCE") {
		t.Errorf("alter_sequence SQL missing ALTER SEQUENCE: %s", got)
	}
	if !strings.Contains(got, "START WITH 500") {
		t.Errorf("alter_sequence SQL missing START WITH: %s", got)
	}
	if !strings.Contains(got, "CACHE 20") {
		t.Errorf("alter_sequence SQL missing CACHE: %s", got)
	}
}

func TestGenerateMigration_AddFunction(t *testing.T) {
	desired := &model.Schema{
		Functions: []model.Function{
			{
				Name:       "calculate_tax",
				Schema:     "public",
				Language:   "plpgsql",
				ReturnType: "numeric",
				Args:       []model.FunctionArg{{Name: "amount", Type: typeinfo.T("numeric")}},
				Body:       "BEGIN RETURN amount * 0.1; END;",
			},
		},
	}
	d := &diff.SchemaDiff{
		FunctionsAdded: []string{"calculate_tax"},
	}

	m, diags := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_function" && op.Name == "calculate_tax" {
			found = true
			if op.FunctionDef == nil {
				t.Error("create_function op has no FunctionDef")
			}
			if op.Down == nil || len(op.Down.Ops) == 0 {
				t.Error("create_function should have reversible down (drop_function)")
			} else if op.Down.Ops[0].Op != "drop_function" {
				t.Errorf("down op = %q, want drop_function", op.Down.Ops[0].Op)
			}
			break
		}
	}
	if !found {
		t.Error("expected create_function op for calculate_tax")
	}

	// create_function is safe, should not have risk diagnostics.
	for _, diag := range diags {
		if diag.Code == "MIGRATE_RISK" && strings.Contains(diag.Message, "create_function") {
			t.Errorf("unexpected risk diagnostic for create_function: %s", diag.Message)
		}
	}
}

func TestGenerateMigration_DropFunction(t *testing.T) {
	desired := &model.Schema{}
	d := &diff.SchemaDiff{
		FunctionsRemoved: []string{"old_func"},
	}

	m, diags := GenerateMigration(d, desired, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_function" && op.Name == "old_func" {
			found = true
			if op.Down == nil || !op.Down.Irreversible {
				t.Error("drop_function should be irreversible")
			}
			break
		}
	}
	if !found {
		t.Error("expected drop_function op for old_func")
	}

	// drop_function is Caution, should have a warning diagnostic.
	hasCaution := false
	for _, diag := range diags {
		if diag.Code == "MIGRATE_RISK" && strings.Contains(diag.Message, "drop_function") {
			hasCaution = true
			break
		}
	}
	if !hasCaution {
		t.Error("expected caution diagnostic for drop_function")
	}
}

func TestGenerateMigration_FunctionBodyChange(t *testing.T) {
	desired := &model.Schema{
		Functions: []model.Function{
			{
				Name:       "calc",
				Schema:     "public",
				Language:   "plpgsql",
				ReturnType: "numeric",
				Args:       []model.FunctionArg{{Name: "x", Type: typeinfo.T("numeric")}},
				Body:       "BEGIN RETURN x * 2; END;",
			},
		},
	}
	d := &diff.SchemaDiff{
		FunctionsChanged: []diff.FunctionDiff{
			{
				Name:             "calc",
				BodyChanged:      &[2]string{"BEGIN RETURN x; END;", "BEGIN RETURN x * 2; END;"},
				SignatureChanged: false,
			},
		},
	}

	m, _ := GenerateMigration(d, desired, "0.3.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_or_replace_function" && op.Name == "calc" {
			found = true
			if op.FunctionDef == nil {
				t.Error("create_or_replace_function op has no FunctionDef")
			}
			break
		}
	}
	if !found {
		t.Error("expected create_or_replace_function op for body-only change")
	}
}

func TestGenerateMigration_FunctionSignatureChange(t *testing.T) {
	desired := &model.Schema{
		Functions: []model.Function{
			{
				Name:       "calc",
				Schema:     "public",
				Language:   "plpgsql",
				ReturnType: "numeric",
				Args:       []model.FunctionArg{{Name: "x", Type: typeinfo.T("numeric")}, {Name: "y", Type: typeinfo.T("numeric")}},
				Body:       "BEGIN RETURN x + y; END;",
			},
		},
	}
	d := &diff.SchemaDiff{
		FunctionsChanged: []diff.FunctionDiff{
			{
				Name:             "calc",
				ArgsChanged:      true,
				SignatureChanged: true,
			},
		},
	}

	m, diags := GenerateMigration(d, desired, "0.4.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	// Should have DROP then CREATE (not CREATE OR REPLACE).
	hasDropFunc := false
	hasCreateFunc := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_function" && op.Name == "calc" {
			hasDropFunc = true
		}
		if op.Op == "create_function" && op.Name == "calc" {
			hasCreateFunc = true
		}
		if op.Op == "create_or_replace_function" {
			t.Error("signature change should not use create_or_replace_function")
		}
	}
	if !hasDropFunc {
		t.Error("expected drop_function for signature change")
	}
	if !hasCreateFunc {
		t.Error("expected create_function for signature change")
	}

	// Should have the FUNCTION_SIGNATURE_CHANGE warning.
	hasSigWarning := false
	for _, diag := range diags {
		if diag.Code == "FUNCTION_SIGNATURE_CHANGE" {
			hasSigWarning = true
			break
		}
	}
	if !hasSigWarning {
		t.Error("expected FUNCTION_SIGNATURE_CHANGE diagnostic")
	}
}

func TestGenerateMigration_DomainAdded(t *testing.T) {
	desired := &model.Schema{
		Name: "app",
		Domains: []model.Domain{
			{Name: "slug", Schema: "public", BaseType: typeinfo.T("text"), Check: "VALUE ~ '^[a-z0-9-]+$'"},
		},
	}
	d := &diff.SchemaDiff{
		DomainsAdded: []string{"slug"},
	}

	m, _ := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_domain" && op.Name == "slug" {
			found = true
			if op.DomainDef == nil {
				t.Error("expected DomainDef to be set")
			}
			if op.Down == nil {
				t.Error("expected down op")
			} else if len(op.Down.Ops) != 1 || op.Down.Ops[0].Op != "drop_domain" {
				t.Errorf("expected drop_domain down op, got: %v", op.Down.Ops)
			}
			break
		}
	}
	if !found {
		t.Error("expected create_domain op for slug")
	}
}

func TestGenerateMigration_DomainRemoved(t *testing.T) {
	desired := &model.Schema{Name: "app"}
	d := &diff.SchemaDiff{
		DomainsRemoved: []string{"slug"},
	}

	m, diags := GenerateMigration(d, desired, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_domain" && op.Name == "slug" {
			found = true
			if op.Down == nil || !op.Down.Irreversible {
				t.Error("drop_domain should have irreversible down")
			}
			break
		}
	}
	if !found {
		t.Error("expected drop_domain op for slug")
	}

	hasDangerous := false
	for _, diag := range diags {
		if strings.Contains(diag.Message, "drop_domain") {
			hasDangerous = true
			break
		}
	}
	if !hasDangerous {
		t.Error("expected dangerous diagnostic for drop_domain")
	}
}

func TestGenerateMigration_DomainCheckChanged(t *testing.T) {
	desired := &model.Schema{Name: "app"}
	d := &diff.SchemaDiff{
		DomainsChanged: []diff.DomainDiff{
			{
				Name:         "slug",
				CheckChanged: &[2]string{"VALUE ~ '^[a-z]+$'", "VALUE ~ '^[a-z0-9-]+$'"},
			},
		},
	}

	m, _ := GenerateMigration(d, desired, "0.3.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	var dropFound, addFound bool
	for _, op := range m.DDLOps {
		if op.Op == "alter_domain_drop_constraint" && op.Name == "slug" {
			dropFound = true
		}
		if op.Op == "alter_domain_add_constraint" && op.Name == "slug" {
			addFound = true
			if op.Expr != "VALUE ~ '^[a-z0-9-]+$'" {
				t.Errorf("expected new check expression, got: %q", op.Expr)
			}
		}
	}
	if !dropFound {
		t.Error("expected alter_domain_drop_constraint op")
	}
	if !addFound {
		t.Error("expected alter_domain_add_constraint op")
	}
}

func TestOpToSQL_CreateDomain(t *testing.T) {
	op := DDLOp{
		Op:     "create_domain",
		Name:   "slug",
		Schema: "public",
		DomainDef: &model.Domain{
			Name:     "slug",
			BaseType: typeinfo.T("text"),
			NotNull:  true,
			Check:    "VALUE ~ '^[a-z0-9-]+$'",
		},
	}
	got := OpToSQL(op)
	if !strings.Contains(got, "CREATE DOMAIN") {
		t.Errorf("expected CREATE DOMAIN, got: %q", got)
	}
	if !strings.Contains(got, "slug") {
		t.Errorf("expected slug in SQL, got: %q", got)
	}
	if !strings.Contains(got, "NOT NULL") {
		t.Errorf("expected NOT NULL, got: %q", got)
	}
	if !strings.Contains(got, "CHECK") {
		t.Errorf("expected CHECK, got: %q", got)
	}
}

func TestOpToSQL_DropDomain(t *testing.T) {
	op := DDLOp{
		Op:     "drop_domain",
		Name:   "slug",
		Schema: "public",
	}
	got := OpToSQL(op)
	if !strings.Contains(got, "DROP DOMAIN") {
		t.Errorf("expected DROP DOMAIN, got: %q", got)
	}
	if !strings.Contains(got, "CASCADE") {
		t.Errorf("expected CASCADE, got: %q", got)
	}
}

func TestOpToSQL_AlterDomainAddConstraint(t *testing.T) {
	op := DDLOp{
		Op:     "alter_domain_add_constraint",
		Name:   "slug",
		Schema: "public",
		Column: "slug_check",
		Expr:   "VALUE ~ '^[a-z0-9-]+$'",
	}
	got := OpToSQL(op)
	if !strings.Contains(got, "ALTER DOMAIN") {
		t.Errorf("expected ALTER DOMAIN, got: %q", got)
	}
	if !strings.Contains(got, "ADD CONSTRAINT") {
		t.Errorf("expected ADD CONSTRAINT, got: %q", got)
	}
	if !strings.Contains(got, "slug_check") {
		t.Errorf("expected constraint name slug_check, got: %q", got)
	}
}

func TestOpToSQL_AlterDomainDropConstraint(t *testing.T) {
	op := DDLOp{
		Op:     "alter_domain_drop_constraint",
		Name:   "slug",
		Schema: "public",
		Column: "slug_check",
	}
	got := OpToSQL(op)
	if !strings.Contains(got, "ALTER DOMAIN") {
		t.Errorf("expected ALTER DOMAIN, got: %q", got)
	}
	if !strings.Contains(got, "DROP CONSTRAINT") {
		t.Errorf("expected DROP CONSTRAINT, got: %q", got)
	}
	if !strings.Contains(got, "slug_check") {
		t.Errorf("expected constraint name slug_check, got: %q", got)
	}
}

func TestGenerateMigration_DomainBaseTypeChanged(t *testing.T) {
	desired := &model.Schema{
		Name: "app",
		Domains: []model.Domain{
			{Name: "counter", Schema: "public", BaseType: typeinfo.T("int8")},
		},
	}
	d := &diff.SchemaDiff{
		DomainsChanged: []diff.DomainDiff{
			{
				Name:            "counter",
				BaseTypeChanged: &[2]string{"integer", "bigint"},
			},
		},
	}

	m, diags := GenerateMigration(d, desired, "0.4.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	var dropFound, createFound bool
	for _, op := range m.DDLOps {
		if op.Op == "drop_domain" && op.Name == "counter" {
			dropFound = true
		}
		if op.Op == "create_domain" && op.Name == "counter" {
			createFound = true
			if op.DomainDef == nil {
				t.Error("expected DomainDef on recreate")
			}
		}
	}
	if !dropFound {
		t.Error("expected drop_domain op for base type change")
	}
	if !createFound {
		t.Error("expected create_domain op for base type change")
	}

	// Should have a dangerous diagnostic for DROP.
	hasDangerous := false
	for _, diag := range diags {
		if strings.Contains(diag.Message, "drop_domain") {
			hasDangerous = true
			break
		}
	}
	if !hasDangerous {
		t.Error("expected dangerous diagnostic for domain base type change DROP")
	}
}

func TestGenerateMigration_DomainDefaultChanged(t *testing.T) {
	desired := &model.Schema{Name: "app"}
	d := &diff.SchemaDiff{
		DomainsChanged: []diff.DomainDiff{
			{
				Name:           "counter",
				DefaultChanged: &[2]string{"0", "1"},
			},
		},
	}

	m, _ := GenerateMigration(d, desired, "0.5.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "alter_domain_set_default" && op.Name == "counter" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected alter_domain_set_default op")
	}
}

func TestGenerateMigration_DomainDefaultRemoved(t *testing.T) {
	desired := &model.Schema{Name: "app"}
	d := &diff.SchemaDiff{
		DomainsChanged: []diff.DomainDiff{
			{
				Name:           "counter",
				DefaultChanged: &[2]string{"0", ""},
			},
		},
	}

	m, _ := GenerateMigration(d, desired, "0.6.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "alter_domain_drop_default" && op.Name == "counter" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected alter_domain_drop_default op")
	}
}

func TestGenerateMigration_DomainNotNullChanged(t *testing.T) {
	desired := &model.Schema{Name: "app"}
	d := &diff.SchemaDiff{
		DomainsChanged: []diff.DomainDiff{
			{
				Name:           "slug",
				NotNullChanged: &[2]bool{false, true},
			},
		},
	}

	m, _ := GenerateMigration(d, desired, "0.7.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "alter_domain_set_not_null" && op.Name == "slug" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected alter_domain_set_not_null op")
	}
}

func TestOpToSQL_AlterDomainSetDefault(t *testing.T) {
	op := DDLOp{
		Op:      "alter_domain_set_default",
		Name:    "counter",
		Schema:  "public",
		Default: "42",
		Type:    "bigint",
	}
	got := OpToSQL(op)
	if !strings.Contains(got, "ALTER DOMAIN") {
		t.Errorf("expected ALTER DOMAIN, got: %q", got)
	}
	if !strings.Contains(got, "SET DEFAULT") {
		t.Errorf("expected SET DEFAULT, got: %q", got)
	}
}

func TestOpToSQL_AlterDomainDropDefault(t *testing.T) {
	op := DDLOp{
		Op:     "alter_domain_drop_default",
		Name:   "counter",
		Schema: "public",
	}
	got := OpToSQL(op)
	if !strings.Contains(got, "ALTER DOMAIN") {
		t.Errorf("expected ALTER DOMAIN, got: %q", got)
	}
	if !strings.Contains(got, "DROP DEFAULT") {
		t.Errorf("expected DROP DEFAULT, got: %q", got)
	}
}

func TestOpToSQL_AlterDomainSetNotNull(t *testing.T) {
	op := DDLOp{
		Op:     "alter_domain_set_not_null",
		Name:   "slug",
		Schema: "public",
	}
	got := OpToSQL(op)
	if !strings.Contains(got, "ALTER DOMAIN") {
		t.Errorf("expected ALTER DOMAIN, got: %q", got)
	}
	if !strings.Contains(got, "SET NOT NULL") {
		t.Errorf("expected SET NOT NULL, got: %q", got)
	}
}

func TestOpToSQL_AlterDomainDropNotNull(t *testing.T) {
	op := DDLOp{
		Op:     "alter_domain_drop_not_null",
		Name:   "slug",
		Schema: "public",
	}
	got := OpToSQL(op)
	if !strings.Contains(got, "ALTER DOMAIN") {
		t.Errorf("expected ALTER DOMAIN, got: %q", got)
	}
	if !strings.Contains(got, "DROP NOT NULL") {
		t.Errorf("expected DROP NOT NULL, got: %q", got)
	}
}

func TestGenerateMigration_TriggerOnNewTable(t *testing.T) {
	d := &diff.SchemaDiff{
		TablesAdded: []string{"orders"},
	}
	desired := &model.Schema{
		Tables: []model.Table{
			{
				Name: "orders", Schema: "public",
				Comment: "Order table",
				PK:      []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
				},
				Triggers: []model.Trigger{
					{Name: "audit_insert", Function: "audit_fn", Events: []string{"INSERT"}, Timing: "AFTER", ForEach: "ROW"},
				},
			},
		},
	}
	m, _ := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, nil)
	// Find the create_trigger op.
	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_trigger" && op.Name == "audit_insert" {
			found = true
			if op.TriggerDef == nil {
				t.Error("expected TriggerDef to be set")
			}
			if op.TriggerDef.Function != "audit_fn" {
				t.Errorf("expected function audit_fn, got %s", op.TriggerDef.Function)
			}
			if op.Table != "orders" {
				t.Errorf("expected table orders, got %s", op.Table)
			}
		}
	}
	if !found {
		t.Error("expected create_trigger op for audit_insert")
	}
}

func TestGenerateMigration_TriggerAdded(t *testing.T) {
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "orders",
				TriggersAdded: []model.Trigger{
					{Name: "audit_insert", Function: "audit_fn", Events: []string{"INSERT"}, Timing: "AFTER", ForEach: "ROW"},
				},
			},
		},
	}
	desired := &model.Schema{PGVersion: 14}
	m, _ := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, nil)
	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_trigger" && op.Name == "audit_insert" {
			found = true
			if op.TriggerDef == nil {
				t.Error("expected TriggerDef to be set")
			}
		}
	}
	if !found {
		t.Error("expected create_trigger op")
	}
}

func TestGenerateMigration_TriggerRemoved(t *testing.T) {
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name:            "orders",
				TriggersRemoved: []string{"audit_insert"},
			},
		},
	}
	desired := &model.Schema{PGVersion: 14}
	m, _ := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, nil)
	found := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_trigger" && op.Name == "audit_insert" {
			found = true
			if op.Table != "orders" {
				t.Errorf("expected table orders, got %s", op.Table)
			}
		}
	}
	if !found {
		t.Error("expected drop_trigger op")
	}
}

func TestGenerateMigration_TriggerChanged(t *testing.T) {
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "orders",
				TriggersChanged: []diff.TriggerChange{
					{
						Name: "audit_insert",
						Old:  model.Trigger{Name: "audit_insert", Function: "old_fn", Events: []string{"INSERT"}, Timing: "AFTER", ForEach: "ROW"},
						New:  model.Trigger{Name: "audit_insert", Function: "new_fn", Events: []string{"INSERT", "UPDATE"}, Timing: "AFTER", ForEach: "ROW"},
					},
				},
			},
		},
	}
	desired := &model.Schema{PGVersion: 14}
	m, _ := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, nil)
	// Expect drop_trigger followed by create_trigger.
	dropFound := false
	createFound := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_trigger" && op.Name == "audit_insert" {
			dropFound = true
		}
		if op.Op == "create_trigger" && op.Name == "audit_insert" {
			createFound = true
			if op.TriggerDef == nil {
				t.Error("expected TriggerDef to be set")
			} else if op.TriggerDef.Function != "new_fn" {
				t.Errorf("expected new function new_fn, got %s", op.TriggerDef.Function)
			}
		}
	}
	if !dropFound {
		t.Error("expected drop_trigger op for changed trigger")
	}
	if !createFound {
		t.Error("expected create_trigger op for changed trigger")
	}
}

func TestOpToSQL_CreateTrigger(t *testing.T) {
	trig := model.Trigger{
		Name:     "audit_insert",
		Function: "audit_fn",
		Events:   []string{"INSERT"},
		Timing:   "AFTER",
		ForEach:  "ROW",
	}
	op := DDLOp{
		Op:         "create_trigger",
		Table:      "public.orders",
		Name:       "audit_insert",
		TriggerDef: &trig,
	}
	sql := OpToSQL(op)
	if sql == "" {
		t.Fatal("expected non-empty SQL")
	}
	if !strings.Contains(sql, "CREATE TRIGGER") {
		t.Errorf("expected CREATE TRIGGER in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, "audit_insert") {
		t.Errorf("expected trigger name in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, "AFTER") {
		t.Errorf("expected AFTER in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, "INSERT") {
		t.Errorf("expected INSERT in SQL, got: %s", sql)
	}
	if !strings.Contains(sql, "audit_fn") {
		t.Errorf("expected function name in SQL, got: %s", sql)
	}
}

func TestGenerateMigration_FKChanged(t *testing.T) {
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "scores",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "player_id", PGType: typeinfo.T("int8"), NotNull: true},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.scores",
				FKsChanged: []diff.FKChange{
					{
						Name: "fk_scores_player",
						Old: model.FK{
							Name:       "fk_scores_player",
							Columns:    []string{"player_id"},
							RefTable:   "players",
							RefColumns: []string{"id"},
							OnDelete:   "RESTRICT",
						},
						New: model.FK{
							Name:       "fk_scores_player",
							Columns:    []string{"player_id"},
							RefTable:   "players",
							RefColumns: []string{"id"},
							OnDelete:   "CASCADE",
						},
					},
				},
			},
		},
	}

	m, _ := GenerateMigration(d, desired, "0.8.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

	if len(m.DDLOps) != 2 {
		t.Fatalf("expected 2 DDL ops, got %d", len(m.DDLOps))
	}

	if m.DDLOps[0].Op != "drop_fk" {
		t.Errorf("expected first op to be drop_fk, got %s", m.DDLOps[0].Op)
	}
	if m.DDLOps[1].Op != "add_fk" {
		t.Errorf("expected second op to be add_fk, got %s", m.DDLOps[1].Op)
	}
	if m.DDLOps[1].OnDelete != "CASCADE" {
		t.Errorf("expected add_fk OnDelete=CASCADE, got %s", m.DDLOps[1].OnDelete)
	}
}

// --- RLS policy migration tests ---

func TestGenerateMigration_NewTableWithRLS(t *testing.T) {
	pol := model.Policy{
		Name:      "users_select",
		Type:      "PERMISSIVE",
		Operation: "SELECT",
		Role:      "app_user",
		Using:     "user_id = current_user_id()",
	}
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:      "documents",
			Schema:    "public",
			Comment:   "docs table",
			Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("int4"), NotNull: true}},
			PK:        []string{"id"},
			EnableRLS: true,
			ForceRLS:  true,
			Policies:  []model.Policy{pol},
		}},
	}
	d := &diff.SchemaDiff{
		TablesAdded: []string{"documents"},
	}
	m, _ := GenerateMigration(d, desired, "0.0.1", nil, 0, 0, extregistry.NewBuiltinRegistry())

	// Collect op names.
	var ops []string
	for _, op := range m.DDLOps {
		ops = append(ops, op.Op)
	}
	// Should have: create_table, enable_rls, force_rls, create_policy.
	found := map[string]bool{}
	for _, op := range m.DDLOps {
		found[op.Op] = true
	}
	for _, expected := range []string{"create_table", "enable_rls", "force_rls", "create_policy"} {
		if !found[expected] {
			t.Errorf("expected op %q not found in migration ops: %v", expected, ops)
		}
	}

	// Check ordering: enable_rls before create_policy.
	enableIdx := -1
	policyIdx := -1
	for i, op := range m.DDLOps {
		if op.Op == "enable_rls" {
			enableIdx = i
		}
		if op.Op == "create_policy" {
			policyIdx = i
		}
	}
	if enableIdx >= policyIdx {
		t.Errorf("enable_rls (idx %d) should come before create_policy (idx %d)", enableIdx, policyIdx)
	}

	// Check reversibility.
	for _, op := range m.DDLOps {
		if op.Op == "enable_rls" && op.Down != nil {
			if len(op.Down.Ops) == 0 || op.Down.Ops[0].Op != "disable_rls" {
				t.Errorf("enable_rls down should be disable_rls")
			}
		}
		if op.Op == "force_rls" && op.Down != nil {
			if len(op.Down.Ops) == 0 || op.Down.Ops[0].Op != "no_force_rls" {
				t.Errorf("force_rls down should be no_force_rls")
			}
		}
	}
}

func TestGenerateMigration_EnableRLSChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:      "documents",
			Schema:    "public",
			Comment:   "docs table",
			Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("int4"), NotNull: true}},
			PK:        []string{"id"},
			EnableRLS: true,
		}},
	}
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{{
			Name:             "documents",
			EnableRLSChanged: &[2]bool{false, true},
		}},
	}
	m, _ := GenerateMigration(d, desired, "0.0.1", nil, 0, 0, extregistry.NewBuiltinRegistry())

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "enable_rls" {
			found = true
			if op.Table != "documents" {
				t.Errorf("enable_rls table = %q, want documents", op.Table)
			}
			if op.Down == nil || len(op.Down.Ops) == 0 || op.Down.Ops[0].Op != "disable_rls" {
				t.Errorf("enable_rls down should be disable_rls")
			}
		}
	}
	if !found {
		t.Errorf("expected enable_rls op not found")
	}
}

func TestGenerateMigration_DisableRLS(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:      "documents",
			Schema:    "public",
			Comment:   "docs table",
			Columns:   []model.Column{{Name: "id", PGType: typeinfo.T("int4"), NotNull: true}},
			PK:        []string{"id"},
			EnableRLS: false,
		}},
	}
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{{
			Name:             "documents",
			EnableRLSChanged: &[2]bool{true, false},
		}},
	}
	m, _ := GenerateMigration(d, desired, "0.0.1", nil, 0, 0, extregistry.NewBuiltinRegistry())

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "disable_rls" {
			found = true
			if op.Table != "documents" {
				t.Errorf("disable_rls table = %q, want documents", op.Table)
			}
			if op.Down == nil || len(op.Down.Ops) == 0 || op.Down.Ops[0].Op != "enable_rls" {
				t.Errorf("disable_rls down should be enable_rls")
			}
		}
	}
	if !found {
		t.Errorf("expected disable_rls op not found")
	}
}

func TestGenerateMigration_ForceRLSChanged(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:     "documents",
			Schema:   "public",
			Comment:  "docs table",
			Columns:  []model.Column{{Name: "id", PGType: typeinfo.T("int4"), NotNull: true}},
			PK:       []string{"id"},
			ForceRLS: true,
		}},
	}
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{{
			Name:            "documents",
			ForceRLSChanged: &[2]bool{false, true},
		}},
	}
	m, _ := GenerateMigration(d, desired, "0.0.1", nil, 0, 0, extregistry.NewBuiltinRegistry())

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "force_rls" {
			found = true
			if op.Table != "documents" {
				t.Errorf("force_rls table = %q, want documents", op.Table)
			}
			if op.Down == nil || len(op.Down.Ops) == 0 || op.Down.Ops[0].Op != "no_force_rls" {
				t.Errorf("force_rls down should be no_force_rls")
			}
		}
	}
	if !found {
		t.Errorf("expected force_rls op not found")
	}
}

func TestGenerateMigration_NoForceRLS(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:     "documents",
			Schema:   "public",
			Comment:  "docs table",
			Columns:  []model.Column{{Name: "id", PGType: typeinfo.T("int4"), NotNull: true}},
			PK:       []string{"id"},
			ForceRLS: false,
		}},
	}
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{{
			Name:            "documents",
			ForceRLSChanged: &[2]bool{true, false},
		}},
	}
	m, _ := GenerateMigration(d, desired, "0.0.1", nil, 0, 0, extregistry.NewBuiltinRegistry())

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "no_force_rls" {
			found = true
			if op.Table != "documents" {
				t.Errorf("no_force_rls table = %q, want documents", op.Table)
			}
			if op.Down == nil || len(op.Down.Ops) == 0 || op.Down.Ops[0].Op != "force_rls" {
				t.Errorf("no_force_rls down should be force_rls")
			}
		}
	}
	if !found {
		t.Errorf("expected no_force_rls op not found")
	}
}

func TestGenerateMigration_PolicyAdded(t *testing.T) {
	pol := model.Policy{
		Name:      "users_select",
		Type:      "PERMISSIVE",
		Operation: "SELECT",
		Role:      "app_user",
		Using:     "user_id = current_user_id()",
	}
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:     "documents",
			Schema:   "public",
			Comment:  "docs",
			Columns:  []model.Column{{Name: "id", PGType: typeinfo.T("int4"), NotNull: true}},
			PK:       []string{"id"},
			Policies: []model.Policy{pol},
		}},
	}
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{{
			Name:          "documents",
			PoliciesAdded: []model.Policy{pol},
		}},
	}
	m, _ := GenerateMigration(d, desired, "0.0.1", nil, 0, 0, extregistry.NewBuiltinRegistry())

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "create_policy" {
			found = true
			if op.Name != "users_select" {
				t.Errorf("create_policy name = %q, want users_select", op.Name)
			}
			if op.PolicyDef == nil {
				t.Fatalf("create_policy PolicyDef is nil")
			}
			if op.PolicyDef.Using != "user_id = current_user_id()" {
				t.Errorf("PolicyDef.Using = %q", op.PolicyDef.Using)
			}
			// Down should be drop_policy.
			if op.Down == nil || len(op.Down.Ops) == 0 || op.Down.Ops[0].Op != "drop_policy" {
				t.Errorf("create_policy down should be drop_policy")
			}
		}
	}
	if !found {
		t.Errorf("expected create_policy op not found")
	}
}

func TestGenerateMigration_PolicyRemoved(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:    "documents",
			Schema:  "public",
			Comment: "docs",
			Columns: []model.Column{{Name: "id", PGType: typeinfo.T("int4"), NotNull: true}},
			PK:      []string{"id"},
		}},
	}
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{{
			Name:            "documents",
			PoliciesRemoved: []string{"old_policy"},
		}},
	}
	m, _ := GenerateMigration(d, desired, "0.0.1", nil, 0, 0, extregistry.NewBuiltinRegistry())

	found := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_policy" {
			found = true
			if op.Name != "old_policy" {
				t.Errorf("drop_policy name = %q, want old_policy", op.Name)
			}
			if op.Table != "documents" {
				t.Errorf("drop_policy table = %q, want documents", op.Table)
			}
			// Down should be irreversible.
			if op.Down == nil || !op.Down.Irreversible {
				t.Errorf("drop_policy down should be irreversible")
			}
		}
	}
	if !found {
		t.Errorf("expected drop_policy op not found")
	}
}

func TestGenerateMigration_PolicyChanged(t *testing.T) {
	newPol := model.Policy{
		Name:      "users_select",
		Type:      "PERMISSIVE",
		Operation: "SELECT",
		Role:      "app_user",
		Using:     "user_id = current_user_id() AND active",
	}
	desired := &model.Schema{
		Tables: []model.Table{{
			Name:     "documents",
			Schema:   "public",
			Comment:  "docs",
			Columns:  []model.Column{{Name: "id", PGType: typeinfo.T("int4"), NotNull: true}},
			PK:       []string{"id"},
			Policies: []model.Policy{newPol},
		}},
	}
	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{{
			Name: "documents",
			PoliciesChanged: []diff.PolicyDiff{{
				Name:         "users_select",
				UsingChanged: &[2]string{"user_id = current_user_id()", "user_id = current_user_id() AND active"},
			}},
		}},
	}
	m, _ := GenerateMigration(d, desired, "0.0.1", nil, 0, 0, extregistry.NewBuiltinRegistry())

	// Expect drop_policy followed by create_policy.
	dropFound := false
	createFound := false
	for _, op := range m.DDLOps {
		if op.Op == "drop_policy" && op.Name == "users_select" {
			dropFound = true
		}
		if op.Op == "create_policy" && op.Name == "users_select" {
			createFound = true
			if op.PolicyDef == nil {
				t.Error("expected PolicyDef to be set")
			} else if op.PolicyDef.Using != "user_id = current_user_id() AND active" {
				t.Errorf("expected new Using expression, got %s", op.PolicyDef.Using)
			}
		}
	}
	if !dropFound {
		t.Error("expected drop_policy op for changed policy")
	}
	if !createFound {
		t.Error("expected create_policy op for changed policy")
	}
}

func TestOpToSQL_CreatePolicy(t *testing.T) {
	pol := model.Policy{
		Name:      "users_select",
		Type:      "PERMISSIVE",
		Operation: "SELECT",
		Role:      "app_user",
		Using:     "user_id = current_user_id()",
	}
	op := DDLOp{
		Op:        "create_policy",
		Table:     "public.documents",
		Name:      "users_select",
		Schema:    "public",
		PolicyDef: &pol,
	}
	sql := OpToSQL(op)
	if !strings.Contains(sql, "CREATE POLICY") {
		t.Errorf("expected CREATE POLICY, got: %s", sql)
	}
	if !strings.Contains(sql, "users_select") {
		t.Errorf("expected policy name, got: %s", sql)
	}
	if !strings.Contains(sql, "USING") {
		t.Errorf("expected USING clause, got: %s", sql)
	}
}

func TestOpToSQL_DropPolicy(t *testing.T) {
	op := DDLOp{
		Op:     "drop_policy",
		Table:  "public.documents",
		Name:   "old_policy",
		Schema: "public",
	}
	sql := OpToSQL(op)
	if !strings.Contains(sql, "DROP POLICY") {
		t.Errorf("expected DROP POLICY, got: %s", sql)
	}
	if !strings.Contains(sql, "old_policy") {
		t.Errorf("expected policy name, got: %s", sql)
	}
}

func TestOpToSQL_EnableRLS(t *testing.T) {
	op := DDLOp{
		Op:     "enable_rls",
		Table:  "public.documents",
		Schema: "public",
	}
	sql := OpToSQL(op)
	if !strings.Contains(sql, "ENABLE ROW LEVEL SECURITY") {
		t.Errorf("expected ENABLE ROW LEVEL SECURITY, got: %s", sql)
	}
}

func TestOpToSQL_DisableRLS(t *testing.T) {
	op := DDLOp{
		Op:     "disable_rls",
		Table:  "public.documents",
		Schema: "public",
	}
	sql := OpToSQL(op)
	if !strings.Contains(sql, "DISABLE ROW LEVEL SECURITY") {
		t.Errorf("expected DISABLE ROW LEVEL SECURITY, got: %s", sql)
	}
}

func TestOpToSQL_ForceRLS(t *testing.T) {
	op := DDLOp{
		Op:     "force_rls",
		Table:  "public.documents",
		Schema: "public",
	}
	sql := OpToSQL(op)
	if !strings.Contains(sql, "FORCE ROW LEVEL SECURITY") {
		t.Errorf("expected FORCE ROW LEVEL SECURITY, got: %s", sql)
	}
}

func TestOpToSQL_NoForceRLS(t *testing.T) {
	op := DDLOp{
		Op:     "no_force_rls",
		Table:  "public.documents",
		Schema: "public",
	}
	sql := OpToSQL(op)
	if !strings.Contains(sql, "NO FORCE ROW LEVEL SECURITY") {
		t.Errorf("expected NO FORCE ROW LEVEL SECURITY, got: %s", sql)
	}
}

func opsString(ops []DDLOp) string {
	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = op.Op
	}
	return strings.Join(names, ", ")
}

func TestOpToSQL_CreateTableConsolidated(t *testing.T) {
	op := DDLOp{
		Op:      "create_table",
		Table:   "public.users",
		PK:      []string{"id"},
		Comment: "Users table",
		ConsolidatedOps: []DDLOp{
			{Op: "add_column", Table: "public.users", Column: "email", Type: "text", NotNull: true},
			{Op: "add_column", Table: "public.users", Column: "name", Type: "varchar(100)"},
			{Op: "add_fk", Table: "public.users", Name: "fk_users_org", Columns: []string{"org_id"}, RefTable: "public.orgs", RefCols: []string{"id"}, OnDelete: "CASCADE"},
		},
	}
	sql := OpToSQL(op)
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Errorf("expected CREATE TABLE, got: %s", sql)
	}
	if !strings.Contains(sql, "email") {
		t.Errorf("expected email column, got: %s", sql)
	}
	if !strings.Contains(sql, "text") {
		t.Errorf("expected text type, got: %s", sql)
	}
	if !strings.Contains(sql, "NOT NULL") {
		t.Errorf("expected NOT NULL, got: %s", sql)
	}
	if !strings.Contains(sql, "name") {
		t.Errorf("expected name column, got: %s", sql)
	}
	if !strings.Contains(sql, "varchar(100)") {
		t.Errorf("expected varchar(100) type, got: %s", sql)
	}
	// FKs are emitted as separate ALTER TABLE statements (cycle-safe DDL),
	// not inline in CREATE TABLE. Verify the PK constraint is present instead.
	if !strings.Contains(sql, "PRIMARY KEY") {
		t.Errorf("expected PRIMARY KEY, got: %s", sql)
	}
}

func TestBatchSize_TOMLRoundTrip(t *testing.T) {
	m := &Migration{
		Description: "Test batched DML",
		DMLOps: []DMLOp{
			{
				Op:        "backfill",
				SQL:       "UPDATE foo SET bar = 1 WHERE ctid IN (SELECT ctid FROM foo WHERE bar IS NULL LIMIT 5000)",
				BatchSize: 5000,
				Down:      &DownOp{Irreversible: true},
			},
		},
	}

	toml := FormatMigration(m)

	// Verify batch_size appears in output.
	if !strings.Contains(toml, "batch_size = 5000") {
		t.Fatalf("expected batch_size = 5000 in TOML, got:\n%s", toml)
	}

	// Parse it back.
	parsed, err := ParseMigration(toml)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(parsed.DMLOps) != 1 {
		t.Fatalf("expected 1 DML op, got %d", len(parsed.DMLOps))
	}
	if parsed.DMLOps[0].BatchSize != 5000 {
		t.Errorf("BatchSize = %d, want 5000", parsed.DMLOps[0].BatchSize)
	}
}

func TestBatchSize_ZeroNotSerialized(t *testing.T) {
	m := &Migration{
		Description: "No batching",
		DMLOps: []DMLOp{
			{
				Op:  "backfill",
				SQL: "UPDATE foo SET bar = 1",
			},
		},
	}

	toml := FormatMigration(m)
	if strings.Contains(toml, "batch_size") {
		t.Fatalf("expected no batch_size in TOML when BatchSize=0, got:\n%s", toml)
	}
}

func TestGenerateMigration_NonImmutableDefault(t *testing.T) {
	tests := []struct {
		name       string
		defaultVal string
		wantWarn   bool
	}{
		{"now()", "now()", true},
		{"gen_random_uuid()", "gen_random_uuid()", true},
		{"clock_timestamp()", "clock_timestamp()", true},
		{"random()", "random()", true},
		{"nextval", "nextval('my_seq')", true},
		{"txid_current()", "txid_current()", true},
		{"statement_timestamp()", "statement_timestamp()", true},
		{"uuid_generate_v7()", "uuid_generate_v7()", true},
		{"uuid_generate_v4()", "uuid_generate_v4()", true},
		{"constant_int", "42", false},
		{"constant_string", "'hello'", false},
		{"NOW_uppercase", "NOW()", true}, // case-insensitive
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := &model.Schema{
				Name: "public",
				Tables: []model.Table{
					{
						Name:   "users",
						Schema: "public",
						Columns: []model.Column{
							{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
							{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true, DefaultExpr: tt.defaultVal},
						},
					},
				},
			}

			d := &diff.SchemaDiff{
				TablesChanged: []diff.TableDiff{
					{
						Name: "users",
						ColumnsAdded: []model.Column{
							{Name: "created_at", PGType: typeinfo.T("timestamptz"), NotNull: true, DefaultExpr: tt.defaultVal},
						},
					},
				},
			}

			_, diags := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, extregistry.NewBuiltinRegistry())

			found := false
			for _, diag := range diags {
				if diag.Code == "NON_IMMUTABLE_DEFAULT" {
					found = true
					break
				}
			}

			if tt.wantWarn && !found {
				t.Errorf("expected NON_IMMUTABLE_DEFAULT diagnostic for default %q, got none", tt.defaultVal)
			}
			if !tt.wantWarn && found {
				t.Errorf("did not expect NON_IMMUTABLE_DEFAULT diagnostic for default %q, but got one", tt.defaultVal)
			}
		})
	}
}

func TestIsNonImmutableDefault(t *testing.T) {
	tests := []struct {
		val    interface{}
		expect bool
	}{
		{nil, false},
		{"now()", true},
		{"NOW()", true},
		{"gen_random_uuid()", true},
		{"nextval('foo_seq')", true},
		{"clock_timestamp()", true},
		{"random()", true},
		{"txid_current()", true},
		{"statement_timestamp()", true},
		{"uuid_generate_v7()", true},
		{"uuid_generate_v4()", true},
		{42, false},
		{"42", false},
		{"'hello'", false},
		{"current_timestamp", false}, // stable but not in the list; PG treats it specially
		{true, false},
	}

	for _, tt := range tests {
		got := isNonImmutableDefault(tt.val)
		if got != tt.expect {
			t.Errorf("isNonImmutableDefault(%v) = %v, want %v", tt.val, got, tt.expect)
		}
	}
}

func TestGenerateMigration_SMTransitionChangeRegeneratesTrigger(t *testing.T) {
	desired := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "orders",
				Schema: "app",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "status", PGType: typeinfo.T("order_status"), NotNull: true, SemanticTypeName: "order_status"},
				},
				Comment: "Orders table",
			},
		},
		StateMachineTransitions: []model.SMTransitionMap{
			{
				TypeName: "order_status",
				Transitions: map[string][]string{
					"pending":   {"active", "cancelled"},
					"active":    {"suspended", "completed"},
					"suspended": {"active"},
				},
				States:         []string{"pending", "active", "suspended", "cancelled", "completed"},
				EnforceTrigger: true,
			},
		},
	}

	d := &diff.SchemaDiff{
		SMTransitionsChanged: []diff.SMTransitionDiff{
			{
				TypeName: "order_status",
				TransitionsAdded: []diff.SMTransitionRef{
					{From: "suspended", To: "active"},
				},
			},
		},
	}

	m, _ := GenerateMigration(d, desired, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	// Should have 3 ops: drop_trigger, create_sm_trigger_function, create_sm_trigger.
	var dropTrig, createFunc, createTrig bool
	for _, op := range m.DDLOps {
		switch op.Op {
		case "drop_trigger":
			if op.Name == "_pgdesign_sm_orders_status" && op.Table == "app.orders" {
				dropTrig = true
			}
		case "create_sm_trigger_function":
			if op.Name == "_pgdesign_sm_orders_status" && op.Table == "app.orders" {
				createFunc = true
				if op.RawSQL == "" {
					t.Error("create_sm_trigger_function should have RawSQL")
				}
				if !strings.Contains(op.RawSQL, "CREATE OR REPLACE FUNCTION") {
					t.Error("RawSQL should contain CREATE OR REPLACE FUNCTION")
				}
				if !strings.Contains(op.RawSQL, "suspended") {
					t.Error("RawSQL should reference the 'suspended' state")
				}
			}
		case "create_sm_trigger":
			if op.Name == "_pgdesign_sm_orders_status" && op.Table == "app.orders" {
				createTrig = true
				if op.RawSQL == "" {
					t.Error("create_sm_trigger should have RawSQL")
				}
				if !strings.Contains(op.RawSQL, "CREATE TRIGGER") {
					t.Error("RawSQL should contain CREATE TRIGGER")
				}
			}
		}
	}
	if !dropTrig {
		t.Error("expected drop_trigger op for _pgdesign_sm_orders_status")
	}
	if !createFunc {
		t.Error("expected create_sm_trigger_function op for _pgdesign_sm_orders_status")
	}
	if !createTrig {
		t.Error("expected create_sm_trigger op for _pgdesign_sm_orders_status")
	}
}

func TestGenerateMigration_SMTransitionChangeNoEnforceTrigger(t *testing.T) {
	// When EnforceTrigger is false, no trigger ops should be generated.
	desired := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "orders",
				Schema: "app",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "status", PGType: typeinfo.T("order_status"), NotNull: true, SemanticTypeName: "order_status"},
				},
				Comment: "Orders table",
			},
		},
		StateMachineTransitions: []model.SMTransitionMap{
			{
				TypeName: "order_status",
				Transitions: map[string][]string{
					"pending": {"active"},
					"active":  {"completed"},
				},
				States:         []string{"pending", "active", "completed"},
				EnforceTrigger: false,
			},
		},
	}

	d := &diff.SchemaDiff{
		SMTransitionsChanged: []diff.SMTransitionDiff{
			{
				TypeName: "order_status",
				TransitionsAdded: []diff.SMTransitionRef{
					{From: "active", To: "completed"},
				},
			},
		},
	}

	m, _ := GenerateMigration(d, desired, "0.2.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	for _, op := range m.DDLOps {
		if op.Op == "create_sm_trigger_function" || op.Op == "create_sm_trigger" {
			t.Errorf("should not generate SM trigger ops when EnforceTrigger=false, got %s", op.Op)
		}
	}
}

func TestGenerateMigration_SMTransitionChangeOpToSQL(t *testing.T) {
	// Verify OpToSQL correctly renders SM trigger ops.
	funcOp := DDLOp{
		Op:     "create_sm_trigger_function",
		Table:  "app.orders",
		Name:   "_pgdesign_sm_orders_status",
		RawSQL: "CREATE OR REPLACE FUNCTION app._pgdesign_sm_orders_status() RETURNS trigger AS $pgdesign$ BEGIN RETURN NEW; END; $pgdesign$ LANGUAGE plpgsql;",
	}
	sql := OpToSQL(funcOp)
	if sql != funcOp.RawSQL {
		t.Errorf("OpToSQL should return RawSQL for create_sm_trigger_function, got: %s", sql)
	}

	trigOp := DDLOp{
		Op:     "create_sm_trigger",
		Table:  "app.orders",
		Name:   "_pgdesign_sm_orders_status",
		RawSQL: "CREATE TRIGGER _pgdesign_sm_orders_status BEFORE UPDATE OF status ON app.orders FOR EACH ROW EXECUTE FUNCTION app._pgdesign_sm_orders_status();",
	}
	sql = OpToSQL(trigOp)
	if sql != trigOp.RawSQL {
		t.Errorf("OpToSQL should return RawSQL for create_sm_trigger, got: %s", sql)
	}

	// Without RawSQL, should produce a comment.
	emptyOp := DDLOp{
		Op:   "create_sm_trigger_function",
		Name: "test",
	}
	sql = OpToSQL(emptyOp)
	if !strings.Contains(sql, "missing pre-rendered SQL") {
		t.Errorf("expected fallback comment for missing RawSQL, got: %s", sql)
	}
}

// createMigrationDir creates a temporary migrations directory with the given
// versions. Each version gets a minimal valid migration TOML file.
func createMigrationDir(t *testing.T, versions ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, v := range versions {
		content := fmt.Sprintf("description = %q\n\n[[ddl]]\nop = \"create_table\"\ntable = \"public.t_%s\"\ndown = { op = \"drop_table\", table = \"public.t_%s\" }\n",
			"Migration "+v, strings.ReplaceAll(v, ".", "_"), strings.ReplaceAll(v, ".", "_"))
		if err := os.WriteFile(filepath.Join(dir, v+".toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestIntegration_Baseline(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}

	dir := createMigrationDir(t, "1.0.0")

	// Baseline a fresh database.
	if err := Baseline(ctx, conn, dir, "1.0.0", "Initial baseline"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Verify the record exists.
	versions, err := AppliedVersions(ctx, conn)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(versions) != 1 || versions[0] != "1.0.0" {
		t.Errorf("versions = %v, want [1.0.0]", versions)
	}
}

func TestIntegration_BaselineIdempotent(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}

	dir := createMigrationDir(t, "1.0.0")

	// Baseline twice with the same version: should succeed.
	if err := Baseline(ctx, conn, dir, "1.0.0", "Initial baseline"); err != nil {
		t.Fatalf("first baseline: %v", err)
	}
	if err := Baseline(ctx, conn, dir, "1.0.0", "Initial baseline"); err != nil {
		t.Fatalf("second baseline (idempotent): %v", err)
	}

	// Verify only one record exists.
	versions, err := AppliedVersions(ctx, conn)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(versions) != 1 {
		t.Errorf("versions = %v, want exactly [1.0.0]", versions)
	}
}

// TestIntegration_BaselineRecordsAllVersions is the core regression test for
// the history-coverage bug: baseline must record ALL discovered versions <=
// target so that a subsequent Apply finds zero pending migrations.
func TestIntegration_BaselineRecordsAllVersions(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}

	dir := createMigrationDir(t, "0.1.0", "0.2.0", "0.3.0")

	// Baseline to v0.3.0 -- should record all three versions.
	if err := Baseline(ctx, conn, dir, "0.3.0", "Adopt existing schema"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Verify all three versions are recorded.
	versions, err := AppliedVersions(ctx, conn)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("versions = %v, want [0.1.0, 0.2.0, 0.3.0]", versions)
	}
	if versions[0] != "0.1.0" || versions[1] != "0.2.0" || versions[2] != "0.3.0" {
		t.Errorf("versions = %v, want [0.1.0, 0.2.0, 0.3.0]", versions)
	}

	// Apply should find zero pending migrations.
	applied, err := Apply(ctx, conn, dir, "")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("apply returned %v, want zero pending migrations after baseline", applied)
	}
}

// TestIntegration_BaselineAdditiveRerun tests additive idempotency: re-running
// baseline after new migration files are added records the missing versions.
func TestIntegration_BaselineAdditiveRerun(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}

	// First baseline with only v0.1.0 and v0.2.0.
	dir := createMigrationDir(t, "0.1.0", "0.2.0")
	if err := Baseline(ctx, conn, dir, "0.2.0", "Initial baseline"); err != nil {
		t.Fatalf("first baseline: %v", err)
	}

	versions, err := AppliedVersions(ctx, conn)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("after first baseline: versions = %v, want [0.1.0, 0.2.0]", versions)
	}

	// Add v0.3.0 migration file and re-baseline to v0.3.0.
	content := fmt.Sprintf("description = %q\n\n[[ddl]]\nop = \"create_table\"\ntable = \"public.t_0_3_0\"\ndown = { op = \"drop_table\", table = \"public.t_0_3_0\" }\n", "Migration 0.3.0")
	if err := os.WriteFile(filepath.Join(dir, "0.3.0.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Baseline(ctx, conn, dir, "0.3.0", "Re-baseline with new version"); err != nil {
		t.Fatalf("re-baseline: %v", err)
	}

	versions, err = AppliedVersions(ctx, conn)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("after re-baseline: versions = %v, want [0.1.0, 0.2.0, 0.3.0]", versions)
	}
}

// TestIntegration_BaselineOutOfOrderGuard tests that baseline detects when a
// migration file with version < max-applied was added after later versions
// were applied.
func TestIntegration_BaselineOutOfOrderGuard(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}

	// Baseline with v0.2.0 and v0.3.0 (no v0.1.0).
	dir := createMigrationDir(t, "0.2.0", "0.3.0")
	if err := Baseline(ctx, conn, dir, "0.3.0", "Initial baseline"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Now add v0.1.0 migration file (added after v0.3.0 was applied).
	content := fmt.Sprintf("description = %q\n\n[[ddl]]\nop = \"create_table\"\ntable = \"public.t_0_1_0\"\ndown = { op = \"drop_table\", table = \"public.t_0_1_0\" }\n", "Migration 0.1.0")
	if err := os.WriteFile(filepath.Join(dir, "0.1.0.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-baseline with v0.3.0: should detect the out-of-order v0.1.0.
	err = Baseline(ctx, conn, dir, "0.3.0", "Re-baseline")
	if err == nil {
		t.Fatal("expected out-of-order error, got nil")
	}
	if !strings.Contains(err.Error(), "out-of-order") {
		t.Errorf("expected 'out-of-order' in error, got: %v", err)
	}
}

// TestIntegration_BaselineDivergence tests that baseline detects when a
// previously recorded version has no corresponding migration file (deleted).
func TestIntegration_BaselineDivergence(t *testing.T) {
	ephDB := setupEphemeralDB(t)
	ctx := context.Background()

	conn, err := ephDB.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to ephemeral DB: %v", err)
	}

	// Baseline with v0.1.0 and v0.2.0.
	dir := createMigrationDir(t, "0.1.0", "0.2.0")
	if err := Baseline(ctx, conn, dir, "0.2.0", "Initial baseline"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Delete v0.1.0 migration file.
	os.Remove(filepath.Join(dir, "0.1.0.toml"))

	// Re-baseline: should detect divergence (v0.1.0 recorded but file missing).
	err = Baseline(ctx, conn, dir, "0.2.0", "Re-baseline")
	if err == nil {
		t.Fatal("expected divergence error, got nil")
	}
	if !strings.Contains(err.Error(), "divergence") {
		t.Errorf("expected 'divergence' in error, got: %v", err)
	}
}

func TestGenerateMigration_CreateTableRoundTrip(t *testing.T) {
	// RED-GREEN: create_table ops generated by GenerateMigration must survive
	// the WriteMigrationFile -> ParseMigrationFile round trip with all column
	// definitions intact. Before the fix, ConsolidatedOps was never populated
	// for new tables, so columns were lost on serialization.

	defaultVal := "active"
	desired := &model.Schema{
		Name: "app",
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "app",
				PK:     []string{"id"},
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("int8"), NotNull: true},
					{Name: "email", PGType: typeinfo.T("text"), NotNull: true},
					{Name: "name", PGType: typeinfo.Parse("varchar(100)"), NotNull: false},
					{Name: "score", PGType: typeinfo.T("numeric"), NotNull: true, Default: model.StrPtr("0")},
					{Name: "bio", PGType: typeinfo.T("text"), NotNull: false, Collation: "en_US"},
					{Name: "status", PGType: typeinfo.T("text"), NotNull: true, Default: &defaultVal},
					{Name: "tags", PGType: typeinfo.T("text"), NotNull: false, Array: true},
					{Name: "display_name", PGType: typeinfo.T("text"), NotNull: true, Generated: "name || ' <' || email || '>'", Stored: true},
				},
				FKs: []model.FK{
					{Name: "fk_users_org", Columns: []string{"org_id"}, RefTable: "app.orgs", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
				Indexes: []model.Index{
					{Name: "idx_users_email", Columns: []string{"email"}, Unique: true},
				},
				Checks: []model.CheckConstraint{
					{Name: "ck_users_email", Expr: "email <> ''"},
				},
				Uniques: []model.UniqueConstraint{
					{Name: "uq_users_name_status", Columns: []string{"name", "status"}},
				},
				Comment: "User accounts",
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesAdded: []string{"app.users"},
	}

	m, _ := GenerateMigration(d, desired, "0.1.0", nil, 0, 0, extregistry.NewBuiltinRegistry())
	if m == nil {
		t.Fatal("expected non-nil migration")
	}

	// Write to file, then re-parse -- this is the lossy round trip.
	dir := t.TempDir()
	path := filepath.Join(dir, "0.1.0.toml")
	if err := WriteMigrationFile(path, m); err != nil {
		t.Fatalf("WriteMigrationFile: %v", err)
	}

	parsed, err := ParseMigrationFile(path)
	if err != nil {
		t.Fatalf("ParseMigrationFile: %v", err)
	}

	// Find the create_table op.
	var createOp *DDLOp
	for i, op := range parsed.DDLOps {
		if op.Op == "create_table" && op.Table == "app.users" {
			createOp = &parsed.DDLOps[i]
			break
		}
	}
	if createOp == nil {
		t.Fatal("no create_table op found for app.users after round trip")
	}

	// TableDef is not serialized, so after round trip it must be nil.
	if createOp.TableDef != nil {
		t.Error("TableDef should be nil after round trip (it is not serialized)")
	}

	// ConsolidatedOps must contain add_column ops for all columns.
	colOps := make(map[string]DDLOp)
	for _, cop := range createOp.ConsolidatedOps {
		if cop.Op == "add_column" {
			colOps[cop.Column] = cop
		}
	}

	// Verify each column survived the round trip.
	wantCols := []struct {
		name      string
		typ       string
		notNull   bool
		defVal    interface{}
		collation string
	}{
		{"id", "int8", true, nil, ""},
		{"email", "text", true, nil, ""},
		{"name", "varchar(100)", false, nil, ""},
		{"score", "numeric", true, "0", ""},
		{"bio", "text", false, nil, "en_US"},
		{"status", "text", true, "active", ""},
		{"tags", "text[]", false, nil, ""},
		{"display_name", "text", true, nil, ""},
	}

	if len(colOps) != len(wantCols) {
		t.Fatalf("got %d add_column ops, want %d", len(colOps), len(wantCols))
	}

	for _, wc := range wantCols {
		cop, ok := colOps[wc.name]
		if !ok {
			t.Errorf("missing add_column for %q", wc.name)
			continue
		}
		if cop.Type != wc.typ {
			t.Errorf("column %q: Type = %q, want %q", wc.name, cop.Type, wc.typ)
		}
		if cop.NotNull != wc.notNull {
			t.Errorf("column %q: NotNull = %v, want %v", wc.name, cop.NotNull, wc.notNull)
		}
		if wc.defVal != nil {
			if cop.Default == nil {
				t.Errorf("column %q: Default = nil, want %v", wc.name, wc.defVal)
			} else {
				gotDefault := fmt.Sprintf("%v", cop.Default)
				wantDefault := fmt.Sprintf("%v", wc.defVal)
				if gotDefault != wantDefault {
					t.Errorf("column %q: Default = %v, want %v", wc.name, cop.Default, wc.defVal)
				}
			}
		} else if cop.Default != nil {
			t.Errorf("column %q: Default = %v, want nil", wc.name, cop.Default)
		}
		if cop.Collation != wc.collation {
			t.Errorf("column %q: Collation = %q, want %q", wc.name, cop.Collation, wc.collation)
		}
	}

	// Verify generated column fields survived the round trip.
	if genCol, ok := colOps["display_name"]; ok {
		if genCol.Generated != "name || ' <' || email || '>'" {
			t.Errorf("display_name Generated = %q, want expression", genCol.Generated)
		}
		if !genCol.Stored {
			t.Errorf("display_name Stored = false, want true")
		}
	}

	// Verify PK survived.
	if len(createOp.PK) != 1 || createOp.PK[0] != "id" {
		t.Errorf("PK = %v, want [id]", createOp.PK)
	}

	// Verify the SQL generated from the parsed migration produces a complete
	// CREATE TABLE (not the empty fallback).
	generatedSQL := OpToSQL(*createOp)
	if strings.Contains(generatedSQL, "CREATE TABLE") && strings.Contains(generatedSQL, "();") {
		t.Errorf("got empty CREATE TABLE fallback; columns were lost during round trip.\nSQL: %s", generatedSQL)
	}
	for _, wc := range wantCols {
		if !strings.Contains(generatedSQL, wc.name) {
			t.Errorf("generated SQL missing column %q:\n%s", wc.name, generatedSQL)
		}
	}
}
