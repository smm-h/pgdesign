// Assessment: RLS codegen pattern expansion with new comparison operators
//
// The sqlexpr parser now supports comparison operators (>, <, >=, <=), LIKE,
// ILIKE, IN, NOT IN, BETWEEN, IS NULL, IS NOT NULL, IS DISTINCT FROM, plus
// float and NULL literals. This assessment examines which new RLS policy
// patterns can now be parsed and which could be recognized by codegen.
//
// Current codegen pattern detection (two layers):
//
// Layer 1 -- FilterGeneratable (codegen.go, line 85-109):
//   Quick match via AST walk. Sets matched=true for:
//   (a) Any ExistsExpr node anywhere in the tree.
//   (b) BinaryOp{Op: "="} where either side (after unwrapCast) is a
//       FuncCall named "current_setting".
//   Policies that don't match either pattern get a C001 warning and are
//   excluded from codegen entirely.
//
// Layer 2 -- Analysis functions (analysis.go):
//   detectOwnership: BinaryOp{Op: "="} with ColumnRef vs current_setting.
//   detectAllExistsLookups: ExistsExpr with join + flag conditions.
//   detectOrCompound: OR of ownership + single exists-lookup.
//   These are called by language-specific generators on policies that
//   already passed FilterGeneratable.
//
// New patterns that NOW PARSE successfully but are NOT YET MATCHED by codegen:
//
// 1. Comparison-based ownership (>, <, >=, <=, !=):
//    Example: created_at > current_setting('app.cutoff')::timestamptz
//    Parses to: BinaryOp{Op: ">"} with ColumnRef and FuncCall.
//    Status: Parseable. NOT matched by FilterGeneratable (only checks Op "=").
//            NOT detected by detectOwnership (only checks Op "=").
//    Expansion difficulty: LOW. FilterGeneratable's BinaryOp check at line 95
//    could accept a set of ops {"=", "!=", "<", ">", "<=", ">="} instead of
//    just "=". detectOwnership (analysis.go line 115) same change. The
//    ownershipCheck struct would need an Op field to carry the operator through
//    to code generators.
//
// 2. LIKE/ILIKE-based patterns with current_setting:
//    Example: name LIKE current_setting('app.prefix') || '%'
//    Parses to: BinaryOp{Op: "LIKE"} with ColumnRef and BinaryOp{Op: "||"}.
//    Status: Parseable. NOT matched (LIKE is not "=", and the right side is a
//            concat expression not a bare FuncCall).
//    Expansion difficulty: MEDIUM. The FilterGeneratable check would need to
//    recognize LIKE/ILIKE as matchable ops. The ownership detector would need
//    to walk into the right-side expression to find current_setting inside a
//    concatenation. Code generators would need a new pattern type for
//    prefix/suffix/contains matching.
//
// 3. IN-based static membership:
//    Example: role IN ('admin', 'moderator')
//    Parses to: BinaryOp{Op: "IN", Left: ColumnRef, Right: FuncCall{Name: "IN", Args: [StringLiteral...]}}.
//    Status: Parseable. NOT matched (no ExistsExpr, no current_setting).
//    Expansion difficulty: MEDIUM. This is a new pattern class entirely -- not
//    ownership (no current_setting) and not exists-lookup. Would need a new
//    staticMembership analysis type and a new FilterGeneratable match arm.
//    Code generators would emit a set-membership check.
//
// 4. NULL checks:
//    Example: deleted_at IS NULL
//    Parses to: UnaryOp{Op: "IS NULL", Operand: ColumnRef}.
//    Status: Parseable. NOT matched (no ExistsExpr, no BinaryOp with "=").
//    Expansion difficulty: LOW for detection, but requires a new pattern type.
//    IS NULL is a UnaryOp, not a BinaryOp, so FilterGeneratable's BinaryOp
//    switch arm does not see it. Would need a new case for UnaryOp with
//    "IS NULL" / "IS NOT NULL".
//
// 5. BETWEEN-based ranges:
//    Example: age BETWEEN 18 AND 65
//    Parses to: BinaryOp{Op: "BETWEEN", Left: ColumnRef, Right: BinaryOp{Op: "AND", Left: IntLiteral, Right: IntLiteral}}.
//    Status: Parseable. NOT matched (BETWEEN is not "=").
//    Expansion difficulty: MEDIUM. New pattern class for range checks. The
//    nested AND in the right side is a parser implementation detail that
//    analysis code would need to handle. Could combine with comparison-based
//    ownership if the bounds involve current_setting calls.
//
// 6. Compound patterns (new ops + existing patterns):
//    Example: deleted_at IS NULL AND player_id::text = current_setting('app.player_id')
//    Parses successfully. The ownership part IS matched by FilterGeneratable
//    because the walk finds the BinaryOp{Op: "="} with current_setting.
//    However, the IS NULL part is silently ignored -- codegen generates a
//    validator for just the ownership check, not the full expression.
//    This is existing behavior: FilterGeneratable matches if ANY sub-node
//    matches, but generators only extract the patterns they recognize.
//
// 7. NOT IN with current_setting:
//    Example: status NOT IN ('banned', 'suspended')
//    Parses to: BinaryOp{Op: "NOT IN", Left: ColumnRef, Right: FuncCall{Name: "NOT IN", Args: [...]}}.
//    Status: Parseable. NOT matched. Same expansion path as IN.
//
// 8. IS DISTINCT FROM:
//    Example: tenant_id IS DISTINCT FROM current_setting('app.tenant')::int
//    Parses to: BinaryOp{Op: "IS DISTINCT FROM"} with current_setting.
//    Status: Parseable. NOT matched (op is not "=").
//    Expansion difficulty: LOW if grouped with the comparison operators.
//    Semantically equivalent to != but NULL-safe.
//
// Summary of expansion priorities:
//
// HIGH VALUE, LOW EFFORT:
//   Comparison-based ownership (extend Op check to a set)
//   IS DISTINCT FROM (same mechanism)
//
// HIGH VALUE, MEDIUM EFFORT:
//   NULL checks (new UnaryOp match arm + new pattern type)
//   IN-based static membership (new pattern class)
//
// LOWER PRIORITY:
//   LIKE/ILIKE patterns (complex right-side analysis)
//   BETWEEN ranges (new pattern class + nested AND handling)
//
// The tests below verify that all these expressions parse without error,
// documenting the gap between "parseable" and "matched by codegen."

