// Package diagnostic provides shared error/warning types used by all compiler passes.
package diagnostic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Severity represents the severity level of a diagnostic.
type Severity int

const (
	Error   Severity = iota
	Warning Severity = iota
	Info    Severity = iota
	Hint    Severity = iota
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	case Info:
		return "info"
	case Hint:
		return "hint"
	default:
		return "unknown"
	}
}

// Diagnostic represents a single diagnostic message from a compiler pass.
type Diagnostic struct {
	Severity   Severity
	Code       string
	File       string
	Table      string
	Column     string
	Message    string
	Suggestion string
}

// Diagnostics is a collection of diagnostics with convenience methods.
type Diagnostics []Diagnostic

// HasErrors returns true if any diagnostic has Error severity.
func (d Diagnostics) HasErrors() bool {
	for _, diag := range d {
		if diag.Severity == Error {
			return true
		}
	}
	return false
}

// Errors returns all diagnostics with Error severity.
func (d Diagnostics) Errors() Diagnostics {
	var result Diagnostics
	for _, diag := range d {
		if diag.Severity == Error {
			result = append(result, diag)
		}
	}
	return result
}

// Warnings returns all diagnostics with Warning severity.
func (d Diagnostics) Warnings() Diagnostics {
	var result Diagnostics
	for _, diag := range d {
		if diag.Severity == Warning {
			result = append(result, diag)
		}
	}
	return result
}

// RenderTerminal renders diagnostics as human-readable terminal output.
// When color is true, ANSI escape codes are used.
func RenderTerminal(diags Diagnostics, color bool) string {
	if len(diags) == 0 {
		return ""
	}

	var b strings.Builder
	for i, diag := range diags {
		if i > 0 {
			b.WriteByte('\n')
		}

		severityStr := diag.Severity.String()
		if color {
			severityStr = colorize(diag.Severity, severityStr)
		}

		// Format: severity[code]: message
		b.WriteString(severityStr)
		if diag.Code != "" {
			b.WriteString("[")
			b.WriteString(diag.Code)
			b.WriteString("]")
		}
		b.WriteString(": ")
		b.WriteString(diag.Message)
		b.WriteByte('\n')

		// Location line
		location := buildLocation(diag)
		if location != "" {
			b.WriteString("  --> ")
			b.WriteString(location)
			b.WriteByte('\n')
		}

		// Suggestion
		if diag.Suggestion != "" {
			b.WriteString("  = ")
			b.WriteString(diag.Suggestion)
			b.WriteByte('\n')
		}
	}

	return b.String()
}

// RenderJSON renders diagnostics as a JSON array string.
func RenderJSON(diags Diagnostics) string {
	type jsonDiag struct {
		Severity   string `json:"severity"`
		Code       string `json:"code,omitempty"`
		File       string `json:"file,omitempty"`
		Table      string `json:"table,omitempty"`
		Column     string `json:"column,omitempty"`
		Message    string `json:"message"`
		Suggestion string `json:"suggestion,omitempty"`
	}

	out := make([]jsonDiag, len(diags))
	for i, d := range diags {
		out[i] = jsonDiag{
			Severity:   d.Severity.String(),
			Code:       d.Code,
			File:       d.File,
			Table:      d.Table,
			Column:     d.Column,
			Message:    d.Message,
			Suggestion: d.Suggestion,
		}
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Sprintf("[{\"error\": %q}]", err.Error())
	}
	return string(data)
}

func colorize(s Severity, text string) string {
	var code string
	switch s {
	case Error:
		code = "31" // red
	case Warning:
		code = "33" // yellow
	case Info:
		code = "36" // cyan
	case Hint:
		code = "32" // green
	default:
		return text
	}
	return fmt.Sprintf("\033[%sm%s\033[0m", code, text)
}

func buildLocation(diag Diagnostic) string {
	parts := make([]string, 0, 3)
	if diag.File != "" {
		parts = append(parts, diag.File)
	}
	if diag.Table != "" {
		parts = append(parts, diag.Table)
	}
	if diag.Column != "" {
		parts = append(parts, diag.Column)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ":")
}
