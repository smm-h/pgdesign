package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/sqlparse"
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

func TestPythonDDL_GoldenFile(t *testing.T) {
	schema := loadTestSchema(t)
	gen := &PythonDDLGenerator{}
	out, diags := gen.Generate(schema)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	expectedPath := filepath.Join("testdata", "ddl_expected.py")
	expectedBytes, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("cannot read expected file: %v", err)
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

	// Count tuples: lines matching the tuple pattern inside STATEMENTS.
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
		if inStatements && strings.HasPrefix(trimmed, "(") {
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

	// Extract phase numbers from tuples. Each tuple ends with ", <phase>),".
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
		if !inStatements {
			continue
		}
		// Find the last number before the closing "),"
		// Tuples end with ", <phase>),"
		idx := strings.LastIndex(trimmed, "),")
		if idx < 0 {
			// Handle last tuple without trailing comma
			idx = strings.LastIndex(trimmed, ")")
		}
		if idx < 0 {
			continue
		}
		// Walk backward to find the phase integer
		end := idx
		start := end - 1
		for start >= 0 && trimmed[start] >= '0' && trimmed[start] <= '9' {
			start--
		}
		if start >= end-1 {
			continue
		}
		phaseStr := trimmed[start+1 : end]
		phase := 0
		for _, ch := range phaseStr {
			phase = phase*10 + int(ch-'0')
		}
		phases = append(phases, phase)
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
		if inStatements && strings.HasPrefix(trimmed, "(") {
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
	if !strings.Contains(output, "STATEMENTS: Final[list[tuple[str, str, str | None, int]]]") {
		t.Error("missing STATEMENTS type annotation")
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

	if !strings.Contains(output, "STATEMENTS: Final[list[tuple[str, str, str | None, int]]] = [\n]") {
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
				{Name: "id", PGType: "uuid", NotNull: true},
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
