package codegen

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/sqlparse"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// loadTestSchema parses and builds the DDL test input schema.
func loadTestSchema(t *testing.T) *model.Schema {
	t.Helper()
	inputPath := filepath.Join("testdata", "ddl_input.toml")
	raw, diags := parse.File(inputPath)
	if raw == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("parse error: %s", d.Message)
		}
	}
	reg := semtype.NewBuiltinRegistry()
	schema, buildDiags := model.Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("build errors: %v", buildDiags)
	}
	return schema
}

// updateGolden regenerates golden files when true (same convention as
// internal/test/golden_test.go: go test -run TestPythonDDL_GoldenFile -update).
var updateGolden = flag.Bool("update", false, "update golden files")

func TestPythonDDL_GoldenFile(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	expectedPath := filepath.Join("testdata", "ddl_expected.py")
	if *updateGolden {
		if err := os.WriteFile(expectedPath, out, 0644); err != nil {
			t.Fatalf("cannot update golden file: %v", err)
		}
		t.Logf("updated %s", expectedPath)
	}
	expectedBytes, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("cannot read expected file (run with -update to create): %v", err)
	}

	got := string(out)
	expected := string(expectedBytes)
	if got != expected {
		t.Errorf("golden file mismatch.\n--- got ---\n%s\n--- expected ---\n%s", got, expected)
	}
}

func TestPythonDDL_TupleCount(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	out, _ := gen.Generate(schema)

	// Count DDLStmt entries: lines matching DDLStmt( inside STATEMENTS.
	output := string(out)
	lines := strings.Split(output, "\n")
	tupleCount := 0
	inStatements := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "STATEMENTS:") {
			inStatements = true
			continue
		}
		if inStatements && trimmed == "]" {
			break
		}
		if inStatements && strings.HasPrefix(trimmed, "DDLStmt(") {
			tupleCount++
		}
	}

	// Expected: schema(1) + extension(1) + domain(2) + table(2) + fk(1) +
	//           unique(1) + check(1) + index(1) + comment(3) = 13
	expected := 13
	if tupleCount != expected {
		t.Errorf("tuple count = %d, want %d", tupleCount, expected)
	}
}

func TestPythonDDL_PhaseOrdering(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	out, _ := gen.Generate(schema)

	// Extract phase numbers from DDLStmt entries.
	// Format: DDLStmt(..., <phase>, True/False),
	// The phase is the second-to-last field before the closing paren.
	output := string(out)
	lines := strings.Split(output, "\n")
	var phases []int
	inStatements := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "STATEMENTS:") {
			inStatements = true
			continue
		}
		if inStatements && trimmed == "]" {
			break
		}
		if !inStatements || !strings.HasPrefix(trimmed, "DDLStmt(") {
			continue
		}
		// Find ", <phase>, True)," or ", <phase>, False),"
		// Look for the pattern: , <digits>, True) or , <digits>, False)
		for _, boolStr := range []string{", True),", ", True)", ", False),", ", False)"} {
			idx := strings.LastIndex(trimmed, boolStr)
			if idx < 0 {
				continue
			}
			// Walk backward from idx to find ", <phase>"
			before := trimmed[:idx]
			lastComma := strings.LastIndex(before, ", ")
			if lastComma < 0 {
				continue
			}
			phaseStr := before[lastComma+2:]
			phase := 0
			valid := true
			for _, ch := range phaseStr {
				if ch < '0' || ch > '9' {
					valid = false
					break
				}
				phase = phase*10 + int(ch-'0')
			}
			if valid && len(phaseStr) > 0 {
				phases = append(phases, phase)
			}
			break
		}
	}

	if len(phases) == 0 {
		t.Fatal("no phases extracted")
	}

	// Verify monotonically non-decreasing.
	for i := 1; i < len(phases); i++ {
		if phases[i] < phases[i-1] {
			t.Errorf("phase ordering violated at index %d: phase %d < %d", i, phases[i], phases[i-1])
		}
	}
}

