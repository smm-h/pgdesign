package sqlparse

import (
	"strings"
	"testing"

	pg "github.com/pganalyze/pg_query_go/v6"
)

func TestSplitStatements(t *testing.T) {
	t.Run("single statement", func(t *testing.T) {
		stmts, err := SplitStatements("SELECT 1;")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stmts) != 1 {
			t.Fatalf("expected 1 statement, got %d: %v", len(stmts), stmts)
		}
		if stmts[0] != "SELECT 1;" {
			t.Errorf("expected %q, got %q", "SELECT 1;", stmts[0])
		}
	})

	t.Run("multiple statements", func(t *testing.T) {
		stmts, err := SplitStatements("SELECT 1;\nSELECT 2;")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stmts) != 2 {
			t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
		}
		if stmts[0] != "SELECT 1;" {
			t.Errorf("stmt[0]: expected %q, got %q", "SELECT 1;", stmts[0])
		}
		if stmts[1] != "SELECT 2;" {
			t.Errorf("stmt[1]: expected %q, got %q", "SELECT 2;", stmts[1])
		}
	})

	t.Run("dollar-quoted function body", func(t *testing.T) {
		input := `CREATE FUNCTION test() RETURNS void AS $$
BEGIN
  INSERT INTO t VALUES (1);
  INSERT INTO t VALUES (2);
END;
$$ LANGUAGE plpgsql;`
		stmts, err := SplitStatements(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stmts) != 1 {
			t.Fatalf("expected 1 statement, got %d: %v", len(stmts), stmts)
		}
		if !strings.HasPrefix(stmts[0], "CREATE FUNCTION") {
			t.Errorf("expected statement to start with CREATE FUNCTION, got %q", stmts[0])
		}
		if !strings.HasSuffix(stmts[0], ";") {
			t.Errorf("expected statement to end with semicolon, got %q", stmts[0])
		}
	})

	t.Run("string literal with semicolons", func(t *testing.T) {
		stmts, err := SplitStatements("SELECT 'hello; world';")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stmts) != 1 {
			t.Fatalf("expected 1 statement, got %d: %v", len(stmts), stmts)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		stmts, err := SplitStatements("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stmts != nil {
			t.Errorf("expected nil, got %v", stmts)
		}
	})

	t.Run("whitespace only", func(t *testing.T) {
		stmts, err := SplitStatements("   \n\t  ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stmts != nil {
			t.Errorf("expected nil, got %v", stmts)
		}
	})

	t.Run("trailing semicolons and whitespace", func(t *testing.T) {
		stmts, err := SplitStatements("SELECT 1;\n\n")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stmts) != 1 {
			t.Fatalf("expected 1 statement, got %d: %v", len(stmts), stmts)
		}
		if stmts[0] != "SELECT 1;" {
			t.Errorf("expected %q, got %q", "SELECT 1;", stmts[0])
		}
	})

	t.Run("multiple statements with varied whitespace", func(t *testing.T) {
		input := "CREATE TABLE t (id int);\n\nALTER TABLE t ADD COLUMN name text;"
		stmts, err := SplitStatements(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stmts) != 2 {
			t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
		}
		if stmts[0] != "CREATE TABLE t (id int);" {
			t.Errorf("stmt[0]: expected %q, got %q", "CREATE TABLE t (id int);", stmts[0])
		}
		if stmts[1] != "ALTER TABLE t ADD COLUMN name text;" {
			t.Errorf("stmt[1]: expected %q, got %q", "ALTER TABLE t ADD COLUMN name text;", stmts[1])
		}
	})
}

