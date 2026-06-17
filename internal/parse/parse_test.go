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

func TestFiles(t *testing.T) {
	paths := []string{
		filepath.Join("testdata", "multi", "auth.toml"),
		filepath.Join("testdata", "multi", "game.toml"),
	}
	schemas, diags := Files(paths)
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if len(schemas) != 2 {
		t.Fatalf("expected 2 schemas, got %d", len(schemas))
	}

	// First schema should be auth.
	if schemas[0].Meta.Schema != "auth" {
		t.Errorf("schemas[0].meta.schema = %q, want %q", schemas[0].Meta.Schema, "auth")
	}
	if len(schemas[0].Tables) != 1 {
		t.Errorf("auth schema tables = %d, want 1", len(schemas[0].Tables))
	}

	// Second schema should be game.
	if schemas[1].Meta.Schema != "game" {
		t.Errorf("schemas[1].meta.schema = %q, want %q", schemas[1].Meta.Schema, "game")
	}
	if len(schemas[1].Tables) != 1 {
		t.Errorf("game schema tables = %d, want 1", len(schemas[1].Tables))
	}

	// Game schema's players table should have a cross-schema FK.
	players := schemas[1].Tables[0]
	if players.Name != "players" {
		t.Errorf("game table name = %q, want %q", players.Name, "players")
	}
	fk, ok := players.FKs["fk_players_auth"]
	if !ok {
		t.Fatal("expected FK fk_players_auth on players table")
	}
	if fk.RefTable != "auth.users" {
		t.Errorf("fk.ref_table = %q, want %q", fk.RefTable, "auth.users")
	}
}

func TestDir(t *testing.T) {
	schemas, diags := Dir(filepath.Join("testdata", "multi"))
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if len(schemas) != 2 {
		t.Fatalf("expected 2 schemas, got %d", len(schemas))
	}

	// Schemas should be alphabetically ordered by filename (auth.toml, game.toml).
	schemaNames := make([]string, len(schemas))
	for i, s := range schemas {
		schemaNames[i] = s.Meta.Schema
	}
	if schemaNames[0] != "auth" || schemaNames[1] != "game" {
		t.Errorf("schema names = %v, want [auth game]", schemaNames)
	}
}

