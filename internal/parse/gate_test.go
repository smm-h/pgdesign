package parse

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
)

// hasCode reports whether any diagnostic carries the given code, and returns the
// first matching diagnostic for message assertions.
func hasCode(diags []diagnostic.Diagnostic, code string) (diagnostic.Diagnostic, bool) {
	for _, d := range diags {
		if d.Code == code {
			return d, true
		}
	}
	return diagnostic.Diagnostic{}, false
}

// TestGate_FormatVersionAbsent: a real-shaped schema document that lacks the
// top-level format_version gate field is rejected by the strictspec shape gate
// with STRICTSPEC_GATE_ABSENT, and the native walk is skipped (schema is nil).
func TestGate_FormatVersionAbsent(t *testing.T) {
	testenv.Isolate(t)
	// A valid pgdesign schema fragment WITHOUT format_version (a pre-stamp
	// corpus file copy).
	content := `[meta]
version = 16
schema = "shop"

[tables.orders]
comment = "Customer orders."

[tables.orders.columns.id]
type = "bigint"
`
	schema, diags := Bytes([]byte(content))
	if _, ok := hasCode(diags, "STRICTSPEC_GATE_ABSENT"); !ok {
		t.Fatalf("expected STRICTSPEC_GATE_ABSENT, got diags: %v", diags)
	}
	if schema != nil {
		t.Errorf("expected nil schema (walk skipped) on gate failure, got non-nil")
	}
}

// TestGate_UnknownKeyWithSuggestion: an unknown key close to a real one is a
// hard error (STRICTSPEC_KEY_UNKNOWN) carrying a did-you-mean suggestion in its
// message.
func TestGate_UnknownKeyWithSuggestion(t *testing.T) {
	testenv.Isolate(t)
	content := `format_version = 1

[tables.users]
comment = "Users."

[tables.users.columns.id]
type = "bigint"
nulable = true
`
	_, diags := Bytes([]byte(content))
	d, ok := hasCode(diags, "STRICTSPEC_KEY_UNKNOWN")
	if !ok {
		t.Fatalf("expected STRICTSPEC_KEY_UNKNOWN, got diags: %v", diags)
	}
	if !strings.Contains(d.Message, "nullable") {
		t.Errorf("expected did-you-mean suggestion of \"nullable\" in message, got: %q", d.Message)
	}
	if d.Table != "users" || d.Column != "id" {
		t.Errorf("expected Table=users Column=id from path, got Table=%q Column=%q", d.Table, d.Column)
	}
}

// TestGate_CompactColumnsForm: the compact `[tables.X.columns]` form, where a
// column maps directly to a type string (e.g. `id = "bigint"`) instead of a
// nested `[tables.X.columns.id]` record with a `type` key, was silently ignored
// by the old lenient parser. The strictspec shape gate now rejects it: a column
// position must be a record, so a bare string there is STRICTSPEC_TYPE_NOT_RECORD
// with the offending table/column named.
func TestGate_CompactColumnsForm(t *testing.T) {
	testenv.Isolate(t)
	content := `format_version = 1

[tables.users]
comment = "Users."

[tables.users.columns]
id = "bigint"
`
	schema, diags := Bytes([]byte(content))
	d, ok := hasCode(diags, "STRICTSPEC_TYPE_NOT_RECORD")
	if !ok {
		t.Fatalf("expected STRICTSPEC_TYPE_NOT_RECORD, got diags: %v", diags)
	}
	if !strings.Contains(d.Message, "record") {
		t.Errorf("expected a record-shape message, got: %q", d.Message)
	}
	if d.Table != "users" || d.Column != "id" {
		t.Errorf("expected Table=users Column=id from path, got Table=%q Column=%q", d.Table, d.Column)
	}
	if schema != nil {
		t.Errorf("expected nil schema (walk skipped) on gate failure, got non-nil")
	}
}

// TestGate_BadIdentifierLexeme: a value in an identifier-typed position that is
// not a valid identifier (leading digit) fails the identifier custom scalar's
// lexeme rule.
func TestGate_BadIdentifierLexeme(t *testing.T) {
	testenv.Isolate(t)
	content := `format_version = 1

[tables.users]
comment = "Users."
pk = ["9bad"]

[tables.users.columns.id]
type = "bigint"
`
	_, diags := Bytes([]byte(content))
	d, ok := hasCode(diags, "STRICTSPEC_SCALAR_LEXEME")
	if !ok {
		t.Fatalf("expected STRICTSPEC_SCALAR_LEXEME, got diags: %v", diags)
	}
	if !strings.Contains(d.Message, "identifier") {
		t.Errorf("expected identifier scalar in message, got: %q", d.Message)
	}
}

// TestGate_BadPgtypeLexeme: a column type with an embedded space breaks the
// pgtype surface-syntax lexeme rule.
func TestGate_BadPgtypeLexeme(t *testing.T) {
	testenv.Isolate(t)
	content := `format_version = 1

[tables.users]
comment = "Users."

[tables.users.columns.id]
type = "bi gint"
`
	_, diags := Bytes([]byte(content))
	d, ok := hasCode(diags, "STRICTSPEC_SCALAR_LEXEME")
	if !ok {
		t.Fatalf("expected STRICTSPEC_SCALAR_LEXEME, got diags: %v", diags)
	}
	if !strings.Contains(d.Message, "pgtype") {
		t.Errorf("expected pgtype scalar in message, got: %q", d.Message)
	}
	if d.Table != "users" || d.Column != "id" {
		t.Errorf("expected Table=users Column=id, got Table=%q Column=%q", d.Table, d.Column)
	}
}
