package migrate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestSquashMigrations_RequiresDB verifies the 0.6(d) stopgap: the exported
// SquashMigrations refuses to run without a database connection, since the
// M200 applied-version safety check is mandatory.
func TestSquashMigrations_RequiresDB(t *testing.T) {
	_, err := SquashMigrations(context.Background(), nil, t.TempDir(), "0.1.0", "0.2.0")
	if err == nil {
		t.Fatal("expected error when conn is nil (mandatory M200 check)")
	}
	if !strings.Contains(err.Error(), "database connection") {
		t.Errorf("expected error about the required database connection, got: %v", err)
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

	result, err := squashFiles(dir, "0.1.0", "0.2.0")
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
	_, err := squashFiles(dir, "0.2.0", "0.1.0")
	if err == nil {
		t.Fatal("expected error for from > to")
	}
}

func TestSquashMigrations_InvalidSemver(t *testing.T) {
	dir := t.TempDir()

	_, err := squashFiles(dir, "not-semver", "0.1.0")
	if err == nil {
		t.Fatal("expected error for invalid from")
	}

	_, err = squashFiles(dir, "0.1.0", "not-semver")
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

	_, err := squashFiles(dir, "0.1.0", "0.1.0")
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

	_, err := squashFiles(dir, "0.5.0", "0.9.0")
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

	result, err := squashFiles(dir, "0.2.0", "0.3.0")
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

	result, err := squashFiles(dir, "0.1.0", "0.2.0")
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

	result, err := squashFiles(dir, "0.1.0", "0.2.0")
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

func TestOutputPath(t *testing.T) {
	got := OutputPath("/migrations", "0.3.0")
	want := filepath.Join("/migrations", "0.3.0.toml")
	if got != want {
		t.Errorf("OutputPath = %q, want %q", got, want)
	}
}

func TestSquashMigrations_StripPhases(t *testing.T) {
	dir := t.TempDir()

	m1 := &Migration{
		Description: "Create table",
		DDLOps: []DDLOp{
			{Op: "create_table", Table: "public.t", Phase: "expand", PK: []string{"id"}, Comment: "T"},
		},
	}
	m2 := &Migration{
		Description: "Drop and backfill",
		DDLOps: []DDLOp{
			{Op: "drop_column", Table: "public.t", Column: "old", Phase: "contract"},
		},
		DMLOps: []DMLOp{
			{Op: "backfill", SQL: "UPDATE public.t SET x = 1", Phase: "migrate"},
		},
	}

	WriteMigrationFile(filepath.Join(dir, "0.1.0.toml"), m1)
	WriteMigrationFile(filepath.Join(dir, "0.2.0.toml"), m2)

	result, err := squashFiles(dir, "0.1.0", "0.2.0")
	if err != nil {
		t.Fatalf("SquashMigrations: %v", err)
	}

	for i, op := range result.Squashed.DDLOps {
		if op.Phase != "" {
			t.Errorf("DDLOps[%d].Phase = %q, want empty", i, op.Phase)
		}
	}
	for i, op := range result.Squashed.DMLOps {
		if op.Phase != "" {
			t.Errorf("DMLOps[%d].Phase = %q, want empty", i, op.Phase)
		}
	}
}