func TestDirExcludesPgdesignToml(t *testing.T) {
	// Create a temp dir with pgdesign.toml and a schema file.
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "pgdesign.toml"), []byte(`[project]
schemas = ["s.toml"]
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "s.toml"), []byte(`[meta]
version = 1
schema = "test"
`), 0644); err != nil {
		t.Fatal(err)
	}

	schemas, diags := Dir(tmpDir)
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema (pgdesign.toml excluded), got %d", len(schemas))
	}
	if schemas[0].Meta.Schema != "test" {
		t.Errorf("schema name = %q, want %q", schemas[0].Meta.Schema, "test")
	}
}

func TestFilesWithMissingFile(t *testing.T) {
	paths := []string{
		filepath.Join("testdata", "multi", "auth.toml"),
		filepath.Join("testdata", "multi", "nonexistent.toml"),
	}
	schemas, diags := Files(paths)
	// Should still return the valid schema.
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema (one file missing), got %d", len(schemas))
	}
	if schemas[0].Meta.Schema != "auth" {
		t.Errorf("schema name = %q, want %q", schemas[0].Meta.Schema, "auth")
	}
	// Should have an error diagnostic for the missing file.
	var hasError bool
	for _, d := range diags {
		if d.Severity == diagnostic.Error && d.Code == "E001" {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Error("expected E001 error for missing file")
	}
}

func TestOpclassSingleString(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.docs]
pk = ["id"]

[tables.docs.columns.id]
type = "auto_id"

[tables.docs.columns.content]
type = "text"

[tables.docs.indexes.idx_content]
columns = ["content"]
method = "gin"
opclass = "gin_trgm_ops"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	idx := schema.Tables[0].Indexes["idx_content"]
	if idx.Opclass == nil || *idx.Opclass != "gin_trgm_ops" {
		t.Errorf("idx.Opclass = %v, want %q", idx.Opclass, "gin_trgm_ops")
	}
	if idx.OpclassMap != nil {
		t.Errorf("idx.OpclassMap should be nil for single string, got %v", idx.OpclassMap)
	}
}

func TestOpclassPerColumnMap(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.docs]
pk = ["id"]

[tables.docs.columns.id]
type = "auto_id"

[tables.docs.columns.title]
type = "text"

[tables.docs.columns.body]
type = "text"

[tables.docs.indexes.idx_search]
columns = ["title", "body"]
method = "gin"
opclass = { title = "gin_trgm_ops", body = "gin_trgm_ops" }
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	idx := schema.Tables[0].Indexes["idx_search"]
	if idx.Opclass != nil {
		t.Errorf("idx.Opclass should be nil for map syntax, got %v", idx.Opclass)
	}
	if idx.OpclassMap == nil {
		t.Fatal("idx.OpclassMap should not be nil")
	}
	if idx.OpclassMap["title"] != "gin_trgm_ops" {
		t.Errorf("OpclassMap[title] = %q, want %q", idx.OpclassMap["title"], "gin_trgm_ops")
	}
	if idx.OpclassMap["body"] != "gin_trgm_ops" {
		t.Errorf("OpclassMap[body] = %q, want %q", idx.OpclassMap["body"], "gin_trgm_ops")
	}
}

func TestOpclassPerColumnMixed(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.docs]
pk = ["id"]

[tables.docs.columns.id]
type = "auto_id"

[tables.docs.columns.name]
type = "text"

[tables.docs.columns.code]
type = "text"

[tables.docs.indexes.idx_mixed]
columns = ["name", "code"]
opclass = { name = "varchar_pattern_ops", code = "text_ops" }
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	idx := schema.Tables[0].Indexes["idx_mixed"]
	if idx.OpclassMap == nil {
		t.Fatal("idx.OpclassMap should not be nil")
	}
	if idx.OpclassMap["name"] != "varchar_pattern_ops" {
		t.Errorf("OpclassMap[name] = %q, want %q", idx.OpclassMap["name"], "varchar_pattern_ops")
	}
	if idx.OpclassMap["code"] != "text_ops" {
		t.Errorf("OpclassMap[code] = %q, want %q", idx.OpclassMap["code"], "text_ops")
	}
}

func TestPolicies(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.messages]
pk = ["id"]
comment = "Messages table"
enable_rls = true

[tables.messages.columns.id]
type = "id"

[tables.messages.columns.channel_id]
type = "ref"

[tables.messages.columns.body]
type = "text"

[tables.messages.policies.select_own]
for = "SELECT"
to = "game_app"
using = "channel_id = current_setting('app.channel_id')::uuid"

[tables.messages.policies.insert_own]
for = "INSERT"
to = "game_app"
with_check = "channel_id = current_setting('app.channel_id')::uuid"
error_code = "chat_disabled"
error_message = "You cannot send messages to this channel"

[tables.messages.policies.update_own]
for = "UPDATE"
to = "game_app"
using = "channel_id = current_setting('app.channel_id')::uuid"
with_check = "channel_id = current_setting('app.channel_id')::uuid"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}
	tbl := schema.Tables[0]

	if !tbl.EnableRLS {
		t.Error("expected EnableRLS = true")
	}

	if len(tbl.Policies) != 3 {
		t.Fatalf("expected 3 policies, got %d", len(tbl.Policies))
	}

	// Check select_own policy.
	sel, ok := tbl.Policies["select_own"]
	if !ok {
		t.Fatal("expected policy 'select_own'")
	}
	if sel.For != "SELECT" {
		t.Errorf("select_own.For = %q, want %q", sel.For, "SELECT")
	}
	if sel.To != "game_app" {
		t.Errorf("select_own.To = %q, want %q", sel.To, "game_app")
	}
	if sel.Using == "" {
		t.Error("select_own.Using should not be empty")
	}

	// Check insert_own policy.
	ins, ok := tbl.Policies["insert_own"]
	if !ok {
		t.Fatal("expected policy 'insert_own'")
	}
	if ins.For != "INSERT" {
		t.Errorf("insert_own.For = %q, want %q", ins.For, "INSERT")
	}
	if ins.WithCheck == "" {
		t.Error("insert_own.WithCheck should not be empty")
	}
	if ins.ErrorCode != "chat_disabled" {
		t.Errorf("insert_own.ErrorCode = %q, want %q", ins.ErrorCode, "chat_disabled")
	}
	if ins.ErrorMessage != "You cannot send messages to this channel" {
		t.Errorf("insert_own.ErrorMessage = %q, want %q", ins.ErrorMessage, "You cannot send messages to this channel")
	}

	// Check update_own policy.
	upd, ok := tbl.Policies["update_own"]
	if !ok {
		t.Fatal("expected policy 'update_own'")
	}
	if upd.For != "UPDATE" {
		t.Errorf("update_own.For = %q, want %q", upd.For, "UPDATE")
	}
	if upd.Using == "" {
		t.Error("update_own.Using should not be empty")
	}
	if upd.WithCheck == "" {
		t.Error("update_own.WithCheck should not be empty")
	}
}

func TestArrayColumn(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[types.tags]
kind = "scalar"
base_type = "text"
array = true

[tables.posts]
pk = ["id"]
comment = "Posts table"

[tables.posts.columns.id]
type = "auto_id"

[tables.posts.columns.tags]
type = "text"
array = true

[tables.posts.columns.scores]
type = "integer"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	// Type-level array
	if len(schema.Types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(schema.Types))
	}
	tagsType := schema.Types[0]
	if tagsType.Array == nil || !*tagsType.Array {
		t.Errorf("expected type tags.array = true, got %v", tagsType.Array)
	}

	// Column-level array
	posts := schema.Tables[0]
	var tagsCol, scoresCol *RawColumn
	for i := range posts.Columns {
		switch posts.Columns[i].Name {
		case "tags":
			tagsCol = &posts.Columns[i]
		case "scores":
			scoresCol = &posts.Columns[i]
		}
	}
	if tagsCol == nil {
		t.Fatal("expected tags column")
	}
	if tagsCol.Array == nil || !*tagsCol.Array {
		t.Errorf("expected tags column array = true, got %v", tagsCol.Array)
	}
	if scoresCol == nil {
		t.Fatal("expected scores column")
	}
	if scoresCol.Array != nil {
		t.Errorf("expected scores column array = nil, got %v", scoresCol.Array)
	}
}

func TestAppendOnly(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.events]
pk = ["id"]
comment = "Append-only event log"
append_only = true

[tables.events.columns.id]
type = "auto_id"

[tables.events.columns.payload]
type = "text"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}
	tbl := schema.Tables[0]
	if tbl.AppendOnly == nil {
		t.Fatal("expected AppendOnly to be non-nil")
	}
	if !*tbl.AppendOnly {
		t.Error("expected AppendOnly = true")
	}
}

func TestJSONSchemaAttribute(t *testing.T) {
	path := filepath.Join("testdata", "json_schema.toml")
	schema, diags := File(path)
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Tables) != 1 {
		t.Fatalf("len(tables) = %d, want 1", len(schema.Tables))
	}

	products := schema.Tables[0]
	if len(products.Columns) != 2 {
		t.Fatalf("products columns = %d, want 2", len(products.Columns))
	}

	metadata := products.Columns[1]
	if metadata.Name != "metadata" {
		t.Fatalf("column[1].name = %q, want %q", metadata.Name, "metadata")
	}
	if metadata.JSONSchema == nil {
		t.Fatal("expected json_schema to be set")
	}
	if *metadata.JSONSchema != "test_schema.json" {
		t.Errorf("json_schema = %q, want %q", *metadata.JSONSchema, "test_schema.json")
	}
}

func TestJSONSchemaFileMissing(t *testing.T) {
	schema, diags := Bytes([]byte(`
[meta]
version = 1
schema = "test"

[tables.t]
comment = "test"
pk = ["id"]

[tables.t.columns.id]
type = "uuid"

[tables.t.columns.data]
type = "jsonb"
json_schema = "nonexistent.json"
`))
	if schema == nil {
		t.Fatal("expected schema")
	}
	// When parsing from bytes, json_schema file validation is skipped
	// (no directory context to resolve relative paths).
	// The value should still be stored.
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors from Bytes(): %v", diags)
	}
	col := schema.Tables[0].Columns[1]
	if col.JSONSchema == nil {
		t.Fatal("expected json_schema to be set even from Bytes()")
	}
}

func TestJSONSchemaFileNotFound(t *testing.T) {
	// Create a temp TOML file that references a nonexistent JSON schema
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "test.toml")
	os.WriteFile(tomlPath, []byte(`
[meta]
version = 1
schema = "test"

[tables.t]
comment = "test"
pk = ["id"]

[tables.t.columns.id]
type = "uuid"

[tables.t.columns.data]
type = "jsonb"
json_schema = "nonexistent.json"
`), 0644)

	_, diags := File(tomlPath)
	found := false
	for _, d := range diags {
		if d.Code == "E012" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected E012 diagnostic for missing json_schema file")
	}
}

func TestJSONSchemaFileInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	// Write an invalid JSON file
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{not valid json`), 0644)
	tomlPath := filepath.Join(dir, "test.toml")
	os.WriteFile(tomlPath, []byte(`
[meta]
version = 1
schema = "test"

[tables.t]
comment = "test"
pk = ["id"]

[tables.t.columns.id]
type = "uuid"

[tables.t.columns.data]
type = "jsonb"
json_schema = "bad.json"
`), 0644)

	_, diags := File(tomlPath)
	found := false
	for _, d := range diags {
		if d.Code == "E013" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected E013 diagnostic for invalid JSON in json_schema file")
	}
}

