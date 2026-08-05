package diagnostic

import (
	"encoding/json"
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"
)

func TestHasErrors_WithErrors(t *testing.T) {
	testenv.Isolate(t)
	diags := Diagnostics{
		{Severity: Warning, Code: "W001", Message: "a warning"},
		{Severity: Error, Code: "E001", Message: "an error"},
	}
	if !diags.HasErrors() {
		t.Fatal("expected HasErrors() to return true")
	}
}

func TestHasErrors_WithoutErrors(t *testing.T) {
	testenv.Isolate(t)
	diags := Diagnostics{
		{Severity: Warning, Code: "W001", Message: "a warning"},
		{Severity: Info, Code: "I001", Message: "info"},
		{Severity: Hint, Message: "a hint"},
	}
	if diags.HasErrors() {
		t.Fatal("expected HasErrors() to return false")
	}
}

func TestHasErrors_Empty(t *testing.T) {
	testenv.Isolate(t)
	var diags Diagnostics
	if diags.HasErrors() {
		t.Fatal("expected HasErrors() to return false for empty diagnostics")
	}
}

func TestErrors(t *testing.T) {
	testenv.Isolate(t)
	diags := Diagnostics{
		{Severity: Error, Code: "E001", Message: "first error"},
		{Severity: Warning, Code: "W001", Message: "a warning"},
		{Severity: Error, Code: "E002", Message: "second error"},
		{Severity: Info, Message: "info"},
	}
	errors := diags.Errors()
	if len(errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(errors))
	}
	if errors[0].Code != "E001" {
		t.Errorf("expected first error code E001, got %s", errors[0].Code)
	}
	if errors[1].Code != "E002" {
		t.Errorf("expected second error code E002, got %s", errors[1].Code)
	}
}

func TestWarnings(t *testing.T) {
	testenv.Isolate(t)
	diags := Diagnostics{
		{Severity: Error, Code: "E001", Message: "an error"},
		{Severity: Warning, Code: "W001", Message: "first warning"},
		{Severity: Warning, Code: "W002", Message: "second warning"},
		{Severity: Hint, Message: "a hint"},
	}
	warnings := diags.Warnings()
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d", len(warnings))
	}
	if warnings[0].Code != "W001" {
		t.Errorf("expected first warning code W001, got %s", warnings[0].Code)
	}
	if warnings[1].Code != "W002" {
		t.Errorf("expected second warning code W002, got %s", warnings[1].Code)
	}
}

func TestRenderTerminal_NoColor(t *testing.T) {
	testenv.Isolate(t)
	diags := Diagnostics{
		{
			Severity:   Error,
			Code:       "E001",
			File:       "schema.toml",
			Table:      "users",
			Column:     "email",
			Message:    "column type is invalid",
			Suggestion: "use 'text' or 'varchar(255)'",
		},
		{
			Severity: Warning,
			Code:     "W001",
			File:     "schema.toml",
			Table:    "orders",
			Message:  "table has no primary key",
		},
	}

	output := RenderTerminal(diags, false)

	if !strings.Contains(output, "error[E001]: column type is invalid") {
		t.Errorf("expected error line in output, got:\n%s", output)
	}
	if !strings.Contains(output, "--> schema.toml:users:email") {
		t.Errorf("expected location line in output, got:\n%s", output)
	}
	if !strings.Contains(output, "= use 'text' or 'varchar(255)'") {
		t.Errorf("expected suggestion line in output, got:\n%s", output)
	}
	if !strings.Contains(output, "warning[W001]: table has no primary key") {
		t.Errorf("expected warning line in output, got:\n%s", output)
	}
	if !strings.Contains(output, "--> schema.toml:orders") {
		t.Errorf("expected location line for warning, got:\n%s", output)
	}
}

func TestRenderTerminal_WithColor(t *testing.T) {
	testenv.Isolate(t)
	diags := Diagnostics{
		{Severity: Error, Code: "E001", Message: "bad"},
	}

	output := RenderTerminal(diags, true)

	// Should contain ANSI escape for red
	if !strings.Contains(output, "\033[31m") {
		t.Errorf("expected ANSI red escape in color output, got:\n%s", output)
	}
	if !strings.Contains(output, "\033[0m") {
		t.Errorf("expected ANSI reset in color output, got:\n%s", output)
	}
}

func TestRenderTerminal_Empty(t *testing.T) {
	testenv.Isolate(t)
	output := RenderTerminal(Diagnostics{}, false)
	if output != "" {
		t.Errorf("expected empty string for empty diagnostics, got: %q", output)
	}
}

