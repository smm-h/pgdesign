// Package codegen generates type-safe application-layer code from resolved pgdesign schemas across six target languages including Go, TypeScript, and Python.
//
// It extracts RLS policies and produces language-specific validators that can
// pre-check policy conditions before hitting the database.
package codegen

import (
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sqlexpr"
	"github.com/smm-h/pgdesign/internal/sqlutil"
)

// The Generator and MultiFileGenerator interfaces this package's generators
// implement live in pkg/genkit (the single provenance/generator contract).
// Go interface satisfaction is structural, so the generators here need no
// import of genkit for the interfaces; they do import genkit for the shared
// provenance-header helper (see Stamp/Header).

// PolicyContext holds the data needed to generate a validator for one policy.
type PolicyContext struct {
	SchemaName   string
	TableName    string
	PolicyName   string
	Operation    string
	Using        string
	WithCheck    string
	ErrorCode    string
	ErrorMessage string
	AST          sqlexpr.Node // parsed expression AST, set by FilterGeneratable
}

// ExtractPolicies collects all policies from a schema into PolicyContexts.
func ExtractPolicies(schema *model.Schema) []PolicyContext {
	var contexts []PolicyContext
	for _, tbl := range schema.Tables {
		for _, pol := range tbl.Policies {
			contexts = append(contexts, PolicyContext{
				SchemaName:   tbl.Schema,
				TableName:    tbl.Name,
				PolicyName:   pol.Name,
				Operation:    pol.Operation,
				Using:        pol.Using,
				WithCheck:    pol.WithCheck,
				ErrorCode:    pol.ErrorCode,
				ErrorMessage: pol.ErrorMessage,
			})
		}
	}
	return contexts
}

// FilterGeneratable returns policies that have an ErrorCode and whose expression
// parses into an AST matching at least one supported codegen pattern:
//
//  1. Exists-lookup: an ExistsExpr node (privacy check pattern)
//  2. Ownership: a BinaryOp{Op: "="} where one side (unwrapping Cast) is a
//     FuncCall named "current_setting"
//
// Policies that lack an ErrorCode are silently skipped. Policies whose
// expression cannot be parsed produce a C002 diagnostic, and those that do not
// match any pattern produce a C001 diagnostic; both are excluded from the result.
func FilterGeneratable(policies []PolicyContext) ([]PolicyContext, []diagnostic.Diagnostic) {
	var result []PolicyContext
	var diags []diagnostic.Diagnostic
	for _, p := range policies {
		if p.ErrorCode == "" {
			continue
		}
		expr := p.WithCheck
		if expr == "" {
			expr = p.Using
		}

		ast, parseDiag := sqlutil.ParseExpr(expr, fmt.Sprintf("policy %s.%s", p.TableName, p.PolicyName))
		if parseDiag != nil {
			parseDiag.Code = "C002"
			parseDiag.Table = p.TableName
			diags = append(diags, *parseDiag)
			continue
		}

		matched := false
		sqlexpr.Walk(ast, func(n sqlexpr.Node) bool {
			if matched {
				return false
			}
			switch node := n.(type) {
			case *sqlexpr.ExistsExpr:
				matched = true
				return false
			case *sqlexpr.BinaryOp:
				if node.Op == "=" {
					left := unwrapCast(node.Left)
					right := unwrapCast(node.Right)
					if fc, ok := left.(*sqlexpr.FuncCall); ok && strings.EqualFold(fc.Name, "current_setting") {
						matched = true
						return false
					}
					if fc, ok := right.(*sqlexpr.FuncCall); ok && strings.EqualFold(fc.Name, "current_setting") {
						matched = true
						return false
					}
				}
			}
			return true
		})

		if matched {
			p.AST = ast
			result = append(result, p)
		} else {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Code:     "C001",
				Table:    p.TableName,
				Message:  fmt.Sprintf("policy %q: expression does not match any generatable pattern", p.PolicyName),
			})
		}
	}
	return result, diags
}
