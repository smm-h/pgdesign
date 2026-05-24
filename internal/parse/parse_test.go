package parse

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
)

func TestMinimalSchema(t *testing.T) {
	path := filepath.Join("testdata", "minimal.toml")
	schema, diags := File(path)
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	// Meta
	if schema.Meta.Version != 1 {
		t.Errorf("meta.version = %d, want 1", schema.Meta.Version)
	}
	if schema.Meta.Schema != "test" {
		t.Errorf("meta.schema = %q, want %q", schema.Meta.Schema, "test")
	}
	if len(schema.Meta.Extensions) != 1 || schema.Meta.Extensions[0] != "pgcrypto" {
		t.Errorf("meta.extensions = %v, want [pgcrypto]", schema.Meta.Extensions)
	}

	// Tables
	if len(schema.Tables) != 2 {
		t.Fatalf("len(tables) = %d, want 2", len(schema.Tables))
	}
	users := schema.Tables[0]
	posts := schema.Tables[1]

	if users.Name != "users" {
		t.Errorf("tables[0].name = %q, want %q", users.Name, "users")
	}
	if posts.Name != "posts" {
		t.Errorf("tables[1].name = %q, want %q", posts.Name, "posts")
	}

	// Users columns
	if len(users.Columns) != 5 {
		t.Fatalf("users columns = %d, want 5", len(users.Columns))
	}

	// Posts FK
	if len(posts.FKs) != 1 {
		t.Fatalf("posts FKs = %d, want 1", len(posts.FKs))
	}
	fk := posts.FKs["author_fk"]
	if fk.RefTable != "users" {
		t.Errorf("fk.ref_table = %q, want %q", fk.RefTable, "users")
	}
	if fk.OnDelete != "CASCADE" {
		t.Errorf("fk.on_delete = %q, want %q", fk.OnDelete, "CASCADE")
	}

	// Posts index
	if len(posts.Indexes) != 1 {
		t.Fatalf("posts indexes = %d, want 1", len(posts.Indexes))
	}
	idx := posts.Indexes["idx_author"]
	if len(idx.Columns) != 1 || idx.Columns[0] != "author_id" {
		t.Errorf("idx.columns = %v, want [author_id]", idx.Columns)
	}

	// Posts PK
	if len(posts.PK) != 1 || posts.PK[0] != "id" {
		t.Errorf("posts.pk = %v, want [id]", posts.PK)
	}
}

func TestEnumAndUserTypes(t *testing.T) {
	path := filepath.Join("testdata", "minimal.toml")
	schema, diags := File(path)
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Types) != 1 {
		t.Fatalf("len(types) = %d, want 1", len(schema.Types))
	}

	st := schema.Types[0]
	if st.Name != "status" {
		t.Errorf("type.name = %q, want %q", st.Name, "status")
	}
	if st.Kind != "enum" {
		t.Errorf("type.kind = %q, want %q", st.Kind, "enum")
	}
	if len(st.Values) != 2 || st.Values[0] != "active" || st.Values[1] != "inactive" {
		t.Errorf("type.values = %v, want [active inactive]", st.Values)
	}
	if st.Comment == nil || *st.Comment != "User status" {
		t.Errorf("type.comment = %v, want %q", st.Comment, "User status")
	}
}

func TestColumnOrderPreservation(t *testing.T) {
	path := filepath.Join("testdata", "minimal.toml")
	schema, diags := File(path)
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	// Users table columns must be in source order
	users := schema.Tables[0]
	expectedOrder := []string{"id", "name", "email", "status", "created_at"}
	if len(users.Columns) != len(expectedOrder) {
		t.Fatalf("users columns = %d, want %d", len(users.Columns), len(expectedOrder))
	}
	for i, want := range expectedOrder {
		if users.Columns[i].Name != want {
			t.Errorf("users.columns[%d].name = %q, want %q", i, users.Columns[i].Name, want)
		}
	}

	// Posts table columns must be in source order
	posts := schema.Tables[1]
	expectedPostsCols := []string{"id", "title", "author_id", "published"}
	if len(posts.Columns) != len(expectedPostsCols) {
		t.Fatalf("posts columns = %d, want %d", len(posts.Columns), len(expectedPostsCols))
	}
	for i, want := range expectedPostsCols {
		if posts.Columns[i].Name != want {
			t.Errorf("posts.columns[%d].name = %q, want %q", i, posts.Columns[i].Name, want)
		}
	}
}