package codegen

import (
	"fmt"
	"testing"

	"github.com/smm-h/pgdesign/internal/sqlutil"
)

// TestParseCoverage_ComparisonOwnership verifies that ownership patterns using
// comparison operators beyond "=" parse successfully. These are parseable but
// not yet matched by FilterGeneratable or detectOwnership.
func TestParseCoverage_ComparisonOwnership(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{
			name: "greater than with current_setting",
			expr: "created_at > current_setting('app.cutoff')::timestamptz",
		},
		{
			name: "less than with current_setting",
			expr: "created_at < current_setting('app.cutoff')::timestamptz",
		},
		{
			name: "greater than or equal with current_setting",
			expr: "score >= current_setting('app.min_score')::int",
		},
		{
			name: "less than or equal with current_setting",
			expr: "level <= current_setting('app.max_level')::int",
		},
		{
			name: "not equal with current_setting",
			expr: "status != current_setting('app.banned_status')",
		},
		{
			name: "is distinct from with current_setting",
			expr: "tenant_id IS DISTINCT FROM current_setting('app.tenant')::int",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ast, diag := sqlutil.ParseExpr(tc.expr, "test")
			if diag != nil {
				t.Fatalf("parse failed: %s", diag.Message)
			}

			// Verify these are NOT matched by FilterGeneratable today.
			policies := []PolicyContext{{
				PolicyName: "test_policy",
				ErrorCode:  "test_code",
				Using:      tc.expr,
			}}
			result, _ := FilterGeneratable(policies)
			if len(result) != 0 {
				t.Errorf("expected policy to NOT be matched by FilterGeneratable, but it was matched")
			}

			// Verify the AST is non-nil (parse succeeded).
			if ast == nil {
				t.Fatal("AST is nil despite no parse error")
			}
		})
	}
}

// TestParseCoverage_LikePatterns verifies that LIKE/ILIKE expressions with
// current_setting parse successfully. These involve concatenation on the
// pattern side and are not yet matched by codegen.
func TestParseCoverage_LikePatterns(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{
			name: "LIKE with current_setting prefix",
			expr: "name LIKE current_setting('app.prefix') || '%'",
		},
		{
			name: "ILIKE with current_setting",
			expr: "email ILIKE current_setting('app.domain_pattern')",
		},
		{
			name: "NOT LIKE",
			expr: "name NOT LIKE '%_deleted'",
		},
		{
			name: "NOT ILIKE",
			expr: "email NOT ILIKE '%@blocked.com'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ast, diag := sqlutil.ParseExpr(tc.expr, "test")
			if diag != nil {
				t.Fatalf("parse failed: %s", diag.Message)
			}

			// Verify these are NOT matched by FilterGeneratable today.
			policies := []PolicyContext{{
				PolicyName: "test_policy",
				ErrorCode:  "test_code",
				Using:      tc.expr,
			}}
			result, _ := FilterGeneratable(policies)
			if len(result) != 0 {
				t.Errorf("expected policy to NOT be matched by FilterGeneratable, but it was matched")
			}

			if ast == nil {
				t.Fatal("AST is nil despite no parse error")
			}
		})
	}
}