func TestPythonDDL_TableNames(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	out, _ := gen.Generate(schema)

	output := string(out)

	// Verify TABLE_NAMES matches schema tables.
	tables := schema.TableOrder()
	for _, tbl := range tables {
		if !strings.Contains(output, `"`+tbl.Name+`"`) {
			t.Errorf("TABLE_NAMES missing table %q", tbl.Name)
		}
	}

	// Verify the TABLE_NAMES line exists.
	if !strings.Contains(output, "TABLE_NAMES: Final[tuple[str, ...]]") {
		t.Error("TABLE_NAMES declaration not found")
	}

	// Verify order: customers before orders (dependency order).
	customersIdx := strings.Index(output, `"customers"`)
	ordersIdx := strings.Index(output, `"orders"`)
	// Find these in the TABLE_NAMES line specifically.
	tableNamesLine := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "TABLE_NAMES:") {
			tableNamesLine = line
			break
		}
	}
	if tableNamesLine == "" {
		t.Fatal("TABLE_NAMES line not found")
	}
	customersIdx = strings.Index(tableNamesLine, `"customers"`)
	ordersIdx = strings.Index(tableNamesLine, `"orders"`)
	if customersIdx < 0 || ordersIdx < 0 {
		t.Fatal("TABLE_NAMES missing expected table names")
	}
	if customersIdx >= ordersIdx {
		t.Error("TABLE_NAMES: customers should come before orders (dependency order)")
	}
}

func TestPythonDDL_AllKindsPresent(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	out, _ := gen.Generate(schema)

	output := string(out)

	// The test schema exercises: schema, extension, domain, table, fk, unique,
	// check, index, comment. Other kinds (enum, composite, partition, etc.)
	// require different schema features.
	expectedKinds := []string{
		"schema", "extension", "domain", "table", "fk",
		"unique", "check", "index", "comment",
	}
	for _, kind := range expectedKinds {
		// Look for the kind as a quoted string in a tuple.
		if !strings.Contains(output, `"`+kind+`"`) {
			t.Errorf("missing DDL kind %q in output", kind)
		}
	}
}

func TestPythonDDL_StatementCountMatchesSQL(t *testing.T) {
	if testing.Short() {
		t.Skip("requires WASM parser")
	}
	schema := loadTestSchema(t)

	// Generate SQL with comments (to match DDL generator which always emits comments).
	sqlOutput, _, err := generate.Generate(schema, generate.Options{
		IncludeComments: true,
		Format:          "sql",
	})
	if err != nil {
		t.Fatalf("SQL generation failed: %v", err)
	}

	// Split SQL into individual statements.
	sqlStmts, err := sqlparse.SplitStatements(sqlOutput)
	if err != nil {
		t.Fatalf("SQL split failed: %v", err)
	}

	// Generate Python DDL tuples.
	gen := &PythonDDLGenerator{}
	pyOut, _ := gen.Generate(schema)

	// Count tuples in Python output.
	output := string(pyOut)
	lines := strings.Split(output, "\n")
	tupleCount := 0
	inStatements := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "STATEMENTS:") {
			inStatements = true
			continue
		}
		if inStatements && trimmed == "]" {
			break
		}
		if inStatements && strings.HasPrefix(trimmed, "DDLStmt(") {
			tupleCount++
		}
	}

	if tupleCount != len(sqlStmts) {
		t.Errorf("statement count mismatch: Python DDL has %d tuples, SQL has %d statements", tupleCount, len(sqlStmts))
	}
}

func TestPythonDDL_Header(t *testing.T) {
	schema := &model.Schema{
		Name: "test",
	}
	gen := &PythonDDLGenerator{}
	out, _ := gen.Generate(schema)
	output := string(out)

	if !strings.HasPrefix(output, "# Code generated by pgdesign -- do not edit.") {
		t.Error("missing header comment")
	}
	if !strings.Contains(output, "from typing import Final") {
		t.Error("missing typing import")
	}
	if !strings.Contains(output, "STATEMENTS: Final[list[DDLStmt]]") {
		t.Error("missing STATEMENTS type annotation")
	}
	if !strings.Contains(output, `DDLStmt = namedtuple("DDLStmt"`) {
		t.Error("missing DDLStmt namedtuple definition")
	}
}

func TestPythonDDL_EmptySchema(t *testing.T) {
	schema := &model.Schema{}
	gen := &PythonDDLGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	output := string(out)

	if !strings.Contains(output, "STATEMENTS: Final[list[DDLStmt]] = [\n]") {
		t.Error("empty schema should produce empty STATEMENTS list")
	}
	if !strings.Contains(output, "TABLE_NAMES: Final[tuple[str, ...]] = ()") {
		t.Error("empty schema should produce empty TABLE_NAMES")
	}
}

