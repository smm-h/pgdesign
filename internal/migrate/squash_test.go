package migrate

import (
	"path/filepath"
	"testing"
)

func TestSquashMigrations_Basic(t *testing.T) {
	dir := t.TempDir()

	// Create three migration files.
	m1 := &Migration{
		Description: "Create users table",
		DDLOps: []DDLOp{
			{
				Op:      "create_table",
				Table:   "public.users",
				PK:      []string{"id"},
				Comment: "User accounts",
				Down:    &DownOp{Ops: []DDLOp{{Op: "drop_table", Table: "public.users"}}},
			},
		},
	}
	m2 := &Migration{
		Description: "Add email column",
		DDLOps: []DDLOp{
			{
				Op:     "add_column",
				Table:  "public.users",
				Column: "email",
				Type:   "text",
				Down:   &DownOp{Ops: []DDLOp{{Op: "drop_column", Table: "public.users", Column: "email"}}},
			},
		},
	}
	m3 := &Migration{
		Description: "Add name column",
		DDLOps: []DDLOp{
			{
				Op:     "add_column",
				Table:  "public.users",
				Column: "name",
				Type:   "text",
				Down:   &DownOp{Ops: []DDLOp{{Op: "drop_column", Table: "public.users", Column: "name"}}},
			},
		},
	}

	WriteMigrationFile(filepath.Join(dir, "0.1.0.toml"), m1)
	WriteMigrationFile(filepath.Join(dir, "0.2.0.toml"), m2)
	WriteMigrationFile(filepath.Join(dir, "0.3.0.toml"), m3)

	result, err := SquashMigrations(dir, "0.1.0", "0.3.0")
	if err != nil {
		t.Fatalf("SquashMigrations: %v", err)
	}

	if result.OriginalCount != 3 {
		t.Errorf("OriginalCount = %d, want 3", result.OriginalCount)
	}
	if result.Squashed.Version != "0.3.0" {
		t.Errorf("Version = %q, want %q", result.Squashed.Version, "0.3.0")
	}
	if result.Squashed.Description != "Squashed from 0.1.0 to 0.3.0" {
		t.Errorf("Description = %q, want %q", result.Squashed.Description, "Squashed from 0.1.0 to 0.3.0")
	}
	// Should have 3 DDL ops (create_table + 2 add_column).
	if len(result.Squashed.DDLOps) != 3 {
		t.Fatalf("DDL ops = %d, want 3", len(result.Squashed.DDLOps))
	}
	if result.Squashed.DDLOps[0].Op != "create_table" {
		t.Errorf("DDL[0].Op = %q, want create_table", result.Squashed.DDLOps[0].Op)
	}
	if result.Squashed.DDLOps[1].Op != "add_column" {
		t.Errorf("DDL[1].Op = %q, want add_column", result.Squashed.DDLOps[1].Op)
	}
	if result.Squashed.DDLOps[2].Op != "add_column" {
		t.Errorf("DDL[2].Op = %q, want add_column", result.Squashed.DDLOps[2].Op)
	}
	if result.CancelledPairs != 0 {
		t.Errorf("CancelledPairs = %d, want 0", result.CancelledPairs)
	}
}

func TestSquashMigrations_InversePairCancellation(t *testing.T) {
	dir := t.TempDir()

	// Migration 1: add column. Migration 2: drop same column.
	m1 := &Migration{
		Description: "Add temp column",
		DDLOps: []DDLOp{
			{
				Op:     "add_column",
				Table:  "public.users",
				Column: "temp",
				Type:   "text",
				Down:   &DownOp{Ops: []DDLOp{{Op: "drop_column", Table: "public.users", Column: "temp"}}},
			},
		},
	}
	m2 := &Migration{
		Description: "Drop temp column",
		DDLOps: []DDLOp{
			{
				Op:     "drop_column",
				Table:  "public.users",
				Column: "temp",
				Down:   &DownOp{Ops: []DDLOp{{Op: "add_column", Table: "public.users", Column: "temp"}}},
			},
		},
	}

	WriteMigrationFile(filepath.Join(dir, "0.1.0.toml"), m1)
	WriteMigrationFile(filepath.Join(dir, "0.2.0.toml"), m2)

	result, err := SquashMigrations(dir, "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("SquashMigrations: %v", err)
	}

	if result.CancelledPairs != 1 {
		t.Errorf("CancelledPairs = %d, want 1", result.CancelledPairs)
	}
	if len(result.Squashed.DDLOps) != 0 {
		t.Errorf("DDL ops = %d, want 0 (both should be cancelled)", len(result.Squashed.DDLOps))
	}
}