// TestParseCoverage_InMembership verifies that IN/NOT IN membership checks
// parse successfully. These are a new pattern class not recognized by codegen.
func TestParseCoverage_InMembership(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{
			name: "IN with string literals",
			expr: "role IN ('admin', 'moderator')",
		},
		{
			name: "NOT IN with string literals",
			expr: "status NOT IN ('banned', 'suspended', 'deleted')",
		},
		{
			name: "IN with integer literals",
			expr: "tier IN (1, 2, 3)",
		},
		{
			name: "IN with current_setting",
			expr: "role IN (current_setting('app.allowed_role'))",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ast, diag := sqlutil.ParseExpr(tc.expr, "test")
			if diag != nil {
				t.Fatalf("parse failed: %s", diag.Message)
			}

			policies := []PolicyContext{{
				PolicyName: "test_policy",
				ErrorCode:  "test_code",
				Using:      tc.expr,
			}}
			result, _ := FilterGeneratable(policies)
			if len(result) != 0 {
				t.Errorf("expected policy to NOT be matched by FilterGeneratable, but it was matched")
			}

			if ast == nil {
				t.Fatal("AST is nil despite no parse error")
			}
		})
	}
}

// TestParseCoverage_NullChecks verifies that IS NULL / IS NOT NULL expressions
// parse successfully. These produce UnaryOp nodes, not BinaryOp, so they
// require a new match arm in FilterGeneratable.
func TestParseCoverage_NullChecks(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{
			name: "IS NULL",
			expr: "deleted_at IS NULL",
		},
		{
			name: "IS NOT NULL",
			expr: "verified_at IS NOT NULL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ast, diag := sqlutil.ParseExpr(tc.expr, "test")
			if diag != nil {
				t.Fatalf("parse failed: %s", diag.Message)
			}

			policies := []PolicyContext{{
				PolicyName: "test_policy",
				ErrorCode:  "test_code",
				Using:      tc.expr,
			}}
			result, _ := FilterGeneratable(policies)
			if len(result) != 0 {
				t.Errorf("expected policy to NOT be matched by FilterGeneratable, but it was matched")
			}

			if ast == nil {
				t.Fatal("AST is nil despite no parse error")
			}
		})
	}
}

// TestParseCoverage_BetweenRanges verifies that BETWEEN expressions parse
// successfully. The parser represents BETWEEN as
// BinaryOp{Op: "BETWEEN", Right: BinaryOp{Op: "AND", Left: lo, Right: hi}}.
func TestParseCoverage_BetweenRanges(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{
			name: "BETWEEN with integer literals",
			expr: "age BETWEEN 18 AND 65",
		},
		{
			name: "BETWEEN with float literals",
			expr: "rating BETWEEN 0.0 AND 5.0",
		},
		{
			name: "BETWEEN with current_setting bounds",
			expr: "score BETWEEN current_setting('app.min')::int AND current_setting('app.max')::int",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ast, diag := sqlutil.ParseExpr(tc.expr, "test")
			if diag != nil {
				t.Fatalf("parse failed: %s", diag.Message)
			}

			policies := []PolicyContext{{
				PolicyName: "test_policy",
				ErrorCode:  "test_code",
				Using:      tc.expr,
			}}
			result, _ := FilterGeneratable(policies)
			if len(result) != 0 {
				t.Errorf("expected policy to NOT be matched by FilterGeneratable, but it was matched")
			}

			if ast == nil {
				t.Fatal("AST is nil despite no parse error")
			}
		})
	}
}