func TestPythonDDL_SingleTable(t *testing.T) {
	// Single-element tuple needs trailing comma in Python.
	schema := &model.Schema{
		Name: "test",
		Tables: []model.Table{{
			Name:    "users",
			Schema:  "test",
			Comment: "All users",
			PK:      []string{"id"},
			Columns: []model.Column{
				{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
			},
		}},
	}
	gen := &PythonDDLGenerator{}
	out, _ := gen.Generate(schema)
	output := string(out)

	// Single-element tuple must have trailing comma.
	if !strings.Contains(output, `("users",)`) {
		t.Error("single-element TABLE_NAMES tuple should have trailing comma")
	}
}

// -- Phase 11: MultiFileGenerator and executor tests --

func TestPythonDDL_GenerateFiles_TwoFiles(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, diags := gen.GenerateFiles(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if _, ok := files["schema_ddl.py"]; !ok {
		t.Error("missing schema_ddl.py")
	}
	if _, ok := files["schema_executor.py"]; !ok {
		t.Error("missing schema_executor.py")
	}
}

func TestPythonDDL_GenerateFiles_DDLMatchesGenerate(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}

	singleFile, diags1 := gen.Generate(schema)
	files, diags2 := gen.GenerateFiles(schema)

	if len(diags1) != len(diags2) {
		t.Fatalf("diagnostic count mismatch: Generate=%d, GenerateFiles=%d", len(diags1), len(diags2))
	}

	ddlFile := files["schema_ddl.py"]
	if string(singleFile) != string(ddlFile) {
		t.Error("schema_ddl.py from GenerateFiles should match Generate output")
	}
}

func TestPythonDDL_Executor_SectionCount(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	// The test schema has phases: 1 (schemas), 2 (extensions), 3 (domains),
	// 4 (tables), 6 (fks), 7 (uniques), 8 (checks), 9 (indexes), 10 (comments).
	// That's 9 sections.
	sectionCount := strings.Count(executor, "Section(")
	// Subtract 1 for the class definition "class Section:".
	// Actually Section( only appears in SECTIONS list entries.
	expected := 9
	if sectionCount != expected {
		t.Errorf("section count = %d, want %d", sectionCount, expected)
	}
}

func TestPythonDDL_Executor_HasExecuteFunction(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "async def execute(") {
		t.Error("executor missing execute function")
	}
}

func TestPythonDDL_Executor_HasAsyncConnectionProtocol(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "class AsyncConnection(Protocol)") {
		t.Error("executor missing AsyncConnection protocol")
	}
}

func TestPythonDDL_Executor_HasDDLOpNamedtuple(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, `DDLOp = namedtuple("DDLOp", ["sql", "idempotent_sql", "name"])`) {
		t.Error("executor missing DDLOp namedtuple definition")
	}
}

func TestPythonDDL_Executor_HasConvenienceFunctions(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "async def create_schema(") {
		t.Error("executor missing create_schema convenience function")
	}
	if !strings.Contains(executor, "async def ensure_schema(") {
		t.Error("executor missing ensure_schema convenience function")
	}
}

func TestPythonDDL_Executor_HasVerifyFunction(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "async def verify(") {
		t.Error("executor missing verify function")
	}
}

func TestPythonDDL_Executor_HasExistsMethod(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "async def exists(self, conn") {
		t.Error("executor missing Section.exists method")
	}
}

func TestPythonDDL_Executor_TransactionalSections(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	// All sections in the test schema should be transactional (no CONCURRENTLY
	// indexes or ALTER TYPE ADD VALUE).
	if strings.Contains(executor, "transactional=False") {
		t.Error("test schema should have no non-transactional sections")
	}
}

func TestPythonDDL_Executor_EmptySchema(t *testing.T) {
	schema := &model.Schema{}
	gen := &PythonDDLGenerator{}
	files, diags := gen.GenerateFiles(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	executor := string(files["schema_executor.py"])

	// Empty schema should produce SECTIONS = [] with no Section entries.
	if !strings.Contains(executor, "SECTIONS: list[Section] = [\n]") {
		t.Error("empty schema should produce empty SECTIONS list")
	}

	// But should still have the protocol, DDLOp, and functions.
	if !strings.Contains(executor, "class AsyncConnection(Protocol)") {
		t.Error("empty executor missing AsyncConnection protocol")
	}
	if !strings.Contains(executor, "DDLOp = namedtuple") {
		t.Error("empty executor missing DDLOp")
	}
	if !strings.Contains(executor, "async def execute(") {
		t.Error("empty executor missing execute function")
	}
}

func TestPythonDDL_Executor_IdempotentSQL(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	// Schemas, extensions, tables, indexes all have idempotent variants.
	// Check that IF NOT EXISTS appears in the executor.
	if !strings.Contains(executor, "IF NOT EXISTS") {
		t.Error("executor should contain IF NOT EXISTS idempotent SQL")
	}

	// PostgreSQL has no CREATE TYPE IF NOT EXISTS; enum idempotency uses a
	// DO block with a pg_type catalog check instead.
	if strings.Contains(executor, "CREATE TYPE IF NOT EXISTS") {
		t.Error("executor must not contain invalid CREATE TYPE IF NOT EXISTS syntax")
	}

	// Domains and composites have no idempotent variant; they should have None.
	// Check for at least one DDLOp with None as idempotent_sql.
	if !strings.Contains(executor, ", None, ") {
		t.Error("executor should contain ops with None idempotent_sql (domains, etc.)")
	}
}

// TestPythonDDL_EnumIdempotentDOBlock verifies that enum idempotent SQL in the
// default two-file output is the valid DO-block form with a pg_type catalog
// check, never the invalid CREATE TYPE IF NOT EXISTS syntax.
func TestPythonDDL_EnumIdempotentDOBlock(t *testing.T) {
	schema := loadSplitTestSchema(t) // has the trace_status enum
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)

	for _, name := range []string{"schema_ddl.py", "schema_executor.py"} {
		content := string(files[name])
		if strings.Contains(content, "CREATE TYPE IF NOT EXISTS") {
			t.Errorf("%s must not contain invalid CREATE TYPE IF NOT EXISTS syntax", name)
		}
		if !strings.Contains(content, "DO $$") || !strings.Contains(content, "typtype = 'e'") {
			t.Errorf("%s should contain a DO block with pg_type check for the enum", name)
		}
		if !strings.Contains(content, "typname = 'trace_status'") {
			t.Errorf("%s enum DO block should reference trace_status", name)
		}
	}
}

