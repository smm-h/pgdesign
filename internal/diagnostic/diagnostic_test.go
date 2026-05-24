package diagnostic

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHasErrors_WithErrors(t *testing.T) {
	diags := Diagnostics{
		{Severity: Warning, Code: "W001", Message: "a warning"},
		{Severity: Error, Code: "E001", Message: "an error"},
	}
	if !diags.HasErrors() {
		t.Fatal("expected HasErrors() to return true")
	}
}

func TestHasErrors_WithoutErrors(t *testing.T) {
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
	var diags Diagnostics
	if diags.HasErrors() {
		t.Fatal("expected HasErrors() to return false for empty diagnostics")
	}
}

func TestErrors(t *testing.T) {
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
	output := RenderTerminal(Diagnostics{}, false)
	if output != "" {
		t.Errorf("expected empty string for empty diagnostics, got: %q", output)
	}
}

func TestRenderJSON(t *testing.T) {
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
	output := RenderJSON(Diagnostics{})

	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("RenderJSON produced invalid JSON for empty input: %v", err)
	}
	if len(parsed) != 0 {
		t.Errorf("expected empty JSON array, got %d items", len(parsed))
	}
}
