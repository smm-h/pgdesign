package sqlexpr

import "testing"

func assertTokens(t *testing.T, input string, expected []token) {
	t.Helper()
	tokens, err := tokenize(input)
	if err != nil {
		t.Fatalf("tokenize(%q) error: %v", input, err)
	}
	// exclude trailing EOF
	got := tokens[:len(tokens)-1]
	if len(got) != len(expected) {
		t.Fatalf("tokenize(%q): expected %d tokens, got %d\ngot: %v", input, len(expected), len(got), got)
	}
	for i, exp := range expected {
		if got[i].kind != exp.kind || got[i].value != exp.value {
			t.Errorf("tokenize(%q) token[%d]: expected {kind:%d value:%q}, got {kind:%d value:%q}",
				input, i, exp.kind, exp.value, got[i].kind, got[i].value)
		}
	}
}

func TestTokenizeComparisons(t *testing.T) {
	t.Run("all comparison operators", func(t *testing.T) {
		assertTokens(t, "<= >= < > <>", []token{
			{kind: tokenLessEqual, value: "<="},
			{kind: tokenGreaterEqual, value: ">="},
			{kind: tokenLess, value: "<"},
			{kind: tokenGreater, value: ">"},
			{kind: tokenNotEquals, value: "<>"},
		})
	})

	t.Run("<> vs <= disambiguation", func(t *testing.T) {
		assertTokens(t, "<>", []token{{kind: tokenNotEquals, value: "<>"}})
		assertTokens(t, "<=", []token{{kind: tokenLessEqual, value: "<="}})
	})

	t.Run("< = with space produces two tokens", func(t *testing.T) {
		assertTokens(t, "< =", []token{
			{kind: tokenLess, value: "<"},
			{kind: tokenEquals, value: "="},
		})
	})

	t.Run("< at end of input", func(t *testing.T) {
		assertTokens(t, "a <", []token{
			{kind: tokenIdent, value: "a"},
			{kind: tokenLess, value: "<"},
		})
	})

	t.Run("> at end of input", func(t *testing.T) {
		assertTokens(t, "a >", []token{
			{kind: tokenIdent, value: "a"},
			{kind: tokenGreater, value: ">"},
		})
	})
}

func TestTokenizeArithmetic(t *testing.T) {
	t.Run("slash and percent", func(t *testing.T) {
		assertTokens(t, "/ %", []token{
			{kind: tokenSlash, value: "/"},
			{kind: tokenPercent, value: "%"},
		})
	})

	t.Run("arithmetic expression", func(t *testing.T) {
		assertTokens(t, "10 / 3 % 2", []token{
			{kind: tokenInt, value: "10"},
			{kind: tokenSlash, value: "/"},
			{kind: tokenInt, value: "3"},
			{kind: tokenPercent, value: "%"},
			{kind: tokenInt, value: "2"},
		})
	})

	t.Run("division with identifiers", func(t *testing.T) {
		assertTokens(t, "price / quantity", []token{
			{kind: tokenIdent, value: "price"},
			{kind: tokenSlash, value: "/"},
			{kind: tokenIdent, value: "quantity"},
		})
	})

	t.Run("modulo with identifier", func(t *testing.T) {
		assertTokens(t, "id % 10", []token{
			{kind: tokenIdent, value: "id"},
			{kind: tokenPercent, value: "%"},
			{kind: tokenInt, value: "10"},
		})
	})
}

func TestTokenizeFloats(t *testing.T) {
	tests := []struct {
		name  string
		input string
		value string
	}{
		{"standard float", "1.0", "1.0"},
		{"trailing dot", "1.", "1."},
		{"leading dot", ".5", ".5"},
		{"pi", "3.14", "3.14"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTokens(t, tt.input, []token{{kind: tokenFloat, value: tt.value}})
		})
	}
}

func TestTokenizeKeywords(t *testing.T) {
	t.Run("SQL keywords as identifiers", func(t *testing.T) {
		assertTokens(t, "IN BETWEEN IS NULL LIKE ILIKE DISTINCT", []token{
			{kind: tokenIdent, value: "IN"},
			{kind: tokenIdent, value: "BETWEEN"},
			{kind: tokenIdent, value: "IS"},
			{kind: tokenIdent, value: "NULL"},
			{kind: tokenIdent, value: "LIKE"},
			{kind: tokenIdent, value: "ILIKE"},
			{kind: tokenIdent, value: "DISTINCT"},
		})
	})

	t.Run("IS NULL expression", func(t *testing.T) {
		assertTokens(t, "x IS NULL", []token{
			{kind: tokenIdent, value: "x"},
			{kind: tokenIdent, value: "IS"},
			{kind: tokenIdent, value: "NULL"},
		})
	})

	t.Run("BETWEEN expression", func(t *testing.T) {
		assertTokens(t, "x BETWEEN 1 AND 10", []token{
			{kind: tokenIdent, value: "x"},
			{kind: tokenIdent, value: "BETWEEN"},
			{kind: tokenInt, value: "1"},
			{kind: tokenIdent, value: "AND"},
			{kind: tokenInt, value: "10"},
		})
	})
}

func TestTokenizeMixed(t *testing.T) {
	t.Run("arithmetic and comparison", func(t *testing.T) {
		assertTokens(t, "a < b + 1", []token{
			{kind: tokenIdent, value: "a"},
			{kind: tokenLess, value: "<"},
			{kind: tokenIdent, value: "b"},
			{kind: tokenPlus, value: "+"},
			{kind: tokenInt, value: "1"},
		})
	})

	t.Run("float in arithmetic", func(t *testing.T) {
		assertTokens(t, "3.14 * r", []token{
			{kind: tokenFloat, value: "3.14"},
			{kind: tokenStar, value: "*"},
			{kind: tokenIdent, value: "r"},
		})
	})
}
