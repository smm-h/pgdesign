package sqlexpr

import (
	"strings"
	"testing"
)

// helpers for AST assertions

func assertColumnRef(t *testing.T, node Node, parts ...string) *ColumnRef {
	t.Helper()
	cr, ok := node.(*ColumnRef)
	if !ok {
		t.Fatalf("expected *ColumnRef, got %T", node)
	}
	if len(cr.Parts) != len(parts) {
		t.Fatalf("expected %d parts, got %d: %v", len(parts), len(cr.Parts), cr.Parts)
	}
	for i, p := range parts {
		if cr.Parts[i] != p {
			t.Fatalf("part[%d]: expected %q, got %q", i, p, cr.Parts[i])
		}
	}
	return cr
}

func assertBoolLiteral(t *testing.T, node Node, value bool) {
	t.Helper()
	bl, ok := node.(*BoolLiteral)
	if !ok {
		t.Fatalf("expected *BoolLiteral, got %T", node)
	}
	if bl.Value != value {
		t.Fatalf("expected %v, got %v", value, bl.Value)
	}
}

func assertIntLiteral(t *testing.T, node Node, value int) {
	t.Helper()
	il, ok := node.(*IntLiteral)
	if !ok {
		t.Fatalf("expected *IntLiteral, got %T", node)
	}
	if il.Value != value {
		t.Fatalf("expected %d, got %d", value, il.Value)
	}
}

func assertStringLiteral(t *testing.T, node Node, value string) {
	t.Helper()
	sl, ok := node.(*StringLiteral)
	if !ok {
		t.Fatalf("expected *StringLiteral, got %T", node)
	}
	if sl.Value != value {
		t.Fatalf("expected %q, got %q", value, sl.Value)
	}
}

func assertBinaryOp(t *testing.T, node Node, op string) *BinaryOp {
	t.Helper()
	bo, ok := node.(*BinaryOp)
	if !ok {
		t.Fatalf("expected *BinaryOp, got %T", node)
	}
	if bo.Op != op {
		t.Fatalf("expected op %q, got %q", op, bo.Op)
	}
	return bo
}

func assertUnaryOp(t *testing.T, node Node, op string) *UnaryOp {
	t.Helper()
	uo, ok := node.(*UnaryOp)
	if !ok {
		t.Fatalf("expected *UnaryOp, got %T", node)
	}
	if uo.Op != op {
		t.Fatalf("expected op %q, got %q", op, uo.Op)
	}
	return uo
}

func assertFuncCall(t *testing.T, node Node, name string, argCount int) *FuncCall {
	t.Helper()
	fc, ok := node.(*FuncCall)
	if !ok {
		t.Fatalf("expected *FuncCall, got %T", node)
	}
	if fc.Name != name {
		t.Fatalf("expected func name %q, got %q", name, fc.Name)
	}
	if len(fc.Args) != argCount {
		t.Fatalf("expected %d args, got %d", argCount, len(fc.Args))
	}
	return fc
}

func assertCast(t *testing.T, node Node, typeName string) *Cast {
	t.Helper()
	c, ok := node.(*Cast)
	if !ok {
		t.Fatalf("expected *Cast, got %T", node)
	}
	if c.TypeName != typeName {
		t.Fatalf("expected type %q, got %q", typeName, c.TypeName)
	}
	return c
}

func assertParenExpr(t *testing.T, node Node) *ParenExpr {
	t.Helper()
	pe, ok := node.(*ParenExpr)
	if !ok {
		t.Fatalf("expected *ParenExpr, got %T", node)
	}
	return pe
}

func assertExistsExpr(t *testing.T, node Node) *ExistsExpr {
	t.Helper()
	ee, ok := node.(*ExistsExpr)
	if !ok {
		t.Fatalf("expected *ExistsExpr, got %T", node)
	}
	return ee
}

func assertCaseExpr(t *testing.T, node Node, whenCount int) *CaseExpr {
	t.Helper()
	ce, ok := node.(*CaseExpr)
	if !ok {
		t.Fatalf("expected *CaseExpr, got %T", node)
	}
	if len(ce.Whens) != whenCount {
		t.Fatalf("expected %d WHEN clauses, got %d", whenCount, len(ce.Whens))
	}
	return ce
}