func TestErrorRecovery(t *testing.T) {
	// Create a TOML with unknown keys and a missing type field
	content := `[meta]
version = 1
schema = "test"
unknown_key = "hello"

[tables.broken]
comment = "A broken table"

[tables.broken.columns.no_type]
nullable = true

[tables.broken.columns.valid]
type = "text"
weird_field = "what"
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "broken.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	schema, diags := File(path)
	if schema == nil {
		t.Fatalf("expected partial schema on error, got nil")
	}

	// Should still parse what it can
	if schema.Meta.Version != 1 {
		t.Errorf("meta.version = %d, want 1", schema.Meta.Version)
	}
	if schema.Meta.Schema != "test" {
		t.Errorf("meta.schema = %q, want %q", schema.Meta.Schema, "test")
	}

	// Should have the table with both columns
	if len(schema.Tables) != 1 {
		t.Fatalf("len(tables) = %d, want 1", len(schema.Tables))
	}
	tbl := schema.Tables[0]
	if len(tbl.Columns) != 2 {
		t.Fatalf("len(columns) = %d, want 2", len(tbl.Columns))
	}

	// Check diagnostics: expect warnings for unknown keys, error for missing type
	var warnings, errors int
	for _, d := range diags {
		switch d.Severity {
		case diagnostic.Warning:
			warnings++
		case diagnostic.Error:
			errors++
		}
	}

	// unknown_key in meta (1 warning), weird_field in column (1 warning)
	if warnings < 2 {
		t.Errorf("expected at least 2 warnings, got %d; diags: %v", warnings, diags)
	}

	// missing type on no_type column (1 error)
	if errors < 1 {
		t.Errorf("expected at least 1 error, got %d; diags: %v", errors, diags)
	}

	// Verify the missing-type column still has partial data
	noType := tbl.Columns[0]
	if noType.Name != "no_type" {
		t.Errorf("columns[0].name = %q, want %q", noType.Name, "no_type")
	}
	if noType.Nullable == nil || *noType.Nullable != true {
		t.Errorf("columns[0].nullable = %v, want true", noType.Nullable)
	}
	if noType.Type != "" {
		t.Errorf("columns[0].type = %q, want empty", noType.Type)
	}
}

func TestMultiSection(t *testing.T) {
	// Verify that meta, types, and tables are all parsed from the same file
	path := filepath.Join("testdata", "minimal.toml")
	schema, diags := File(path)
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	// All three sections populated
	if schema.Meta.Version == 0 && schema.Meta.Schema == "" {
		t.Error("meta section not parsed")
	}
	if len(schema.Types) == 0 {
		t.Error("types section not parsed")
	}
	if len(schema.Tables) == 0 {
		t.Error("tables section not parsed")
	}

	// Verify types reference works: the users table has a status column
	// that references the status type
	users := schema.Tables[0]
	var statusCol *RawColumn
	for i := range users.Columns {
		if users.Columns[i].Name == "status" {
			statusCol = &users.Columns[i]
			break
		}
	}
	if statusCol == nil {
		t.Fatal("users table missing status column")
	}
	if statusCol.Type != "status" {
		t.Errorf("status column type = %q, want %q", statusCol.Type, "status")
	}

	// The enum type should exist
	if schema.Types[0].Name != "status" {
		t.Errorf("types[0].name = %q, want %q", schema.Types[0].Name, "status")
	}
}

func TestFileNotFound(t *testing.T) {
	schema, diags := File("nonexistent.toml")
	if schema != nil {
		t.Error("expected nil schema for missing file")
	}
	if len(diags) != 1 || diags[0].Severity != diagnostic.Error {
		t.Errorf("expected 1 error diagnostic, got %v", diags)
	}
}

func TestInvalidTOML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "bad.toml")
	if err := os.WriteFile(path, []byte("this is not valid [ toml"), 0644); err != nil {
		t.Fatal(err)
	}

	schema, diags := File(path)
	if schema != nil {
		t.Error("expected nil schema for invalid TOML")
	}
	if len(diags) != 1 || diags[0].Severity != diagnostic.Error {
		t.Errorf("expected 1 error diagnostic, got %v", diags)
	}
}

// hasFatalErrors returns true if any diagnostic is an error (not warning/info).
func hasFatalErrors(diags []diagnostic.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == diagnostic.Error {
			return true
		}
	}
	return false
}