func TestIndexWithParams(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.items]
pk = ["id"]

[tables.items.columns.id]
type = "auto_id"

[tables.items.columns.embedding]
type = "vector(1536)"

[tables.items.indexes.idx_embedding]
columns = ["embedding"]
method = "hnsw"
with = { m = "16", ef_construction = "200" }
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	idx := schema.Tables[0].Indexes["idx_embedding"]
	if idx.With == nil {
		t.Fatal("idx.With should not be nil")
	}
	if idx.With["m"] != "16" {
		t.Errorf("With[m] = %q, want %q", idx.With["m"], "16")
	}
	if idx.With["ef_construction"] != "200" {
		t.Errorf("With[ef_construction] = %q, want %q", idx.With["ef_construction"], "200")
	}
}

func TestViewParsing(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.users]
pk = ["id"]
comment = "Users"

[tables.users.columns.id]
type = "auto_id"

[tables.users.columns.active]
type = "boolean"

[views.active_users]
query = "SELECT id FROM users WHERE active"
comment = "Active users only"
depends_on = ["users"]
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(schema.Views))
	}

	v := schema.Views[0]
	if v.Name != "active_users" {
		t.Errorf("view name = %q, want %q", v.Name, "active_users")
	}
	if v.Query != "SELECT id FROM users WHERE active" {
		t.Errorf("view query = %q, want %q", v.Query, "SELECT id FROM users WHERE active")
	}
	if v.Comment == nil || *v.Comment != "Active users only" {
		t.Errorf("view comment = %v, want %q", v.Comment, "Active users only")
	}
	if len(v.DependsOn) != 1 || v.DependsOn[0] != "users" {
		t.Errorf("view depends_on = %v, want [users]", v.DependsOn)
	}
}

