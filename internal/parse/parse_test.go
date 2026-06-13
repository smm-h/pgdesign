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

// hasFatalErrors returns true if any diagnostic is an error (not warning/info).
func hasFatalErrors(diags []diagnostic.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == diagnostic.Error {
			return true
		}
	}
	return false
}