func TestParseLiterals(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		node, err := Parse("true")
		if err != nil {
			t.Fatal(err)
		}
		assertBoolLiteral(t, node, true)
	})

	t.Run("false", func(t *testing.T) {
		node, err := Parse("false")
		if err != nil {
			t.Fatal(err)
		}
		assertBoolLiteral(t, node, false)
	})

	t.Run("integer", func(t *testing.T) {
		node, err := Parse("42")
		if err != nil {
			t.Fatal(err)
		}
		assertIntLiteral(t, node, 42)
	})

	t.Run("string", func(t *testing.T) {
		node, err := Parse("'hello'")
		if err != nil {
			t.Fatal(err)
		}
		assertStringLiteral(t, node, "hello")
	})
}

func TestParseColumnRefs(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		node, err := Parse("owner_id")
		if err != nil {
			t.Fatal(err)
		}
		assertColumnRef(t, node, "owner_id")
	})

	t.Run("qualified_two", func(t *testing.T) {
		node, err := Parse("table.column")
		if err != nil {
			t.Fatal(err)
		}
		assertColumnRef(t, node, "table", "column")
	})

	t.Run("qualified_three", func(t *testing.T) {
		node, err := Parse("schema.table.column")
		if err != nil {
			t.Fatal(err)
		}
		assertColumnRef(t, node, "schema", "table", "column")
	})
}