func TestViewMissingQuery(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[views.broken]
comment = "No query"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}

	var hasError bool
	for _, d := range diags {
		if d.Severity == diagnostic.Error && d.Code == "E011" {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Error("expected E011 error for missing query field")
	}
}

func TestViewUnknownKey(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[views.v]
query = "SELECT 1"
unknown_field = "oops"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}

	var hasWarning bool
	for _, d := range diags {
		if d.Severity == diagnostic.Warning && d.Code == "W001" {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Error("expected W001 warning for unknown key in view")
	}
}

func TestParseMaterializedView_Basic(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[materialized_views.monthly_stats]
query = "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1"
comment = "Monthly order statistics"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.MaterializedViews) != 1 {
		t.Fatalf("expected 1 materialized view, got %d", len(schema.MaterializedViews))
	}

	mv := schema.MaterializedViews[0]
	if mv.Name != "monthly_stats" {
		t.Errorf("name = %q, want %q", mv.Name, "monthly_stats")
	}
	if mv.Query != "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1" {
		t.Errorf("query = %q, want %q", mv.Query, "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1")
	}
	if mv.Comment == nil || *mv.Comment != "Monthly order statistics" {
		t.Errorf("comment = %v, want %q", mv.Comment, "Monthly order statistics")
	}
	if mv.WithData != nil {
		t.Errorf("with_data = %v, want nil", mv.WithData)
	}
	if mv.DependsOn != nil {
		t.Errorf("depends_on = %v, want nil", mv.DependsOn)
	}
	if len(mv.Indexes) != 0 {
		t.Errorf("indexes len = %d, want 0", len(mv.Indexes))
	}
}