// TestParseCoverage_CompoundWithNewOps verifies that compound expressions
// mixing new operators with existing matched patterns parse successfully.
// These partially match: the existing pattern (ownership =, EXISTS) is found
// by FilterGeneratable's walk, but the new-operator parts are silently ignored
// by the analysis layer.
func TestParseCoverage_CompoundWithNewOps(t *testing.T) {
	cases := []struct {
		name          string
		expr          string
		expectMatched bool
		matchReason   string
	}{
		{
			name:          "IS NULL AND ownership",
			expr:          "deleted_at IS NULL AND player_id::text = current_setting('app.player_id')",
			expectMatched: true,
			matchReason:   "walk finds BinaryOp{Op: \"=\"} with current_setting",
		},
		{
			name:          "ownership AND comparison",
			expr:          "player_id::text = current_setting('app.player_id') AND score > 100",
			expectMatched: true,
			matchReason:   "walk finds BinaryOp{Op: \"=\"} with current_setting",
		},
		{
			name:          "EXISTS AND IS NOT NULL",
			expr:          "EXISTS (SELECT 1 FROM game.settings WHERE player_id = sender_id AND chat_enabled = true) AND verified_at IS NOT NULL",
			expectMatched: true,
			matchReason:   "walk finds ExistsExpr",
		},
		{
			name:          "IS NULL AND role IN - no existing pattern",
			expr:          "deleted_at IS NULL AND role IN ('admin', 'moderator')",
			expectMatched: false,
			matchReason:   "neither IS NULL nor IN matches any current pattern",
		},
		{
			name:          "comparison ownership OR EXISTS",
			expr:          "created_at > current_setting('app.cutoff')::timestamptz OR EXISTS (SELECT 1 FROM game.settings WHERE player_id = sender_id AND active = true)",
			expectMatched: true,
			matchReason:   "walk finds ExistsExpr (but the > ownership side is not detected)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ast, diag := sqlutil.ParseExpr(tc.expr, "test")
			if diag != nil {
				t.Fatalf("parse failed: %s", diag.Message)
			}

			policies := []PolicyContext{{
				PolicyName: "test_policy",
				ErrorCode:  "test_code",
				Using:      tc.expr,
			}}
			result, _ := FilterGeneratable(policies)
			matched := len(result) > 0

			if matched != tc.expectMatched {
				t.Errorf("expected matched=%v (reason: %s), got matched=%v",
					tc.expectMatched, tc.matchReason, matched)
			}

			if ast == nil {
				t.Fatal("AST is nil despite no parse error")
			}
		})
	}
}

// TestParseCoverage_NewLiterals verifies that new literal types (float, NULL)
// parse correctly in RLS-like expressions.
func TestParseCoverage_NewLiterals(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{
			name: "float literal comparison",
			expr: "balance > 0.0",
		},
		{
			name: "NULL literal in equality",
			expr: "deleted_at = NULL",
		},
		{
			name: "float with current_setting",
			expr: "rate <= current_setting('app.max_rate')::float",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, diag := sqlutil.ParseExpr(tc.expr, "test")
			if diag != nil {
				t.Fatalf("parse failed for %q: %s", tc.expr, diag.Message)
			}
		})
	}
}

// TestParseCoverage_DetectOwnershipGap verifies that detectOwnership only
// recognizes "=" and misses other comparison operators, even when
// current_setting is present. This documents the specific gap in analysis.go.
func TestParseCoverage_DetectOwnershipGap(t *testing.T) {
	// This IS detected (baseline).
	t.Run("equality is detected", func(t *testing.T) {
		ast, diag := sqlutil.ParseExpr("player_id::text = current_setting('app.player_id')", "test")
		if diag != nil {
			t.Fatalf("parse failed: %s", diag.Message)
		}
		result := detectOwnership(ast)
		if result == nil {
			t.Fatal("expected detectOwnership to find the pattern")
		}
		if result.column != "player_id" {
			t.Errorf("expected column player_id, got %s", result.column)
		}
	})

	// These are NOT detected despite having current_setting.
	ops := []struct {
		name string
		expr string
	}{
		{"greater than", "created_at > current_setting('app.cutoff')::timestamptz"},
		{"less than", "created_at < current_setting('app.cutoff')::timestamptz"},
		{"greater or equal", "score >= current_setting('app.min_score')::int"},
		{"less or equal", "level <= current_setting('app.max_level')::int"},
		{"not equal", "status != current_setting('app.banned_status')"},
		{"is distinct from", "tenant_id IS DISTINCT FROM current_setting('app.tenant')::int"},
	}

	for _, tc := range ops {
		t.Run(fmt.Sprintf("%s is not detected", tc.name), func(t *testing.T) {
			ast, diag := sqlutil.ParseExpr(tc.expr, "test")
			if diag != nil {
				t.Fatalf("parse failed: %s", diag.Message)
			}
			result := detectOwnership(ast)
			if result != nil {
				t.Errorf("expected detectOwnership to NOT find the pattern (op %q not supported), but it did", tc.name)
			}
			_ = ast
		})
	}
}