func TestSquashMigrations_MergeAlterColumnType(t *testing.T) {
	dir := t.TempDir()

	// Two type changes on the same column -- should keep only the final.
	m1 := &Migration{
		Description: "Change type to varchar",
		DDLOps: []DDLOp{
			{
				Op:     "alter_column_type",
				Table:  "public.users",
				Column: "name",
				Type:   "varchar(100)",
				Down:   &DownOp{Ops: []DDLOp{{Op: "alter_column_type", Table: "public.users", Column: "name", Type: "text"}}},
			},
		},
	}
	m2 := &Migration{
		Description: "Change type to varchar(255)",
		DDLOps: []DDLOp{
			{
				Op:     "alter_column_type",
				Table:  "public.users",
				Column: "name",
				Type:   "varchar(255)",
				Down:   &DownOp{Ops: []DDLOp{{Op: "alter_column_type", Table: "public.users", Column: "name", Type: "varchar(100)"}}},
			},
		},
	}

	WriteMigrationFile(filepath.Join(dir, "0.1.0.toml"), m1)
	WriteMigrationFile(filepath.Join(dir, "0.2.0.toml"), m2)

	result, err := SquashMigrations(dir, "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("SquashMigrations: %v", err)
	}

	if result.MergedOps != 1 {
		t.Errorf("MergedOps = %d, want 1", result.MergedOps)
	}
	if len(result.Squashed.DDLOps) != 1 {
		t.Fatalf("DDL ops = %d, want 1", len(result.Squashed.DDLOps))
	}
	if result.Squashed.DDLOps[0].Type != "varchar(255)" {
		t.Errorf("final type = %q, want %q", result.Squashed.DDLOps[0].Type, "varchar(255)")
	}
}

func TestSquashMigrations_IrreversiblePropagation(t *testing.T) {
	dir := t.TempDir()

	m1 := &Migration{
		Description: "Add column",
		DDLOps: []DDLOp{
			{
				Op:     "add_column",
				Table:  "public.users",
				Column: "email",
				Type:   "text",
				Down:   &DownOp{Ops: []DDLOp{{Op: "drop_column", Table: "public.users", Column: "email"}}},
			},
		},
	}
	m2 := &Migration{
		Description: "Drop table",
		DDLOps: []DDLOp{
			{
				Op:    "drop_table",
				Table: "public.legacy",
				Down:  &DownOp{Irreversible: true},
			},
		},
	}

	WriteMigrationFile(filepath.Join(dir, "0.1.0.toml"), m1)
	WriteMigrationFile(filepath.Join(dir, "0.2.0.toml"), m2)

	result, err := SquashMigrations(dir, "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("SquashMigrations: %v", err)
	}

	// All down ops should be marked irreversible.
	for i, op := range result.Squashed.DDLOps {
		if op.Down == nil {
			t.Errorf("DDL[%d] has no down op", i)
		} else if !op.Down.Irreversible {
			t.Errorf("DDL[%d] should be irreversible (propagated from drop_table)", i)
		}
	}
}

func TestSquashMigrations_InvalidRange(t *testing.T) {
	dir := t.TempDir()

	// from > to
	_, err := SquashMigrations(dir, "0.2.0", "0.1.0")
	if err == nil {
		t.Fatal("expected error for from > to")
	}
}

func TestSquashMigrations_InvalidSemver(t *testing.T) {
	dir := t.TempDir()

	_, err := SquashMigrations(dir, "not-semver", "0.1.0")
	if err == nil {
		t.Fatal("expected error for invalid from")
	}

	_, err = SquashMigrations(dir, "0.1.0", "not-semver")
	if err == nil {
		t.Fatal("expected error for invalid to")
	}
}

func TestSquashMigrations_SingleMigration(t *testing.T) {
	dir := t.TempDir()

	m := &Migration{
		Description: "Only one",
		DDLOps: []DDLOp{
			{Op: "create_table", Table: "public.t"},
		},
	}
	WriteMigrationFile(filepath.Join(dir, "0.1.0.toml"), m)

	_, err := SquashMigrations(dir, "0.1.0", "0.1.0")
	if err == nil {
		t.Fatal("expected error for single migration")
	}
}