func TestParseMaterializedView_WithDataFalse(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[materialized_views.monthly_stats]
query = "SELECT 1"
with_data = false
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.MaterializedViews) != 1 {
		t.Fatalf("expected 1 materialized view, got %d", len(schema.MaterializedViews))
	}

	mv := schema.MaterializedViews[0]
	if mv.WithData == nil || *mv.WithData != false {
		t.Errorf("with_data = %v, want false", mv.WithData)
	}
}

func TestParseMaterializedView_WithIndexes(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[materialized_views.monthly_stats]
query = "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1"

[materialized_views.monthly_stats.indexes.idx_month]
columns = ["month"]
unique = true
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.MaterializedViews) != 1 {
		t.Fatalf("expected 1 materialized view, got %d", len(schema.MaterializedViews))
	}

	mv := schema.MaterializedViews[0]
	if len(mv.Indexes) != 1 {
		t.Fatalf("indexes len = %d, want 1", len(mv.Indexes))
	}
	idx, ok := mv.Indexes["idx_month"]
	if !ok {
		t.Fatalf("indexes missing key %q", "idx_month")
	}
	if len(idx.Columns) != 1 || idx.Columns[0] != "month" {
		t.Errorf("index columns = %v, want [month]", idx.Columns)
	}
	if idx.Unique == nil || *idx.Unique != true {
		t.Errorf("index unique = %v, want true", idx.Unique)
	}
}

func TestParseMaterializedView_MissingQuery(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[materialized_views.bad]
comment = "no query"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}

	var hasError bool
	for _, d := range diags {
		if d.Severity == diagnostic.Error && d.Code == "E011" {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Error("expected E011 error for missing query field")
	}
}

func TestParseMaterializedView_DependsOn(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[materialized_views.monthly_stats]
query = "SELECT 1"
depends_on = ["orders"]
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.MaterializedViews) != 1 {
		t.Fatalf("expected 1 materialized view, got %d", len(schema.MaterializedViews))
	}

	mv := schema.MaterializedViews[0]
	if len(mv.DependsOn) != 1 || mv.DependsOn[0] != "orders" {
		t.Errorf("depends_on = %v, want [orders]", mv.DependsOn)
	}
}

func TestPartitionSingleColumn(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.events]
pk = ["id"]
comment = "Events"

[tables.events.columns.id]
type = "auto_id"

[tables.events.columns.created_at]
type = "timestamptz"

[tables.events.partitioning]
strategy = "range"
column = "created_at"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}
	tbl := schema.Tables[0]
	if tbl.Partitioning == nil {
		t.Fatal("expected partitioning to be set")
	}
	if tbl.Partitioning.Strategy != "range" {
		t.Errorf("strategy = %q, want %q", tbl.Partitioning.Strategy, "range")
	}
	if tbl.Partitioning.Column != "created_at" {
		t.Errorf("column = %q, want %q", tbl.Partitioning.Column, "created_at")
	}
}

func TestPartitionMultiColumn(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.events]
pk = ["id"]
comment = "Events"

[tables.events.columns.id]
type = "auto_id"

[tables.events.columns.year]
type = "integer"

[tables.events.columns.region]
type = "text"

[tables.events.partitioning]
strategy = "range"
columns = ["year", "region"]
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}
	tbl := schema.Tables[0]
	if tbl.Partitioning == nil {
		t.Fatal("expected partitioning to be set")
	}
	if tbl.Partitioning.Strategy != "range" {
		t.Errorf("strategy = %q, want %q", tbl.Partitioning.Strategy, "range")
	}
	if len(tbl.Partitioning.Columns) != 2 {
		t.Fatalf("columns len = %d, want 2", len(tbl.Partitioning.Columns))
	}
	if tbl.Partitioning.Columns[0] != "year" || tbl.Partitioning.Columns[1] != "region" {
		t.Errorf("columns = %v, want [year region]", tbl.Partitioning.Columns)
	}
}

func TestPartitionBothColumnAndColumnsError(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.events]
pk = ["id"]
comment = "Events"

[tables.events.columns.id]
type = "auto_id"

[tables.events.columns.created_at]
type = "timestamptz"