func TestPythonDDL_Executor_SectionKinds(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	expectedKinds := []string{
		"schemas", "extensions", "types", "tables",
		"foreign_keys", "unique_constraints", "check_constraints",
		"indexes", "comments",
	}
	for _, kind := range expectedKinds {
		if !strings.Contains(executor, fmt.Sprintf("kind=%q", kind)) {
			t.Errorf("executor missing section kind %q", kind)
		}
	}
}

func TestPythonDDL_Executor_ExistenceChecks(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	// Verify the existence checker queries reference the right catalog tables.
	checks := map[string]string{
		"schemas":            "information_schema.schemata",
		"tables":             "information_schema.tables",
		"indexes":            "pg_indexes",
		"types":              "pg_type",
		"foreign_keys":       "pg_constraint",
		"unique_constraints": "pg_constraint",
		"check_constraints":  "pg_constraint",
	}
	for kind, catalog := range checks {
		if !strings.Contains(executor, catalog) {
			t.Errorf("existence check for %q should reference %s", kind, catalog)
		}
	}
}

func TestPythonDDL_MultiFileGenerator_Interface(t *testing.T) {
	// Verify PythonDDLGenerator satisfies MultiFileGenerator at compile time.
	var _ MultiFileGenerator = &PythonDDLGenerator{}
}

// -- Faceted output tests --

// loadSplitTestSchema parses both split_trace.toml and split_dispatch.toml,
// registers user-defined types, and builds a multi-file schema.
func loadSplitTestSchema(t *testing.T) *model.Schema {
	t.Helper()
	tracePath := filepath.Join("testdata", "split_trace.toml")
	dispatchPath := filepath.Join("testdata", "split_dispatch.toml")
	raws, diags := parse.Files([]string{tracePath, dispatchPath})
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("parse error: %s", d.Message)
		}
	}
	if len(raws) != 2 {
		t.Fatalf("expected 2 parsed schemas, got %d", len(raws))
	}
	reg := semtype.NewBuiltinRegistry()
	for _, raw := range raws {
		userTypes := parse.CollectUserTypes(raw)
		if len(userTypes) > 0 {
			loadDiags := reg.LoadUserTypes(userTypes)
			if loadDiags.HasErrors() {
				t.Fatalf("LoadUserTypes errors: %v", loadDiags)
			}
		}
	}
	schema, buildDiags := model.BuildMulti(raws, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("build errors: %v", buildDiags)
	}
	return schema
}

func TestPythonDDL_FacetedOutput(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, diags := gen.GenerateFiles(schema)
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("generation error: %s", d.Message)
		}
	}

	expectedFiles := []string{"extensions.py", "types.py", "tables_split_trace.py", "tables_split_dispatch.py", "schema_executor.py"}
	for _, name := range expectedFiles {
		if _, ok := files[name]; !ok {
			t.Errorf("missing expected file %q in faceted output", name)
		}
	}
}

func TestPythonDDL_FacetedTypes(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)

	typesContent := string(files["types.py"])
	if !strings.Contains(typesContent, "trace_status") {
		t.Error("types.py should contain the trace_status enum")
	}
	if strings.Contains(typesContent, "CREATE TABLE") {
		t.Error("types.py should not contain CREATE TABLE statements")
	}
}

