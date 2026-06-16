package sqlparse

import (
	"strings"
	"testing"
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