[tables.events.partitioning]
strategy = "range"
column = "created_at"
columns = ["created_at"]
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}

	found := false
	for _, d := range diags {
		if d.Severity == diagnostic.Error && d.Code == "E010" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected E010 error for both column and columns set, got: %v", diags)
	}
}

func TestParseColumnCollation(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.messages]
pk = ["id"]
comment = "Messages table"

[tables.messages.columns.id]
type = "bigint"

[tables.messages.columns.content]
type = "text"
collation = "de_DE"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	tbl := schema.Tables[0]
	var contentCol *RawColumn
	for i := range tbl.Columns {
		if tbl.Columns[i].Name == "content" {
			contentCol = &tbl.Columns[i]
			break
		}
	}
	if contentCol == nil {
		t.Fatal("expected content column")
	}
	if contentCol.Collation == nil {
		t.Fatal("expected Collation to be set")
	}
	if *contentCol.Collation != "de_DE" {
		t.Errorf("Collation = %q, want %q", *contentCol.Collation, "de_DE")
	}
}

func TestParseColumnStatistics(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.messages]
pk = ["id"]
comment = "Messages table"

[tables.messages.columns.id]
type = "bigint"

[tables.messages.columns.content]
type = "text"
statistics = 1000
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	tbl := schema.Tables[0]
	var contentCol *RawColumn
	for i := range tbl.Columns {
		if tbl.Columns[i].Name == "content" {
			contentCol = &tbl.Columns[i]
			break
		}
	}
	if contentCol == nil {
		t.Fatal("expected content column")
	}
	if contentCol.Statistics == nil {
		t.Fatal("expected Statistics to be set")
	}
	if *contentCol.Statistics != 1000 {
		t.Errorf("Statistics = %d, want %d", *contentCol.Statistics, 1000)
	}
}

func TestParseIndexCollation_String(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.messages]
pk = ["id"]
comment = "Messages table"

[tables.messages.columns.id]
type = "bigint"

[tables.messages.columns.content]
type = "text"

[tables.messages.indexes.idx_content]
columns = ["content"]
collation = "C"
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	idx := schema.Tables[0].Indexes["idx_content"]
	if idx.Collation == nil {
		t.Fatal("expected Collation to be set")
	}
	if *idx.Collation != "C" {
		t.Errorf("Collation = %q, want %q", *idx.Collation, "C")
	}
	if idx.CollationMap != nil {
		t.Errorf("CollationMap should be nil for single string, got %v", idx.CollationMap)
	}
}

func TestParseIndexCollation_Map(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.people]
pk = ["id"]
comment = "People table"

[tables.people.columns.id]
type = "bigint"

[tables.people.columns.first_name]
type = "text"

[tables.people.columns.last_name]
type = "text"

[tables.people.indexes.idx_names]
columns = ["first_name", "last_name"]
collation = { first_name = "de_DE", last_name = "C" }
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	idx := schema.Tables[0].Indexes["idx_names"]
	if idx.Collation != nil {
		t.Errorf("Collation should be nil for map syntax, got %v", idx.Collation)
	}
	if idx.CollationMap == nil {
		t.Fatal("CollationMap should not be nil")
	}
	if idx.CollationMap["first_name"] != "de_DE" {
		t.Errorf("CollationMap[first_name] = %q, want %q", idx.CollationMap["first_name"], "de_DE")
	}
	if idx.CollationMap["last_name"] != "C" {
		t.Errorf("CollationMap[last_name] = %q, want %q", idx.CollationMap["last_name"], "C")
	}
}

func TestParseColumnCollationAndStatistics(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.messages]
pk = ["id"]
comment = "Messages table"

[tables.messages.columns.id]
type = "bigint"

[tables.messages.columns.content]
type = "text"
collation = "de_DE"
statistics = 1000
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	tbl := schema.Tables[0]
	var contentCol *RawColumn
	for i := range tbl.Columns {
		if tbl.Columns[i].Name == "content" {
			contentCol = &tbl.Columns[i]
			break
		}
	}
	if contentCol == nil {
		t.Fatal("expected content column")
	}
	if contentCol.Collation == nil {
		t.Fatal("expected Collation to be set")
	}
	if *contentCol.Collation != "de_DE" {
		t.Errorf("Collation = %q, want %q", *contentCol.Collation, "de_DE")
	}
	if contentCol.Statistics == nil {
		t.Fatal("expected Statistics to be set")
	}
	if *contentCol.Statistics != 1000 {
		t.Errorf("Statistics = %d, want %d", *contentCol.Statistics, 1000)
	}
}

