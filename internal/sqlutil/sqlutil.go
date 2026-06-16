// Package sqlutil provides a shared adapter between the sqlexpr parser and the
// diagnostic package, preventing each consumer from reimplementing
// parse-error-to-diagnostic conversion.
//
// ParseExpr parses a SQL expression via sqlexpr.Parse and converts any
// ParseError into a diagnostic.Warning with position information. The returned
// diagnostic intentionally has no diagnostic code; consumers assign their own
// codes since the same parse failure means different things in different
// contexts (e.g., E213 for generated column validation, C001/C002 for codegen
// checks).
package sqlutil

import (
	"errors"
	"fmt"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/sqlexpr"
)

// ParseExpr parses a SQL expression and returns the AST node. On failure, it
// returns a warning diagnostic with position information extracted from
// ParseError. The context string identifies the expression for error messages
// (e.g., "generated column users.full_name").
func ParseExpr(expr, context string) (sqlexpr.Node, *diagnostic.Diagnostic) {
	node, err := sqlexpr.Parse(expr)
	if err == nil {
		return node, nil
	}
	var pe *sqlexpr.ParseError
	if errors.As(err, &pe) {
		return nil, &diagnostic.Diagnostic{
			Severity: diagnostic.Warning,
			Message:  fmt.Sprintf("expression could not be fully analyzed: %s at position %d in %s", pe.Msg, pe.Pos, context),
		}
	}
	return nil, &diagnostic.Diagnostic{
		Severity: diagnostic.Warning,
		Message:  fmt.Sprintf("expression could not be fully analyzed: %v in %s", err, context),
	}
}