func TestRenderTerminal_GroupedBySeverityAndFile(t *testing.T) {
	testenv.Isolate(t)
	// Diagnostics in deliberately scrambled order: mixed files and severities.
	diags := Diagnostics{
		{Severity: Warning, Code: "W001", File: "schema.toml", Table: "orders", Message: "missing index on orders"},
		{Severity: Error, Code: "E002", File: "types.toml", Message: "unknown type bigserial"},
		{Severity: Hint, File: "schema.toml", Table: "users", Message: "consider adding a check constraint"},
		{Severity: Error, Code: "E001", File: "schema.toml", Table: "users", Column: "email", Message: "column type is invalid"},
		{Severity: Info, Code: "I001", File: "types.toml", Message: "type alias resolved"},
		{Severity: Warning, Code: "W002", File: "types.toml", Message: "deprecated type used"},
	}

	// Verify the input slice is not mutated.
	origFirst := diags[0]

	output := RenderTerminal(diags, false)

	if diags[0] != origFirst {
		t.Fatal("RenderTerminal mutated the input slice")
	}

	// Expected order:
	//   schema.toml group: Error (E001), Warning (W001), Hint
	//   types.toml group:  Error (E002), Warning (W002), Info (I001)
	lines := strings.Split(output, "\n")

	// Collect non-empty, non-indented lines (headers + severity lines).
	type entry struct {
		line string
		idx  int
	}
	var significant []entry
	for i, l := range lines {
		if l == "" || strings.HasPrefix(l, "  ") {
			continue
		}
		significant = append(significant, entry{l, i})
	}

	// We expect: "schema.toml", error[E001], warning[W001], hint,
	//            "types.toml", error[E002], warning[W002], info[I001]
	expected := []string{
		"schema.toml",
		"error[E001]: column type is invalid",
		"warning[W001]: missing index on orders",
		"hint: consider adding a check constraint",
		"types.toml",
		"error[E002]: unknown type bigserial",
		"warning[W002]: deprecated type used",
		"info[I001]: type alias resolved",
	}

	if len(significant) != len(expected) {
		t.Fatalf("expected %d significant lines, got %d\nOutput:\n%s\nSignificant:\n%v",
			len(expected), len(significant), output, significant)
	}

	for i, want := range expected {
		if significant[i].line != want {
			t.Errorf("line %d: expected %q, got %q", i, want, significant[i].line)
		}
	}

	// Verify file headers appear before their group's diagnostics.
	schemaIdx := strings.Index(output, "schema.toml\n")
	typesIdx := strings.Index(output, "types.toml\n")
	if schemaIdx == -1 || typesIdx == -1 {
		t.Fatal("expected file group headers in output")
	}
	if schemaIdx >= typesIdx {
		t.Error("expected schema.toml group before types.toml group (alphabetical)")
	}
}

func TestRenderTerminal_DoesNotMutateInput(t *testing.T) {
	testenv.Isolate(t)
	diags := Diagnostics{
		{Severity: Warning, File: "b.toml", Message: "second"},
		{Severity: Error, File: "a.toml", Message: "first"},
	}
	// Take a snapshot of the original order.
	origCodes := []string{diags[0].File, diags[1].File}

	RenderTerminal(diags, false)

	if diags[0].File != origCodes[0] || diags[1].File != origCodes[1] {
		t.Fatal("RenderTerminal mutated the input slice order")
	}
}

func TestRenderJSON(t *testing.T) {
	testenv.Isolate(t)
	diags := Diagnostics{
		{
			Severity:   Error,
			Code:       "E001",
			File:       "schema.toml",
			Table:      "users",
			Column:     "email",
			Message:    "column type is invalid",
			Suggestion: "use text",
		},
		{
			Severity: Warning,
			Code:     "W001",
			Message:  "missing index",
		},
	}

	output := RenderJSON(diags)

	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("RenderJSON produced invalid JSON: %v\nOutput: %s", err, output)
	}

	if len(parsed) != 2 {
		t.Fatalf("expected 2 items in JSON array, got %d", len(parsed))
	}

	first := parsed[0]
	if first["severity"] != "error" {
		t.Errorf("expected severity 'error', got %v", first["severity"])
	}
	if first["code"] != "E001" {
		t.Errorf("expected code 'E001', got %v", first["code"])
	}
	if first["file"] != "schema.toml" {
		t.Errorf("expected file 'schema.toml', got %v", first["file"])
	}
	if first["table"] != "users" {
		t.Errorf("expected table 'users', got %v", first["table"])
	}
	if first["column"] != "email" {
		t.Errorf("expected column 'email', got %v", first["column"])
	}
	if first["message"] != "column type is invalid" {
		t.Errorf("expected message 'column type is invalid', got %v", first["message"])
	}
	if first["suggestion"] != "use text" {
		t.Errorf("expected suggestion 'use text', got %v", first["suggestion"])
	}

	second := parsed[1]
	if second["severity"] != "warning" {
		t.Errorf("expected severity 'warning', got %v", second["severity"])
	}
	// Empty fields should be omitted
	if _, ok := second["file"]; ok {
		t.Errorf("expected 'file' to be omitted when empty, but it was present")
	}
	if _, ok := second["table"]; ok {
		t.Errorf("expected 'table' to be omitted when empty, but it was present")
	}
}

func TestRenderJSON_Empty(t *testing.T) {
	testenv.Isolate(t)
	output := RenderJSON(Diagnostics{})

	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("RenderJSON produced invalid JSON for empty input: %v", err)
	}
	if len(parsed) != 0 {
		t.Errorf("expected empty JSON array, got %d items", len(parsed))
	}
}