func TestParseExclusion(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.bookings]
comment = "Room bookings"
pk = ["id"]

[tables.bookings.columns.id]
type = "integer"

[tables.bookings.columns.room_id]
type = "integer"

[tables.bookings.columns.during]
type = "tsrange"

[tables.bookings.exclusions.no_overlap]
columns = ["room_id", "during"]
operators = ["=", "&&"]
method = "gist"
where = "active = true"
deferrable = true
initially_deferred = true
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}
	tbl := schema.Tables[0]

	if len(tbl.Exclusions) != 1 {
		t.Fatalf("expected 1 exclusion, got %d", len(tbl.Exclusions))
	}

	exc, ok := tbl.Exclusions["no_overlap"]
	if !ok {
		t.Fatal("expected exclusion 'no_overlap'")
	}
	if exc.Name != "no_overlap" {
		t.Errorf("Name = %q, want %q", exc.Name, "no_overlap")
	}
	if len(exc.Columns) != 2 || exc.Columns[0] != "room_id" || exc.Columns[1] != "during" {
		t.Errorf("Columns = %v, want [room_id during]", exc.Columns)
	}
	if len(exc.Operators) != 2 || exc.Operators[0] != "=" || exc.Operators[1] != "&&" {
		t.Errorf("Operators = %v, want [= &&]", exc.Operators)
	}
	if exc.Method == nil {
		t.Fatal("expected Method to be set")
	}
	if *exc.Method != "gist" {
		t.Errorf("Method = %q, want %q", *exc.Method, "gist")
	}
	if exc.Where == nil {
		t.Fatal("expected Where to be set")
	}
	if *exc.Where != "active = true" {
		t.Errorf("Where = %q, want %q", *exc.Where, "active = true")
	}
	if exc.Deferrable == nil {
		t.Fatal("expected Deferrable to be set")
	}
	if *exc.Deferrable != true {
		t.Errorf("Deferrable = %v, want true", *exc.Deferrable)
	}
	if exc.InitiallyDeferred == nil {
		t.Fatal("expected InitiallyDeferred to be set")
	}
	if *exc.InitiallyDeferred != true {
		t.Errorf("InitiallyDeferred = %v, want true", *exc.InitiallyDeferred)
	}
}

func TestParseExclusionLengthMismatch(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.bookings]
comment = "Room bookings"
pk = ["id"]

[tables.bookings.columns.id]
type = "integer"

[tables.bookings.columns.room_id]
type = "integer"

[tables.bookings.columns.during]
type = "tsrange"

[tables.bookings.exclusions.no_overlap]
columns = ["room_id", "during"]
operators = ["="]
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}

	found := false
	for _, d := range diags {
		if d.Severity == diagnostic.Error && d.Code == "E010" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected E010 error for mismatched columns/operators lengths, got: %v", diags)
	}
}

func TestParseExclusionDefaults(t *testing.T) {
	content := `[meta]
version = 1
schema = "test"

[tables.bookings]
comment = "Room bookings"
pk = ["id"]

[tables.bookings.columns.id]
type = "integer"

[tables.bookings.columns.room_id]
type = "integer"

[tables.bookings.columns.during]
type = "tsrange"

[tables.bookings.exclusions.no_overlap]
columns = ["room_id", "during"]
operators = ["=", "&&"]
`
	schema, diags := Bytes([]byte(content))
	if schema == nil {
		t.Fatalf("expected schema, got nil; diags: %v", diags)
	}
	if hasFatalErrors(diags) {
		t.Fatalf("unexpected errors: %v", diags)
	}

	exc, ok := schema.Tables[0].Exclusions["no_overlap"]
	if !ok {
		t.Fatal("expected exclusion 'no_overlap'")
	}
	if exc.Method != nil {
		t.Errorf("Method = %v, want nil", exc.Method)
	}
	if exc.Where != nil {
		t.Errorf("Where = %v, want nil", exc.Where)
	}
	if exc.Deferrable != nil {
		t.Errorf("Deferrable = %v, want nil", exc.Deferrable)
	}
	if exc.InitiallyDeferred != nil {
		t.Errorf("InitiallyDeferred = %v, want nil", exc.InitiallyDeferred)
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
