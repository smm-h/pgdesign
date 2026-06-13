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