func TestDeparseExpr(t *testing.T) {
	t.Run("simple column reference", func(t *testing.T) {
		node := &pg.Node{Node: &pg.Node_ColumnRef{ColumnRef: &pg.ColumnRef{
			Fields: []*pg.Node{{Node: &pg.Node_String_{String_: &pg.String{Sval: "name"}}}},
		}}}
		got, err := DeparseExpr(node)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "name" {
			t.Errorf("expected %q, got %q", "name", got)
		}
	})

	t.Run("function call with arguments", func(t *testing.T) {
		node := &pg.Node{Node: &pg.Node_FuncCall{FuncCall: &pg.FuncCall{
			Funcname: []*pg.Node{{Node: &pg.Node_String_{String_: &pg.String{Sval: "lower"}}}},
			Args:     []*pg.Node{{Node: &pg.Node_AConst{AConst: &pg.A_Const{Val: &pg.A_Const_Sval{Sval: &pg.String{Sval: "HELLO"}}}}}},
		}}}
		got, err := DeparseExpr(node)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "lower('HELLO')" {
			t.Errorf("expected %q, got %q", "lower('HELLO')", got)
		}
	})

	t.Run("binary operation", func(t *testing.T) {
		node := &pg.Node{Node: &pg.Node_AExpr{AExpr: &pg.A_Expr{
			Kind: pg.A_Expr_Kind_AEXPR_OP,
			Name: []*pg.Node{{Node: &pg.Node_String_{String_: &pg.String{Sval: "+"}}}},
			Lexpr: &pg.Node{Node: &pg.Node_ColumnRef{ColumnRef: &pg.ColumnRef{
				Fields: []*pg.Node{{Node: &pg.Node_String_{String_: &pg.String{Sval: "a"}}}},
			}}},
			Rexpr: &pg.Node{Node: &pg.Node_ColumnRef{ColumnRef: &pg.ColumnRef{
				Fields: []*pg.Node{{Node: &pg.Node_String_{String_: &pg.String{Sval: "b"}}}},
			}}},
		}}}
		got, err := DeparseExpr(node)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// pg_query may wrap binary expressions in parentheses
		if got != "a + b" && got != "(a + b)" {
			t.Errorf("expected %q or %q, got %q", "a + b", "(a + b)", got)
		}
	})

	t.Run("nil node", func(t *testing.T) {
		_, err := DeparseExpr(nil)
		if err == nil {
			t.Fatal("expected error for nil node, got nil")
		}
	})
}

func TestExtractTableRefs(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		want    []string
		wantErr bool
	}{
		{
			name: "simple SELECT",
			sql:  "SELECT * FROM users",
			want: []string{"users"},
		},
		{
			name: "JOIN",
			sql:  "SELECT * FROM users u JOIN orders o ON o.user_id = u.id",
			want: []string{"orders", "users"},
		},
		{
			name: "subquery",
			sql:  "SELECT * FROM users WHERE id IN (SELECT user_id FROM orders)",
			want: []string{"orders", "users"},
		},
		{
			name: "schema-qualified",
			sql:  "SELECT * FROM auth.users",
			want: []string{"auth.users"},
		},
		{
			name: "INSERT",
			sql:  "INSERT INTO users (name) VALUES ('test')",
			want: []string{"users"},
		},
		{
			name: "UPDATE",
			sql:  "UPDATE users SET name = 'test' WHERE id = 1",
			want: []string{"users"},
		},
		{
			name: "DELETE",
			sql:  "DELETE FROM users WHERE id = 1",
			want: []string{"users"},
		},
		{
			name:    "PL/pgSQL body",
			sql:     "BEGIN\n  INSERT INTO t VALUES (1);\nEND;",
			wantErr: true,
		},
		{
			name: "empty input",
			sql:  "",
			want: nil,
		},
		{
			name: "multiple references deduplicated",
			sql:  "SELECT * FROM users u1 JOIN users u2 ON u1.id = u2.id",
			want: []string{"users"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractTableRefs(tt.sql)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d refs %v, got %d refs %v", len(tt.want), tt.want, len(got), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("ref[%d]: expected %q, got %q", i, tt.want[i], got[i])
				}
			}
		})
	}
}

func TestExtractTableRefsEdgeCases(t *testing.T) {
	// Verify the function compiles and handles whitespace-only input.
	refs, err := ExtractTableRefs("   \t\n  ")
	if err != nil {
		t.Fatalf("unexpected error for whitespace input: %v", err)
	}
	if refs != nil {
		t.Errorf("expected nil for whitespace input, got %v", refs)
	}

}