func TestPythonDDL_FacetedTableSeparation(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)

	traceContent := string(files["tables_split_trace.py"])
	dispatchContent := string(files["tables_split_dispatch.py"])

	// Trace file should contain spans and events tables.
	if !strings.Contains(traceContent, "spans") {
		t.Error("tables_split_trace.py should contain spans table")
	}
	if !strings.Contains(traceContent, "events") {
		t.Error("tables_split_trace.py should contain events table")
	}

	// Dispatch file should contain tasks table.
	if !strings.Contains(dispatchContent, "tasks") {
		t.Error("tables_split_dispatch.py should contain tasks table")
	}

	// Cross-file isolation.
	if strings.Contains(traceContent, "tasks") {
		t.Error("tables_split_trace.py should not contain tasks table")
	}
	if strings.Contains(dispatchContent, "spans") {
		t.Error("tables_split_dispatch.py should not contain spans table")
	}
}

func TestPythonDDL_FacetedEmptyPostTables(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)

	if _, ok := files["post_tables.py"]; ok {
		t.Error("post_tables.py should not be present when there are no views/functions")
	}
}

func TestPythonDDL_FacetedExecutor(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)

	executor, ok := files["schema_executor.py"]
	if !ok {
		t.Fatal("schema_executor.py should be present in faceted output")
	}
	content := string(executor)

	// Should import STATEMENTS from faceted modules.
	if !strings.Contains(content, "from .extensions import STATEMENTS as _ext_stmts") {
		t.Error("executor should import from extensions module")
	}
	if !strings.Contains(content, "from .types import STATEMENTS as _types_stmts") {
		t.Error("executor should import from types module")
	}
	if !strings.Contains(content, "from .tables_split_trace import STATEMENTS as _tables_split_trace_stmts") {
		t.Error("executor should import from tables_split_trace module")
	}
	if !strings.Contains(content, "from .tables_split_dispatch import STATEMENTS as _tables_split_dispatch_stmts") {
		t.Error("executor should import from tables_split_dispatch module")
	}

	// Should concatenate all STATEMENTS.
	if !strings.Contains(content, "_ALL_STMTS = ") {
		t.Error("executor should concatenate all STATEMENTS into _ALL_STMTS")
	}

	// Should have the executor API.
	if !strings.Contains(content, "async def execute(") {
		t.Error("executor should have execute function")
	}
	if !strings.Contains(content, "async def verify(") {
		t.Error("executor should have verify function")
	}
	if !strings.Contains(content, "async def create_schema(") {
		t.Error("executor should have create_schema function")
	}
	if !strings.Contains(content, "async def ensure_schema(") {
		t.Error("executor should have ensure_schema function")
	}

	// Should have SECTIONS.
	if !strings.Contains(content, "SECTIONS: list[Section] = [") {
		t.Error("executor should have SECTIONS list")
	}
}

func TestPythonDDL_FacetedDDLStmt(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)

	// All faceted data files should use DDLStmt namedtuple.
	for name, content := range files {
		if name == "schema_executor.py" || name == "__init__.py" {
			continue
		}
		s := string(content)
		if !strings.Contains(s, `DDLStmt = namedtuple("DDLStmt"`) {
			t.Errorf("%s should contain DDLStmt namedtuple definition", name)
		}
		if !strings.Contains(s, "STATEMENTS: Final[list[DDLStmt]]") {
			t.Errorf("%s should use DDLStmt type annotation", name)
		}
		if !strings.Contains(s, "DDLStmt(") {
			t.Errorf("%s should contain DDLStmt( entries", name)
		}
	}
}

func TestPythonDDL_FacetedInitPy(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)

	// __init__.py must be in the owned-files map so --check does not flag it as orphan.
	initPy, ok := files["__init__.py"]
	if !ok {
		t.Fatal("faceted output missing __init__.py")
	}

	// Must have the generated header.
	if !strings.Contains(string(initPy), "Code generated by pgdesign") {
		t.Error("__init__.py missing generated header")
	}
}

func TestPythonDDL_ExecutorTypeAnnotations(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	// fetch() must return list[dict[str, Any]], not list[dict].
	if !strings.Contains(executor, "list[dict[str, Any]]") {
		t.Error("executor fetch() should return list[dict[str, Any]], not list[dict]")
	}
	if strings.Contains(executor, "-> list[dict]:") {
		t.Error("executor still has untyped list[dict] return annotation")
	}

	// __aexit__ must have proper type annotations.
	if !strings.Contains(executor, "type[BaseException] | None") {
		t.Error("executor __aexit__ missing type[BaseException] | None annotation for exc_type")
	}
	if !strings.Contains(executor, "TracebackType | None") {
		t.Error("executor __aexit__ missing TracebackType | None annotation for exc_tb")
	}

	// Must import TracebackType from types.
	if !strings.Contains(executor, "from types import TracebackType") {
		t.Error("executor missing TracebackType import")
	}

	// Must import Any from typing.
	if !strings.Contains(executor, "Any") {
		t.Error("executor missing Any import")
	}
}