func TestSquashMigrations_NoMigrationsInRange(t *testing.T) {
	dir := t.TempDir()

	m := &Migration{
		Description: "v0.1.0",
		DDLOps: []DDLOp{
			{Op: "create_table", Table: "public.t"},
		},
	}
	WriteMigrationFile(filepath.Join(dir, "0.1.0.toml"), m)

	_, err := SquashMigrations(dir, "0.5.0", "0.9.0")
	if err == nil {
		t.Fatal("expected error for no migrations in range")
	}
}

func TestSquashMigrations_PreservesOutOfRangeMigrations(t *testing.T) {
	dir := t.TempDir()

	// Create 4 migrations, squash the middle 2.
	for _, v := range []string{"0.1.0", "0.2.0", "0.3.0", "0.4.0"} {
		m := &Migration{
			Description: "v" + v,
			DDLOps: []DDLOp{
				{
					Op:     "add_column",
					Table:  "public.t",
					Column: "col_" + v,
					Type:   "text",
					Down:   &DownOp{Ops: []DDLOp{{Op: "drop_column", Table: "public.t", Column: "col_" + v}}},
				},
			},
		}
		WriteMigrationFile(filepath.Join(dir, v+".toml"), m)
	}

	result, err := SquashMigrations(dir, "0.2.0", "0.3.0")
	if err != nil {
		t.Fatalf("SquashMigrations: %v", err)
	}

	if result.OriginalCount != 2 {
		t.Errorf("OriginalCount = %d, want 2", result.OriginalCount)
	}
	if len(result.OriginalPaths) != 2 {
		t.Errorf("OriginalPaths len = %d, want 2", len(result.OriginalPaths))
	}

	// The squashed migration should have 2 add_column ops.
	if len(result.Squashed.DDLOps) != 2 {
		t.Errorf("DDL ops = %d, want 2", len(result.Squashed.DDLOps))
	}
}

func TestSquashMigrations_WithDMLOps(t *testing.T) {
	dir := t.TempDir()

	m1 := &Migration{
		Description: "Add column",
		DDLOps: []DDLOp{
			{
				Op:     "add_column",
				Table:  "public.users",
				Column: "level",
				Type:   "integer",
				Down:   &DownOp{Ops: []DDLOp{{Op: "drop_column", Table: "public.users", Column: "level"}}},
			},
		},
		DMLOps: []DMLOp{
			{
				Op:  "backfill",
				SQL: "UPDATE public.users SET level = 1",
			},
		},
	}
	m2 := &Migration{
		Description: "Add index",
		DDLOps: []DDLOp{
			{
				Op:      "create_index",
				Table:   "public.users",
				Name:    "idx_users_level",
				Columns: []string{"level"},
				Down:    &DownOp{Ops: []DDLOp{{Op: "drop_index", Name: "idx_users_level"}}},
			},
		},
	}

	WriteMigrationFile(filepath.Join(dir, "0.1.0.toml"), m1)
	WriteMigrationFile(filepath.Join(dir, "0.2.0.toml"), m2)

	result, err := SquashMigrations(dir, "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("SquashMigrations: %v", err)
	}

	if len(result.Squashed.DDLOps) != 2 {
		t.Errorf("DDL ops = %d, want 2", len(result.Squashed.DDLOps))
	}
	if len(result.Squashed.DMLOps) != 1 {
		t.Errorf("DML ops = %d, want 1", len(result.Squashed.DMLOps))
	}
}

func TestSquashMigrations_RoundTrip(t *testing.T) {
	// Test that the squashed migration can be written and re-read.
	dir := t.TempDir()

	m1 := &Migration{
		Description: "Create table",
		DDLOps: []DDLOp{
			{
				Op:      "create_table",
				Table:   "public.items",
				PK:      []string{"id"},
				Comment: "Items table",
				Down:    &DownOp{Ops: []DDLOp{{Op: "drop_table", Table: "public.items"}}},
			},
		},
	}
	m2 := &Migration{
		Description: "Add price",
		DDLOps: []DDLOp{
			{
				Op:      "add_column",
				Table:   "public.items",
				Column:  "price",
				Type:    "numeric(10,2)",
				Default: int64(0),
				Down:    &DownOp{Ops: []DDLOp{{Op: "drop_column", Table: "public.items", Column: "price"}}},
			},
		},
	}

	WriteMigrationFile(filepath.Join(dir, "0.1.0.toml"), m1)
	WriteMigrationFile(filepath.Join(dir, "0.2.0.toml"), m2)

	result, err := SquashMigrations(dir, "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("SquashMigrations: %v", err)
	}

	outPath := filepath.Join(dir, "squashed.toml")
	if err := WriteMigrationFile(outPath, result.Squashed); err != nil {
		t.Fatalf("WriteMigrationFile: %v", err)
	}

	parsed, err := ParseMigrationFile(outPath)
	if err != nil {
		t.Fatalf("ParseMigrationFile: %v", err)
	}

	if parsed.Description != result.Squashed.Description {
		t.Errorf("description = %q, want %q", parsed.Description, result.Squashed.Description)
	}
	if len(parsed.DDLOps) != len(result.Squashed.DDLOps) {
		t.Errorf("DDL ops = %d, want %d", len(parsed.DDLOps), len(result.Squashed.DDLOps))
	}
}

