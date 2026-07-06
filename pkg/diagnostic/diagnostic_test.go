package diagnostic

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSeverityString(t *testing.T) {
	tests := []struct {
		s    Severity
		want string
	}{
		{Error, "error"},
		{Warning, "warning"},
		{Info, "info"},
		{Hint, "hint"},
		{Severity(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestHasErrors(t *testing.T) {
	yes := Diagnostics{
		{Severity: Warning, Message: "w"},
		{Severity: Error, Message: "e"},
	}
	if !yes.HasErrors() {
		t.Error("expected HasErrors() = true")
	}

	no := Diagnostics{
		{Severity: Warning, Message: "w"},
		{Severity: Info, Message: "i"},
	}
	if no.HasErrors() {
		t.Error("expected HasErrors() = false")
	}

	var empty Diagnostics
	if empty.HasErrors() {
		t.Error("expected HasErrors() = false for empty")
	}
}

func TestErrors(t *testing.T) {
	diags := Diagnostics{
		{Severity: Error, Code: "E001"},
		{Severity: Warning, Code: "W001"},
		{Severity: Error, Code: "E002"},
	}
	errs := diags.Errors()
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(errs))
	}
	if errs[0].Code != "E001" || errs[1].Code != "E002" {
		t.Errorf("unexpected error codes: %v", errs)
	}
}

func TestWarnings(t *testing.T) {
	diags := Diagnostics{
		{Severity: Error, Code: "E001"},
		{Severity: Warning, Code: "W001"},
		{Severity: Warning, Code: "W002"},
	}
	warns := diags.Warnings()
	if len(warns) != 2 {
		t.Fatalf("expected 2 warnings, got %d", len(warns))
	}
}

func TestRenderTerminal(t *testing.T) {
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
	}
	out := RenderTerminal(diags, false)
	if !strings.Contains(out, "error[E001]: column type is invalid") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if !strings.Contains(out, "--> schema.toml:users:email") {
		t.Errorf("expected location in output:\n%s", out)
	}
	if !strings.Contains(out, "= use text") {
		t.Errorf("expected suggestion in output:\n%s", out)
	}
}

func TestRenderTerminalEmpty(t *testing.T) {
	out := RenderTerminal(Diagnostics{}, false)
	if out != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}

func TestRenderJSON(t *testing.T) {
	diags := Diagnostics{
		{Severity: Error, Code: "E001", Message: "bad"},
	}
	out := RenderJSON(diags)
	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 item, got %d", len(parsed))
	}
	if parsed[0]["severity"] != "error" {
		t.Errorf("expected severity error, got %v", parsed[0]["severity"])
	}
}