func TestPythonDDL_FacetedExecutorTypeAnnotations(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	// Same type annotation checks for faceted executor.
	if !strings.Contains(executor, "list[dict[str, Any]]") {
		t.Error("faceted executor fetch() should return list[dict[str, Any]]")
	}
	if !strings.Contains(executor, "type[BaseException] | None") {
		t.Error("faceted executor __aexit__ missing type annotations")
	}
	if !strings.Contains(executor, "from types import TracebackType") {
		t.Error("faceted executor missing TracebackType import")
	}
}

func TestPythonDDL_NonFacetedDDLStmt(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	out, _ := gen.Generate(schema)
	output := string(out)

	if !strings.Contains(output, `DDLStmt = namedtuple("DDLStmt"`) {
		t.Error("non-faceted output should contain DDLStmt namedtuple definition")
	}
	if !strings.Contains(output, "STATEMENTS: Final[list[DDLStmt]]") {
		t.Error("non-faceted output should use DDLStmt type annotation")
	}
	if !strings.Contains(output, "DDLStmt(") {
		t.Error("non-faceted output should contain DDLStmt( entries")
	}
	// Should NOT contain old 4-tuple format.
	lines := strings.Split(output, "\n")
	inStatements := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "STATEMENTS:") {
			inStatements = true
			continue
		}
		if inStatements && trimmed == "]" {
			break
		}
		if inStatements && strings.HasPrefix(trimmed, "(") && !strings.HasPrefix(trimmed, "(\"") {
			// Allow multi-line continuation but not raw tuples.
			continue
		}
		if inStatements && strings.HasPrefix(trimmed, "(\"") {
			t.Errorf("found raw tuple instead of DDLStmt: %s", trimmed[:min(60, len(trimmed))])
		}
	}
}

func TestPythonDDL_FacetedTableNames(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)

	// Each tables_*.py file should contain TABLE_NAMES.
	for name, content := range files {
		if strings.HasPrefix(name, "tables_") {
			if !strings.Contains(string(content), "TABLE_NAMES") {
				t.Errorf("%s should contain TABLE_NAMES", name)
			}
		}
	}

	// types.py should NOT contain TABLE_NAMES.
	typesContent := string(files["types.py"])
	if strings.Contains(typesContent, "TABLE_NAMES") {
		t.Error("types.py should not contain TABLE_NAMES")
	}
}

// -- 5.1: exclude_sections, SECTION_KINDS, name validation tests --

func TestPythonDDL_Executor_SectionKindsConstant(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "SECTION_KINDS: Final[frozenset[str]] = frozenset(s.kind for s in SECTIONS)") {
		t.Error("executor missing SECTION_KINDS constant")
	}
}

func TestPythonDDL_FacetedExecutor_SectionKindsConstant(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "SECTION_KINDS: Final[frozenset[str]] = frozenset(s.kind for s in SECTIONS)") {
		t.Error("faceted executor missing SECTION_KINDS constant")
	}
}

func TestPythonDDL_Executor_ExcludeSectionsParam(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "exclude_sections: Sequence[str] | None = None") {
		t.Error("executor execute() missing exclude_sections parameter")
	}
}

func TestPythonDDL_Executor_ExcludeSectionsValidation(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	// Mutual exclusion check.
	if !strings.Contains(executor, "Cannot specify both sections and exclude_sections") {
		t.Error("executor missing mutual exclusion validation for sections/exclude_sections")
	}

	// Unknown section name validation for sections param.
	if !strings.Contains(executor, "Unknown section(s):") {
		t.Error("executor missing unknown section name validation")
	}

	// The filter line should reference both sections and exclude_sections.
	if !strings.Contains(executor, "s.kind not in exclude_sections") {
		t.Error("executor filter should check exclude_sections")
	}
}

func TestPythonDDL_Executor_VerifyExcludeSections(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	// verify() should accept both sections and exclude_sections.
	verifyIdx := strings.Index(executor, "async def verify(")
	if verifyIdx < 0 {
		t.Fatal("executor missing verify function")
	}
	verifyBlock := executor[verifyIdx:]
	nextFunc := strings.Index(verifyBlock[1:], "\nasync def ")
	if nextFunc > 0 {
		verifyBlock = verifyBlock[:nextFunc+1]
	}

	if !strings.Contains(verifyBlock, "exclude_sections: Sequence[str] | None = None") {
		t.Error("verify() missing exclude_sections parameter")
	}
	if !strings.Contains(verifyBlock, "sections: Sequence[str] | None = None") {
		t.Error("verify() missing sections parameter")
	}
	if !strings.Contains(verifyBlock, "Cannot specify both sections and exclude_sections") {
		t.Error("verify() missing mutual exclusion validation")
	}
}