func TestIsInversePair(t *testing.T) {
	tests := []struct {
		name   string
		op1    DDLOp
		op2    DDLOp
		expect bool
	}{
		{
			name:   "add_column/drop_column same target",
			op1:    DDLOp{Op: "add_column", Table: "t", Column: "c"},
			op2:    DDLOp{Op: "drop_column", Table: "t", Column: "c"},
			expect: true,
		},
		{
			name:   "add_column/drop_column different column",
			op1:    DDLOp{Op: "add_column", Table: "t", Column: "c1"},
			op2:    DDLOp{Op: "drop_column", Table: "t", Column: "c2"},
			expect: false,
		},
		{
			name:   "create_table/drop_table same table",
			op1:    DDLOp{Op: "create_table", Table: "t"},
			op2:    DDLOp{Op: "drop_table", Table: "t"},
			expect: true,
		},
		{
			name:   "create_table/drop_table different table",
			op1:    DDLOp{Op: "create_table", Table: "t1"},
			op2:    DDLOp{Op: "drop_table", Table: "t2"},
			expect: false,
		},
		{
			name:   "create_index/drop_index same name",
			op1:    DDLOp{Op: "create_index", Name: "idx"},
			op2:    DDLOp{Op: "drop_index", Name: "idx"},
			expect: true,
		},
		{
			name:   "set_not_null/drop_not_null same target",
			op1:    DDLOp{Op: "set_not_null", Table: "t", Column: "c"},
			op2:    DDLOp{Op: "drop_not_null", Table: "t", Column: "c"},
			expect: true,
		},
		{
			name:   "not an inverse pair",
			op1:    DDLOp{Op: "add_column", Table: "t", Column: "c"},
			op2:    DDLOp{Op: "create_table", Table: "t"},
			expect: false,
		},
		{
			name:   "reverse direction is not detected",
			op1:    DDLOp{Op: "drop_column", Table: "t", Column: "c"},
			op2:    DDLOp{Op: "add_column", Table: "t", Column: "c"},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInversePair(tt.op1, tt.op2)
			if got != tt.expect {
				t.Errorf("isInversePair(%s, %s) = %v, want %v", tt.op1.Op, tt.op2.Op, got, tt.expect)
			}
		})
	}
}

func TestOptimizeDDLOps(t *testing.T) {
	// Mix of ops: add+drop same column (cancel), two type changes (merge), plus a surviving add.
	ops := []DDLOp{
		{Op: "add_column", Table: "t", Column: "temp", Type: "text"},
		{Op: "alter_column_type", Table: "t", Column: "name", Type: "varchar(50)"},
		{Op: "add_column", Table: "t", Column: "email", Type: "text"},
		{Op: "drop_column", Table: "t", Column: "temp"},
		{Op: "alter_column_type", Table: "t", Column: "name", Type: "varchar(255)"},
	}

	result, cancelled, merged := optimizeDDLOps(ops)

	if cancelled != 1 {
		t.Errorf("cancelled = %d, want 1", cancelled)
	}
	if merged != 1 {
		t.Errorf("merged = %d, want 1", merged)
	}
	// Should have: add_column(email) + alter_column_type(name, varchar(255))
	if len(result) != 2 {
		t.Fatalf("result len = %d, want 2; ops: %v", len(result), opsDebug(result))
	}
	if result[0].Op != "add_column" || result[0].Column != "email" {
		t.Errorf("result[0] = %s %s, want add_column email", result[0].Op, result[0].Column)
	}
	if result[1].Op != "alter_column_type" || result[1].Type != "varchar(255)" {
		t.Errorf("result[1] = %s %s, want alter_column_type varchar(255)", result[1].Op, result[1].Type)
	}
}

func TestOutputPath(t *testing.T) {
	got := OutputPath("/migrations", "0.3.0")
	want := filepath.Join("/migrations", "0.3.0.toml")
	if got != want {
		t.Errorf("OutputPath = %q, want %q", got, want)
	}
}
