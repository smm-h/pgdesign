package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "level", PGType: "integer", NotNull: true, Default: model.StrPtr("1")},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsAdded: []model.Column{
					{Name: "level", PGType: "integer", NotNull: true, Default: model.StrPtr("1")},
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
			if op.Type != "integer" {
				t.Errorf("add_column type = %q, want %q", op.Type, "integer")
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "level", PGType: "integer", NotNull: true, Default: model.StrPtr("1")},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsAdded: []model.Column{
					{Name: "level", PGType: "integer", NotNull: true, Default: model.StrPtr("1")},
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "level", PGType: "integer", NotNull: true, Default: model.StrPtr("1")},
				},
			},
		},
	}

	d := &diff.SchemaDiff{
		TablesChanged: []diff.TableDiff{
			{
				Name: "game.players",
				ColumnsAdded: []model.Column{
					{Name: "level", PGType: "integer", NotNull: true, Default: model.StrPtr("1")},
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "created_at", PGType: "timestamptz", NotNull: true},
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "created_at", PGType: "timestamptz", NotNull: true},
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

	// Should have a warning about strategy change.
	hasWarning := false
	for _, diag := range diags {
		if diag.Code == "PARTITION_STRATEGY_CHANGE" {
			hasWarning = true
			if !strings.Contains(diag.Message, "requires table rebuild") {
				t.Errorf("expected 'requires table rebuild' in message, got: %s", diag.Message)
			}
			break
		}
	}
	if !hasWarning {
		t.Error("expected PARTITION_STRATEGY_CHANGE diagnostic")
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

func TestOpToSQL_CreateTable(t *testing.T) {
	table := &model.Table{
		Name:   "players",
		Schema: "game",
		PK:     []string{"id"},
		Columns: []model.Column{
			{Name: "id", PGType: "bigint", NotNull: true},
			{Name: "name", PGType: "text", NotNull: true},
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
		op   string
		want bool
	}{
		{"create_index_concurrently", true},
		{"drop_index_concurrently", true},
		{"alter_enum_add_value", true},
		{"create_table", false},
		{"add_column", false},
		{"create_index", false},
	}
	for _, tt := range tests {
		got := IsNonTransactional(DDLOp{Op: tt.op})
		if got != tt.want {
			t.Errorf("IsNonTransactional(%q) = %v, want %v", tt.op, got, tt.want)
		}
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

func getTestConnStr() string {
	connStr := os.Getenv("PGDESIGN_TEST_DB")
	if connStr == "" {
		connStr = "postgres://localhost:5432/pgdesign_test?sslmode=disable"
	}
	return connStr
}

func connectTestDB(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, getTestConnStr())
	if err != nil {
		t.Skipf("Skipping integration test: cannot connect to PostgreSQL: %v", err)
	}
	return conn
}

func TestIntegration_StateTracking(t *testing.T) {
	conn := connectTestDB(t)
	ctx := context.Background()
	defer conn.Close(ctx)

	// Clean up before test.
	conn.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_migrations")

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

	// Clean up.
	conn.Exec(ctx, "DROP TABLE pgdesign_migrations")
}

func TestIntegration_AdvisoryLock(t *testing.T) {
	conn := connectTestDB(t)
	ctx := context.Background()
	defer conn.Close(ctx)

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
	conn := connectTestDB(t)
	ctx := context.Background()
	defer conn.Close(ctx)

	// Clean up.
	conn.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_test_table")
	conn.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_migrations")

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

	// Clean up.
	conn.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_migrations")
}

func TestIntegration_ApplyIdempotent(t *testing.T) {
	conn := connectTestDB(t)
	ctx := context.Background()
	defer conn.Close(ctx)

	// Clean up.
	conn.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_test_table2")
	conn.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_migrations")

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

	// Clean up.
	conn.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_test_table2")
	conn.Exec(ctx, "DROP TABLE IF EXISTS pgdesign_migrations")
}

func TestAppendOnlyMigration(t *testing.T) {
	desired := &model.Schema{
		Tables: []model.Table{
			{
				Name:       "events",
				Schema:     "app",
				Columns:    []model.Column{{Name: "id", PGType: "uuid", NotNull: true}},
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
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

func TestGenerateMigration_E300_AddFKLargeTable(t *testing.T) {
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "scores",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "player_id", PGType: "bigint", NotNull: true},
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
	_, diags := GenerateMigration(d, desired, "0.8.0", stats, 10_000, 0, extregistry.NewBuiltinRegistry())

	hasE300 := false
	for _, diag := range diags {
		if diag.Code == "E300" {
			hasE300 = true
			if !strings.Contains(diag.Message, "50000") {
				t.Errorf("E300 message should mention row count, got: %s", diag.Message)
			}
			if !strings.Contains(diag.Message, "NOT VALID") {
				t.Errorf("E300 message should mention NOT VALID, got: %s", diag.Message)
			}
			break
		}
	}
	if !hasE300 {
		t.Error("expected E300 diagnostic for add_fk on table with >10000 rows")
	}
}

func TestGenerateMigration_NoStats_NoE300_NoEscalation(t *testing.T) {
	desired := &model.Schema{
		Name:      "game",
		PGVersion: 17,
		Tables: []model.Table{
			{
				Name:   "scores",
				Schema: "game",
				Columns: []model.Column{
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "player_id", PGType: "bigint", NotNull: true},
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true, Default: model.StrPtr("'unknown'")},
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true, Default: model.StrPtr("'unknown'")},
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
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "name", PGType: "text", NotNull: true},
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
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "tags", PGType: "text", NotNull: true, Array: true},
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
					{Name: "id", PGType: "integer", NotNull: true},
					{Name: "tags", PGType: "text", NotNull: true, Array: false},
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

	// PG 0 + stored=false -> VIRTUAL
	op.PGVersion = 0
	got = OpToSQL(op)
	if !strings.Contains(got, "VIRTUAL") {
		t.Errorf("PG0 + stored=false: expected VIRTUAL in: %s", got)
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
		{false, 17, "STORED"},  // pre-PG18 defensive
		{false, 12, "STORED"},  // pre-PG18 defensive
		{false, 0, "VIRTUAL"},  // unspecified, respect user choice
	}

	for _, tt := range tests {
		got := generatedStorageKeyword(tt.stored, tt.pgVersion)
		if got != tt.want {
			t.Errorf("generatedStorageKeyword(stored=%v, pg=%d) = %q, want %q",
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "content", PGType: "text", Collation: "de_DE"},
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text", Statistics: &v},
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
					{Name: "id", PGType: "bigint", NotNull: true},
					{Name: "name", PGType: "text"},
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
				Args:       []model.FunctionArg{{Name: "amount", Type: "numeric"}},
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
				Args:       []model.FunctionArg{{Name: "x", Type: "numeric"}},
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
				Args:       []model.FunctionArg{{Name: "x", Type: "numeric"}, {Name: "y", Type: "numeric"}},
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