// -- 5.2: extension_stubs tests --

func TestPythonDDL_Executor_ExtensionStubsParam(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "extension_stubs: dict[str, str] | None = None") {
		t.Error("executor execute() missing extension_stubs parameter")
	}
}

func TestPythonDDL_Executor_ExtensionStubsLogic(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	// The extension_stubs substitution should check section kind and op name.
	if !strings.Contains(executor, `sec.kind == "extensions" and op.name in extension_stubs`) {
		t.Error("executor missing extension_stubs substitution logic")
	}
	if !strings.Contains(executor, "stmt = extension_stubs[op.name]") {
		t.Error("executor missing extension_stubs assignment")
	}
}

func TestPythonDDL_FacetedExecutor_ExtensionStubsParam(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "extension_stubs: dict[str, str] | None = None") {
		t.Error("faceted executor execute() missing extension_stubs parameter")
	}
}

func TestPythonDDL_FacetedExecutor_FinalImport(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeFaceted}
	files, _ := gen.GenerateFiles(schema)
	executor := string(files["schema_executor.py"])

	if !strings.Contains(executor, "from typing import Any, Final, Protocol, Sequence, runtime_checkable") {
		t.Error("faceted executor missing expected typing imports")
	}
	if !strings.Contains(executor, "from types import TracebackType") {
		t.Error("faceted executor missing TracebackType import")
	}
}

// -- 5.3: Self-contained split mode tests --

func TestPythonDDL_SelfContainedOutput(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeSelfContained}
	files, diags := gen.GenerateFiles(schema)
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("generation error: %s", d.Message)
		}
	}

	// Should have per-source files, no schema_executor.py.
	expectedFiles := []string{"split_trace.py", "split_dispatch.py"}
	for _, name := range expectedFiles {
		if _, ok := files[name]; !ok {
			t.Errorf("missing expected file %q in self-contained output", name)
		}
	}
	if _, ok := files["schema_executor.py"]; ok {
		t.Error("self-contained mode should not produce schema_executor.py")
	}
}

func TestPythonDDL_SelfContainedPreamble(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeSelfContained}
	files, _ := gen.GenerateFiles(schema)

	// Both files should contain the extension/schema preamble (schemas + extensions).
	for _, name := range []string{"split_trace.py", "split_dispatch.py"} {
		content := string(files[name])
		if !strings.Contains(content, "CREATE SCHEMA") {
			t.Errorf("%s should contain CREATE SCHEMA preamble", name)
		}
	}

	// Both files should contain enum type definition (preamble).
	for _, name := range []string{"split_trace.py", "split_dispatch.py"} {
		content := string(files[name])
		if !strings.Contains(content, "trace_status") {
			t.Errorf("%s should contain trace_status enum preamble", name)
		}
	}
}

func TestPythonDDL_SelfContainedPreambleIdempotent(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeSelfContained}
	files, _ := gen.GenerateFiles(schema)

	// Preamble tuples should use idempotent SQL (IF NOT EXISTS).
	for _, name := range []string{"split_trace.py", "split_dispatch.py"} {
		content := string(files[name])
		// Schema creation should be idempotent.
		if !strings.Contains(content, "IF NOT EXISTS") {
			t.Errorf("%s preamble should use IF NOT EXISTS for idempotent SQL", name)
		}
		// The enum preamble must use the valid DO-block form, never the
		// invalid CREATE TYPE IF NOT EXISTS syntax.
		if strings.Contains(content, "CREATE TYPE IF NOT EXISTS") {
			t.Errorf("%s preamble must not contain invalid CREATE TYPE IF NOT EXISTS syntax", name)
		}
		if !strings.Contains(content, "DO $$") || !strings.Contains(content, "typtype = 'e'") {
			t.Errorf("%s preamble enum should be a DO block with pg_type check", name)
		}
		if !strings.Contains(content, "typname = 'trace_status'") {
			t.Errorf("%s preamble enum DO block should reference trace_status", name)
		}
	}
}