func TestParseComparisons(t *testing.T) {
	t.Run("equals", func(t *testing.T) {
		node, err := Parse("a = b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "=")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})

	t.Run("not_equals_bang", func(t *testing.T) {
		node, err := Parse("a != b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "!=")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})

	t.Run("not_equals_diamond", func(t *testing.T) {
		node, err := Parse("a <> b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "<>")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})
}

func TestParseLogicalOps(t *testing.T) {
	t.Run("NOT", func(t *testing.T) {
		node, err := Parse("NOT x")
		if err != nil {
			t.Fatal(err)
		}
		uo := assertUnaryOp(t, node, "NOT")
		assertColumnRef(t, uo.Operand, "x")
	})

	t.Run("AND", func(t *testing.T) {
		node, err := Parse("a AND b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "AND")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})

	t.Run("OR", func(t *testing.T) {
		node, err := Parse("a OR b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "OR")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})

	t.Run("precedence_and_binds_tighter", func(t *testing.T) {
		// a OR b AND c => OR(a, AND(b, c))
		node, err := Parse("a OR b AND c")
		if err != nil {
			t.Fatal(err)
		}
		or := assertBinaryOp(t, node, "OR")
		assertColumnRef(t, or.Left, "a")
		and := assertBinaryOp(t, or.Right, "AND")
		assertColumnRef(t, and.Left, "b")
		assertColumnRef(t, and.Right, "c")
	})

	t.Run("parens_override_precedence", func(t *testing.T) {
		// (a OR b) AND c => AND(Paren(OR(a, b)), c)
		node, err := Parse("(a OR b) AND c")
		if err != nil {
			t.Fatal(err)
		}
		and := assertBinaryOp(t, node, "AND")
		pe := assertParenExpr(t, and.Left)
		or := assertBinaryOp(t, pe.Inner, "OR")
		assertColumnRef(t, or.Left, "a")
		assertColumnRef(t, or.Right, "b")
		assertColumnRef(t, and.Right, "c")
	})
}

func TestParseCast(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		node, err := Parse("x::text")
		if err != nil {
			t.Fatal(err)
		}
		c := assertCast(t, node, "text")
		assertColumnRef(t, c.Expr, "x")
	})

	t.Run("uuid", func(t *testing.T) {
		node, err := Parse("x::uuid")
		if err != nil {
			t.Fatal(err)
		}
		c := assertCast(t, node, "uuid")
		assertColumnRef(t, c.Expr, "x")
	})

	t.Run("CAST_text", func(t *testing.T) {
		node, err := Parse("CAST(x AS text)")
		if err != nil {
			t.Fatal(err)
		}
		c := assertCast(t, node, "text")
		assertColumnRef(t, c.Expr, "x")
	})

	t.Run("CAST_integer", func(t *testing.T) {
		node, err := Parse("CAST(price AS integer)")
		if err != nil {
			t.Fatal(err)
		}
		c := assertCast(t, node, "integer")
		assertColumnRef(t, c.Expr, "price")
	})

	t.Run("CAST_lowercase", func(t *testing.T) {
		node, err := Parse("cast(x as uuid)")
		if err != nil {
			t.Fatal(err)
		}
		c := assertCast(t, node, "uuid")
		assertColumnRef(t, c.Expr, "x")
	})

	t.Run("CAST_nested_expr", func(t *testing.T) {
		node, err := Parse("CAST(x + 1 AS integer)")
		if err != nil {
			t.Fatal(err)
		}
		c := assertCast(t, node, "integer")
		if _, ok := c.Expr.(*BinaryOp); !ok {
			t.Fatalf("expected *BinaryOp inside CAST, got %T", c.Expr)
		}
	})

	t.Run("CAST_missing_paren", func(t *testing.T) {
		_, err := Parse("CAST x AS text)")
		if err == nil {
			t.Fatal("expected error for missing '(' after CAST")
		}
	})

	t.Run("CAST_missing_AS", func(t *testing.T) {
		_, err := Parse("CAST(x text)")
		if err == nil {
			t.Fatal("expected error for missing AS in CAST")
		}
	})
}

func TestParseFuncCall(t *testing.T) {
	t.Run("current_setting", func(t *testing.T) {
		node, err := Parse("current_setting('app.player_id')")
		if err != nil {
			t.Fatal(err)
		}
		fc := assertFuncCall(t, node, "current_setting", 1)
		assertStringLiteral(t, fc.Args[0], "app.player_id")
	})

	t.Run("lower", func(t *testing.T) {
		node, err := Parse("lower(name)")
		if err != nil {
			t.Fatal(err)
		}
		fc := assertFuncCall(t, node, "lower", 1)
		assertColumnRef(t, fc.Args[0], "name")
	})
}

func TestParseRLSExpressions(t *testing.T) {
	t.Run("owner_check", func(t *testing.T) {
		// owner_id = current_setting('app.player_id')::uuid
		node, err := Parse("owner_id = current_setting('app.player_id')::uuid")
		if err != nil {
			t.Fatal(err)
		}
		eq := assertBinaryOp(t, node, "=")
		assertColumnRef(t, eq.Left, "owner_id")
		cast := assertCast(t, eq.Right, "uuid")
		fc := assertFuncCall(t, cast.Expr, "current_setting", 1)
		assertStringLiteral(t, fc.Args[0], "app.player_id")
	})

	t.Run("compound_and_exists", func(t *testing.T) {
		input := "sender_id::text = current_setting('app.player_id') AND EXISTS (SELECT 1 FROM game.player_privacy_settings WHERE player_id = sender_id AND chat_enabled = true)"
		node, err := Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		and := assertBinaryOp(t, node, "AND")

		// left: sender_id::text = current_setting('app.player_id')
		eq := assertBinaryOp(t, and.Left, "=")
		cast := assertCast(t, eq.Left, "text")
		assertColumnRef(t, cast.Expr, "sender_id")
		fc := assertFuncCall(t, eq.Right, "current_setting", 1)
		assertStringLiteral(t, fc.Args[0], "app.player_id")

		// right: EXISTS (SELECT ...)
		exists := assertExistsExpr(t, and.Right)
		sel := exists.Subquery
		if len(sel.Columns) != 1 {
			t.Fatalf("expected 1 column, got %d", len(sel.Columns))
		}
		assertIntLiteral(t, sel.Columns[0], 1)
		assertColumnRef(t, sel.From, "game", "player_privacy_settings")

		// WHERE: player_id = sender_id AND chat_enabled = true
		whereAnd := assertBinaryOp(t, sel.Where, "AND")
		eq1 := assertBinaryOp(t, whereAnd.Left, "=")
		assertColumnRef(t, eq1.Left, "player_id")
		assertColumnRef(t, eq1.Right, "sender_id")
		eq2 := assertBinaryOp(t, whereAnd.Right, "=")
		assertColumnRef(t, eq2.Left, "chat_enabled")
		assertBoolLiteral(t, eq2.Right, true)
	})

	t.Run("compound_or_exists", func(t *testing.T) {
		input := "player_id::text = current_setting('app.player_id') OR EXISTS (SELECT 1 FROM game.player_privacy_settings WHERE player_id = player_profile.player_id AND public_profile = true)"
		node, err := Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		or := assertBinaryOp(t, node, "OR")

		// left: player_id::text = current_setting('app.player_id')
		eq := assertBinaryOp(t, or.Left, "=")
		cast := assertCast(t, eq.Left, "text")
		assertColumnRef(t, cast.Expr, "player_id")
		fc := assertFuncCall(t, eq.Right, "current_setting", 1)
		assertStringLiteral(t, fc.Args[0], "app.player_id")

		// right: EXISTS (SELECT ...)
		exists := assertExistsExpr(t, or.Right)
		sel := exists.Subquery
		if len(sel.Columns) != 1 {
			t.Fatalf("expected 1 column, got %d", len(sel.Columns))
		}
		assertIntLiteral(t, sel.Columns[0], 1)
		assertColumnRef(t, sel.From, "game", "player_privacy_settings")

		// WHERE: player_id = player_profile.player_id AND public_profile = true
		whereAnd := assertBinaryOp(t, sel.Where, "AND")
		eq1 := assertBinaryOp(t, whereAnd.Left, "=")
		assertColumnRef(t, eq1.Left, "player_id")
		assertColumnRef(t, eq1.Right, "player_profile", "player_id")
		eq2 := assertBinaryOp(t, whereAnd.Right, "=")
		assertColumnRef(t, eq2.Left, "public_profile")
		assertBoolLiteral(t, eq2.Right, true)
	})
}

func TestCollectColumnRefs(t *testing.T) {
	t.Run("owner_check", func(t *testing.T) {
		node, err := Parse("owner_id = current_setting('app.player_id')::uuid")
		if err != nil {
			t.Fatal(err)
		}
		refs := CollectColumnRefs(node)
		if len(refs) != 1 {
			t.Fatalf("expected 1 column ref, got %d", len(refs))
		}
		if refs[0].Parts[0] != "owner_id" {
			t.Fatalf("expected owner_id, got %v", refs[0].Parts)
		}
	})

	t.Run("compound_and_exists", func(t *testing.T) {
		input := "sender_id::text = current_setting('app.player_id') AND EXISTS (SELECT 1 FROM game.player_privacy_settings WHERE player_id = sender_id AND chat_enabled = true)"
		node, err := Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		refs := CollectColumnRefs(node)
		// Expected refs: sender_id, game.player_privacy_settings (FROM),
		// player_id, sender_id, chat_enabled
		if len(refs) != 5 {
			t.Fatalf("expected 5 column refs, got %d", len(refs))
		}
		// Verify specific refs exist
		found := make(map[string]bool)
		for _, ref := range refs {
			key := strings.Join(ref.Parts, ".")
			found[key] = true
		}
		expected := []string{"sender_id", "game.player_privacy_settings", "player_id", "chat_enabled"}
		for _, e := range expected {
			if !found[e] {
				t.Errorf("missing expected column ref %q", e)
			}
		}
	})
}

func TestParseErrors(t *testing.T) {
	t.Run("empty_string", func(t *testing.T) {
		_, err := Parse("")
		if err == nil {
			t.Fatal("expected error for empty input")
		}
	})

	t.Run("unclosed_paren", func(t *testing.T) {
		_, err := Parse("(a")
		if err == nil {
			t.Fatal("expected error for unclosed parenthesis")
		}
	})

	t.Run("unexpected_token", func(t *testing.T) {
		_, err := Parse("= =")
		if err == nil {
			t.Fatal("expected error for unexpected token")
		}
	})

	t.Run("unclosed_string", func(t *testing.T) {
		_, err := Parse("'hello")
		if err == nil {
			t.Fatal("expected error for unclosed string literal")
		}
	})
}

func TestParseConcat(t *testing.T) {
	t.Run("double_pipe", func(t *testing.T) {
		// first_name || ' ' || last_name => ||( ||(first_name, ' '), last_name )
		node, err := Parse("first_name || ' ' || last_name")
		if err != nil {
			t.Fatal(err)
		}
		outer := assertBinaryOp(t, node, "||")
		inner := assertBinaryOp(t, outer.Left, "||")
		assertColumnRef(t, inner.Left, "first_name")
		assertStringLiteral(t, inner.Right, " ")
		assertColumnRef(t, outer.Right, "last_name")

		refs := CollectColumnRefs(node)
		if len(refs) != 2 {
			t.Fatalf("expected 2 column refs, got %d", len(refs))
		}
		if refs[0].Parts[0] != "first_name" {
			t.Fatalf("expected first_name, got %v", refs[0].Parts)
		}
		if refs[1].Parts[0] != "last_name" {
			t.Fatalf("expected last_name, got %v", refs[1].Parts)
		}
	})
}

func TestParseArithmetic(t *testing.T) {
	t.Run("multiply", func(t *testing.T) {
		node, err := Parse("price * quantity")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "*")
		assertColumnRef(t, bo.Left, "price")
		assertColumnRef(t, bo.Right, "quantity")
	})

	t.Run("addition", func(t *testing.T) {
		node, err := Parse("price + tax")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "+")
		assertColumnRef(t, bo.Left, "price")
		assertColumnRef(t, bo.Right, "tax")
	})

	t.Run("mul_binds_tighter_than_add", func(t *testing.T) {
		// a * b + c => +(*(a, b), c)
		node, err := Parse("a * b + c")
		if err != nil {
			t.Fatal(err)
		}
		add := assertBinaryOp(t, node, "+")
		mul := assertBinaryOp(t, add.Left, "*")
		assertColumnRef(t, mul.Left, "a")
		assertColumnRef(t, mul.Right, "b")
		assertColumnRef(t, add.Right, "c")

		refs := CollectColumnRefs(node)
		if len(refs) != 3 {
			t.Fatalf("expected 3 column refs, got %d", len(refs))
		}
		if refs[0].Parts[0] != "a" || refs[1].Parts[0] != "b" || refs[2].Parts[0] != "c" {
			t.Fatalf("expected [a, b, c], got %v", refs)
		}
	})
}

func TestParseCaseExpr(t *testing.T) {
	t.Run("simple_case", func(t *testing.T) {
		node, err := Parse("CASE WHEN status = 'active' THEN 1 ELSE 0 END")
		if err != nil {
			t.Fatal(err)
		}
		ce := assertCaseExpr(t, node, 1)
		cond := assertBinaryOp(t, ce.Whens[0].Condition, "=")
		assertColumnRef(t, cond.Left, "status")
		assertStringLiteral(t, cond.Right, "active")
		assertIntLiteral(t, ce.Whens[0].Result, 1)
		assertIntLiteral(t, ce.Else, 0)

		refs := CollectColumnRefs(node)
		if len(refs) != 1 {
			t.Fatalf("expected 1 column ref, got %d", len(refs))
		}
		if refs[0].Parts[0] != "status" {
			t.Fatalf("expected status, got %v", refs[0].Parts)
		}
	})
}

func TestParseCoalesce(t *testing.T) {
	t.Run("coalesce", func(t *testing.T) {
		node, err := Parse("COALESCE(nickname, first_name)")
		if err != nil {
			t.Fatal(err)
		}
		fc := assertFuncCall(t, node, "COALESCE", 2)
		assertColumnRef(t, fc.Args[0], "nickname")
		assertColumnRef(t, fc.Args[1], "first_name")

		refs := CollectColumnRefs(node)
		if len(refs) != 2 {
			t.Fatalf("expected 2 column refs, got %d", len(refs))
		}
		if refs[0].Parts[0] != "nickname" {
			t.Fatalf("expected nickname, got %v", refs[0].Parts)
		}
		if refs[1].Parts[0] != "first_name" {
			t.Fatalf("expected first_name, got %v", refs[1].Parts)
		}
	})
}

func assertFloatLiteral(t *testing.T, node Node, value float64) {
	t.Helper()
	fl, ok := node.(*FloatLiteral)
	if !ok {
		t.Fatalf("expected *FloatLiteral, got %T", node)
	}
	if fl.Value != value {
		t.Fatalf("expected %f, got %f", value, fl.Value)
	}
}

func assertNullLiteral(t *testing.T, node Node) {
	t.Helper()
	_, ok := node.(*NullLiteral)
	if !ok {
		t.Fatalf("expected *NullLiteral, got %T", node)
	}
}

func TestParseNullLiteral(t *testing.T) {
	t.Run("bare_null", func(t *testing.T) {
		node, err := Parse("NULL")
		if err != nil {
			t.Fatal(err)
		}
		assertNullLiteral(t, node)
	})

	t.Run("null_case_insensitive", func(t *testing.T) {
		node, err := Parse("null")
		if err != nil {
			t.Fatal(err)
		}
		assertNullLiteral(t, node)
	})

	t.Run("coalesce_with_null", func(t *testing.T) {
		node, err := Parse("COALESCE(x, NULL)")
		if err != nil {
			t.Fatal(err)
		}
		fc := assertFuncCall(t, node, "COALESCE", 2)
		assertColumnRef(t, fc.Args[0], "x")
		assertNullLiteral(t, fc.Args[1])
	})

	t.Run("null_in_comparison", func(t *testing.T) {
		// Note: "x = NULL" is valid SQL syntax (though semantically wrong)
		node, err := Parse("x = NULL")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "=")
		assertColumnRef(t, bo.Left, "x")
		assertNullLiteral(t, bo.Right)
	})
}

func TestParseFloatLiteral(t *testing.T) {
	t.Run("simple_float", func(t *testing.T) {
		node, err := Parse("3.14")
		if err != nil {
			t.Fatal(err)
		}
		assertFloatLiteral(t, node, 3.14)
	})

	t.Run("float_in_comparison", func(t *testing.T) {
		node, err := Parse("val >= 3.14")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, ">=")
		assertColumnRef(t, bo.Left, "val")
		assertFloatLiteral(t, bo.Right, 3.14)
	})
}

func TestParseComparisonOperators(t *testing.T) {
	t.Run("less_than", func(t *testing.T) {
		node, err := Parse("a < b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "<")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})

	t.Run("greater_than", func(t *testing.T) {
		node, err := Parse("a > b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, ">")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})

	t.Run("less_equal", func(t *testing.T) {
		node, err := Parse("a <= b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "<=")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})

	t.Run("greater_equal", func(t *testing.T) {
		node, err := Parse("a >= b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, ">=")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})
}

func TestParseDivisionModulo(t *testing.T) {
	t.Run("division", func(t *testing.T) {
		node, err := Parse("a / b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "/")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})

	t.Run("modulo", func(t *testing.T) {
		node, err := Parse("a % b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "%")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})

	t.Run("left_associative_chain", func(t *testing.T) {
		// a * b / c % d => %(/(*(a, b), c), d)
		node, err := Parse("a * b / c % d")
		if err != nil {
			t.Fatal(err)
		}
		mod := assertBinaryOp(t, node, "%")
		div := assertBinaryOp(t, mod.Left, "/")
		mul := assertBinaryOp(t, div.Left, "*")
		assertColumnRef(t, mul.Left, "a")
		assertColumnRef(t, mul.Right, "b")
		assertColumnRef(t, div.Right, "c")
		assertColumnRef(t, mod.Right, "d")
	})
}

func TestParseIsNull(t *testing.T) {
	t.Run("is_null", func(t *testing.T) {
		node, err := Parse("x IS NULL")
		if err != nil {
			t.Fatal(err)
		}
		uo := assertUnaryOp(t, node, "IS NULL")
		assertColumnRef(t, uo.Operand, "x")
	})

	t.Run("is_not_null", func(t *testing.T) {
		node, err := Parse("x IS NOT NULL")
		if err != nil {
			t.Fatal(err)
		}
		uo := assertUnaryOp(t, node, "IS NOT NULL")
		assertColumnRef(t, uo.Operand, "x")
	})

	t.Run("is_null_and_is_not_null", func(t *testing.T) {
		// x IS NOT NULL AND y IS NULL => AND(IS NOT NULL(x), IS NULL(y))
		node, err := Parse("x IS NOT NULL AND y IS NULL")
		if err != nil {
			t.Fatal(err)
		}
		and := assertBinaryOp(t, node, "AND")
		left := assertUnaryOp(t, and.Left, "IS NOT NULL")
		assertColumnRef(t, left.Operand, "x")
		right := assertUnaryOp(t, and.Right, "IS NULL")
		assertColumnRef(t, right.Operand, "y")
	})

	t.Run("is_without_null_errors", func(t *testing.T) {
		_, err := Parse("x IS foo")
		if err == nil {
			t.Fatal("expected error for IS without NULL")
		}
	})
}

func TestParseIn(t *testing.T) {
	t.Run("in_list", func(t *testing.T) {
		node, err := Parse("x IN (1, 2, 3)")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "IN")
		assertColumnRef(t, bo.Left, "x")
		fc, ok := bo.Right.(*FuncCall)
		if !ok {
			t.Fatalf("expected *FuncCall for IN list, got %T", bo.Right)
		}
		if len(fc.Args) != 3 {
			t.Fatalf("expected 3 items in IN list, got %d", len(fc.Args))
		}
		assertIntLiteral(t, fc.Args[0], 1)
		assertIntLiteral(t, fc.Args[1], 2)
		assertIntLiteral(t, fc.Args[2], 3)
	})

	t.Run("not_in_list", func(t *testing.T) {
		node, err := Parse("x NOT IN (1, 2, 3)")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "NOT IN")
		assertColumnRef(t, bo.Left, "x")
		fc, ok := bo.Right.(*FuncCall)
		if !ok {
			t.Fatalf("expected *FuncCall for NOT IN list, got %T", bo.Right)
		}
		if len(fc.Args) != 3 {
			t.Fatalf("expected 3 items in NOT IN list, got %d", len(fc.Args))
		}
	})

	t.Run("in_without_parens_errors", func(t *testing.T) {
		_, err := Parse("x IN 1, 2, 3")
		if err == nil {
			t.Fatal("expected error for IN without parens")
		}
	})
}

func TestParseBetween(t *testing.T) {
	t.Run("simple_between", func(t *testing.T) {
		node, err := Parse("x BETWEEN 1 AND 10")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "BETWEEN")
		assertColumnRef(t, bo.Left, "x")
		andRange := assertBinaryOp(t, bo.Right, "AND")
		assertIntLiteral(t, andRange.Left, 1)
		assertIntLiteral(t, andRange.Right, 10)
	})

	t.Run("between_and_disambiguation", func(t *testing.T) {
		// x BETWEEN 1 AND 10 AND y = 5
		// Should parse as: AND(BETWEEN(x, AND(1, 10)), =(y, 5))
		// The AND inside BETWEEN binds to BETWEEN, the second AND is boolean
		node, err := Parse("x BETWEEN 1 AND 10 AND y = 5")
		if err != nil {
			t.Fatal(err)
		}
		and := assertBinaryOp(t, node, "AND")

		// Left: x BETWEEN 1 AND 10
		between := assertBinaryOp(t, and.Left, "BETWEEN")
		assertColumnRef(t, between.Left, "x")
		andRange := assertBinaryOp(t, between.Right, "AND")
		assertIntLiteral(t, andRange.Left, 1)
		assertIntLiteral(t, andRange.Right, 10)

		// Right: y = 5
		eq := assertBinaryOp(t, and.Right, "=")
		assertColumnRef(t, eq.Left, "y")
		assertIntLiteral(t, eq.Right, 5)
	})

	t.Run("between_without_and_errors", func(t *testing.T) {
		_, err := Parse("x BETWEEN 1 OR 10")
		if err == nil {
			t.Fatal("expected error for BETWEEN without AND")
		}
	})
}

func TestParseLike(t *testing.T) {
	t.Run("like", func(t *testing.T) {
		node, err := Parse("name LIKE '%foo%'")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "LIKE")
		assertColumnRef(t, bo.Left, "name")
		assertStringLiteral(t, bo.Right, "%foo%")
	})

	t.Run("ilike", func(t *testing.T) {
		node, err := Parse("name ILIKE '%foo%'")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "ILIKE")
		assertColumnRef(t, bo.Left, "name")
		assertStringLiteral(t, bo.Right, "%foo%")
	})

	t.Run("not_like", func(t *testing.T) {
		node, err := Parse("name NOT LIKE '%bar%'")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "NOT LIKE")
		assertColumnRef(t, bo.Left, "name")
		assertStringLiteral(t, bo.Right, "%bar%")
	})

	t.Run("not_ilike", func(t *testing.T) {
		node, err := Parse("name NOT ILIKE '%bar%'")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "NOT ILIKE")
		assertColumnRef(t, bo.Left, "name")
		assertStringLiteral(t, bo.Right, "%bar%")
	})

	t.Run("like_and_comparison_combined", func(t *testing.T) {
		// name LIKE '%foo%' AND val >= 3.14
		node, err := Parse("name LIKE '%foo%' AND val >= 3.14")
		if err != nil {
			t.Fatal(err)
		}
		and := assertBinaryOp(t, node, "AND")
		like := assertBinaryOp(t, and.Left, "LIKE")
		assertColumnRef(t, like.Left, "name")
		assertStringLiteral(t, like.Right, "%foo%")
		gte := assertBinaryOp(t, and.Right, ">=")
		assertColumnRef(t, gte.Left, "val")
		assertFloatLiteral(t, gte.Right, 3.14)
	})
}

func TestParseIsDistinctFrom(t *testing.T) {
	t.Run("is_distinct_from", func(t *testing.T) {
		node, err := Parse("a IS DISTINCT FROM b")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "IS DISTINCT FROM")
		assertColumnRef(t, bo.Left, "a")
		assertColumnRef(t, bo.Right, "b")
	})
}

func TestParseRegex(t *testing.T) {
	t.Run("regex match", func(t *testing.T) {
		node, err := Parse("name ~ '^[A-Z]'")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "~")
		assertColumnRef(t, bo.Left, "name")
		assertStringLiteral(t, bo.Right, "^[A-Z]")
	})

	t.Run("case insensitive regex", func(t *testing.T) {
		node, err := Parse("name ~* '^[a-z]'")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "~*")
		assertColumnRef(t, bo.Left, "name")
		assertStringLiteral(t, bo.Right, "^[a-z]")
	})

	t.Run("negated regex", func(t *testing.T) {
		node, err := Parse("name !~ '^[0-9]'")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "!~")
		assertColumnRef(t, bo.Left, "name")
		assertStringLiteral(t, bo.Right, "^[0-9]")
	})

	t.Run("negated case insensitive regex", func(t *testing.T) {
		node, err := Parse("name !~* '^[0-9]'")
		if err != nil {
			t.Fatal(err)
		}
		bo := assertBinaryOp(t, node, "!~*")
		assertColumnRef(t, bo.Left, "name")
		assertStringLiteral(t, bo.Right, "^[0-9]")
	})

	t.Run("regex combined with AND", func(t *testing.T) {
		node, err := Parse("name ~ '^[A-Z]' AND age > 18")
		if err != nil {
			t.Fatal(err)
		}
		and := assertBinaryOp(t, node, "AND")
		regex := assertBinaryOp(t, and.Left, "~")
		assertColumnRef(t, regex.Left, "name")
		assertStringLiteral(t, regex.Right, "^[A-Z]")
		gt := assertBinaryOp(t, and.Right, ">")
		assertColumnRef(t, gt.Left, "age")
		assertIntLiteral(t, gt.Right, 18)
	})
}
