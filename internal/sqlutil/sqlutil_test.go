package sqlutil

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
)

func TestParseExpr(t *testing.T) {
	t.Run("successful parse returns node and nil diagnostic", func(t *testing.T) {
		node, diag := ParseExpr("a + b", "test context")
		if node == nil {
			t.Fatal("expected non-nil node for valid expression")
		}
		if diag != nil {
			t.Fatalf("expected nil diagnostic, got: %s", diag.Message)
		}
	})

	t.Run("parse failure returns nil node and warning diagnostic", func(t *testing.T) {
		node, diag := ParseExpr("a + ", "check expression")
		if node != nil {
			t.Fatal("expected nil node for invalid expression")
		}
		if diag == nil {
			t.Fatal("expected non-nil diagnostic for invalid expression")
		}
		if diag.Severity != diagnostic.Warning {
			t.Errorf("expected Warning severity, got %v", diag.Severity)
		}
		if !strings.Contains(diag.Message, "at position") {
			t.Errorf("expected message to contain 'at position', got: %s", diag.Message)
		}
		if !strings.Contains(diag.Message, "check expression") {
			t.Errorf("expected message to contain context string, got: %s", diag.Message)
		}
	})

	t.Run("diagnostic message includes position from ParseError", func(t *testing.T) {
		// "a + " fails at position 4 (end of input after the operator)
		_, diag := ParseExpr("a + ", "pos test")
		if diag == nil {
			t.Fatal("expected non-nil diagnostic")
		}
		if !strings.Contains(diag.Message, "at position 4") {
			t.Errorf("expected message to contain 'at position 4', got: %s", diag.Message)
		}

		// ")" fails at position 0 (immediate unexpected token)
		_, diag2 := ParseExpr(")", "pos test 2")
		if diag2 == nil {
			t.Fatal("expected non-nil diagnostic")
		}
		if !strings.Contains(diag2.Message, "at position 0") {
			t.Errorf("expected message to contain 'at position 0', got: %s", diag2.Message)
		}
	})

	t.Run("diagnostic message includes error description", func(t *testing.T) {
		_, diag := ParseExpr("a + ", "desc test")
		if diag == nil {
			t.Fatal("expected non-nil diagnostic")
		}
		if !strings.Contains(diag.Message, "expression could not be fully analyzed") {
			t.Errorf("expected message prefix 'expression could not be fully analyzed', got: %s", diag.Message)
		}
	})

	t.Run("context string appears in diagnostic message", func(t *testing.T) {
		ctx := "generated column users.full_name"
		_, diag := ParseExpr("a + ", ctx)
		if diag == nil {
			t.Fatal("expected non-nil diagnostic")
		}
		if !strings.Contains(diag.Message, ctx) {
			t.Errorf("expected message to contain context %q, got: %s", ctx, diag.Message)
		}
	})
}