func TestPythonDDL_SelfContainedTableSeparation(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeSelfContained}
	files, _ := gen.GenerateFiles(schema)

	traceContent := string(files["split_trace.py"])
	dispatchContent := string(files["split_dispatch.py"])

	// Trace file should contain spans and events tables.
	if !strings.Contains(traceContent, "CREATE TABLE") {
		t.Error("split_trace.py should contain CREATE TABLE")
	}
	if !strings.Contains(traceContent, "spans") {
		t.Error("split_trace.py should contain spans table")
	}
	if !strings.Contains(traceContent, "events") {
		t.Error("split_trace.py should contain events table")
	}

	// Dispatch file should contain tasks table.
	if !strings.Contains(dispatchContent, "tasks") {
		t.Error("split_dispatch.py should contain tasks table")
	}

	// Tables should not cross files (excluding preamble type references).
	// The table DDL for "tasks" should only be in dispatch, not trace.
	// We check for CREATE TABLE ... tasks specifically.
	if strings.Contains(traceContent, `"table", "tasks"`) {
		t.Error("split_trace.py should not contain tasks table tuple")
	}
	if strings.Contains(dispatchContent, `"table", "spans"`) {
		t.Error("split_dispatch.py should not contain spans table tuple")
	}
}

func TestPythonDDL_SelfContainedTableNames(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeSelfContained}
	files, _ := gen.GenerateFiles(schema)

	// Each file should have TABLE_NAMES for its own tables only.
	traceContent := string(files["split_trace.py"])
	dispatchContent := string(files["split_dispatch.py"])

	if !strings.Contains(traceContent, "TABLE_NAMES") {
		t.Error("split_trace.py should contain TABLE_NAMES")
	}
	if !strings.Contains(dispatchContent, "TABLE_NAMES") {
		t.Error("split_dispatch.py should contain TABLE_NAMES")
	}
}

func TestPythonDDL_SelfContainedNoExecutor(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeSelfContained}
	files, _ := gen.GenerateFiles(schema)

	// No file should contain executor constructs.
	for name, content := range files {
		s := string(content)
		if strings.Contains(s, "async def execute(") {
			t.Errorf("%s should not contain execute function in self-contained mode", name)
		}
		if strings.Contains(s, "class AsyncConnection") {
			t.Errorf("%s should not contain AsyncConnection in self-contained mode", name)
		}
	}
}

func TestPythonDDL_SelfContainedDDLStmt(t *testing.T) {
	schema := loadSplitTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeSelfContained}
	files, _ := gen.GenerateFiles(schema)

	// All files should use DDLStmt namedtuple.
	for name, content := range files {
		s := string(content)
		if !strings.Contains(s, `DDLStmt = namedtuple("DDLStmt"`) {
			t.Errorf("%s should contain DDLStmt namedtuple definition", name)
		}
		if !strings.Contains(s, "STATEMENTS: Final[list[DDLStmt]]") {
			t.Errorf("%s should use DDLStmt type annotation", name)
		}
	}
}

func TestPythonDDL_SelfContainedSingleSource(t *testing.T) {
	// Test with a schema from a single source file (ddl_input.toml).
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{SplitMode: SplitModeSelfContained}
	files, diags := gen.GenerateFiles(schema)
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("generation error: %s", d.Message)
		}
	}

	// Single-source schema should produce one file.
	if len(files) != 1 {
		t.Errorf("expected 1 file for single-source schema, got %d: %v", len(files), fileNames(files))
	}

	// The file should contain both preamble and table DDL.
	for _, content := range files {
		s := string(content)
		if !strings.Contains(s, "CREATE TABLE") {
			t.Error("single-source self-contained file should contain CREATE TABLE")
		}
		if !strings.Contains(s, "DDLStmt(") {
			t.Error("single-source self-contained file should contain DDLStmt entries")
		}
	}
}

// fileNames returns the sorted keys of a file map.
func fileNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestPythonDDL_SequenceIdempotentSQL verifies that standalone sequence tuples
// carry an idempotent variant (CREATE SEQUENCE IF NOT EXISTS). Regression test:
// sequence tuples used to set only SQL, leaving IdempotentSQL empty even though
// sql.CreateSequence supports an idempotent form.
func TestPythonDDL_SequenceIdempotentSQL(t *testing.T) {
	start := int64(100)
	schema := &model.Schema{
		Name: "test",
		Sequences: []model.Sequence{{
			Name:   "invoice_seq",
			Schema: "test",
			Start:  &start,
		}},
	}
	tuples, _, diags := buildTuples(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	found := false
	for _, tu := range tuples {
		if tu.Kind != "sequence" {
			continue
		}
		found = true
		if tu.IdempotentSQL == "" {
			t.Error("sequence tuple has empty IdempotentSQL; want CREATE SEQUENCE IF NOT EXISTS variant")
			continue
		}
		if !strings.Contains(tu.IdempotentSQL, "IF NOT EXISTS") {
			t.Errorf("sequence IdempotentSQL missing IF NOT EXISTS: %q", tu.IdempotentSQL)
		}
	}
	if !found {
		t.Fatal("no sequence tuple found")
	}
}
