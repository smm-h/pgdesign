package splitfmt

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	stmts := []string{
		"CREATE TABLE foo (id integer PRIMARY KEY);",
		"ALTER TABLE foo ADD COLUMN name text NOT NULL;",
		"CREATE INDEX idx_foo_name ON foo (name);",
	}
	data := Encode(stmts)
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != len(stmts) {
		t.Fatalf("got %d statements, want %d", len(got), len(stmts))
	}
	for i := range stmts {
		if got[i] != stmts[i] {
			t.Errorf("statement %d: got %q, want %q", i, got[i], stmts[i])
		}
	}
}

func TestEmpty(t *testing.T) {
	data := Encode(nil)
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d statements, want 0", len(got))
	}
}

func TestSingle(t *testing.T) {
	stmts := []string{"SELECT 1;"}
	data := Encode(stmts)
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != 1 || got[0] != stmts[0] {
		t.Fatalf("got %v, want %v", got, stmts)
	}
}

func TestDollarQuoting(t *testing.T) {
	stmt := `CREATE FUNCTION foo() RETURNS void AS $pgdesign$
BEGIN
  RAISE NOTICE 'hello';
END;
$pgdesign$ LANGUAGE plpgsql;`
	data := Encode([]string{stmt})
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got[0] != stmt {
		t.Errorf("dollar-quoted statement mangled:\ngot:  %q\nwant: %q", got[0], stmt)
	}
}

func TestMultilineStatements(t *testing.T) {
	stmt := "CREATE TABLE bar (\n  id integer PRIMARY KEY,\n  name text NOT NULL\n);"
	data := Encode([]string{stmt})
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got[0] != stmt {
		t.Errorf("multiline statement mangled:\ngot:  %q\nwant: %q", got[0], stmt)
	}
}

func TestUnicode(t *testing.T) {
	stmt := "INSERT INTO t (name) VALUES ('äöüß 世界 \U0001f600');"
	data := Encode([]string{stmt})
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got[0] != stmt {
		t.Errorf("unicode statement mangled:\ngot:  %q\nwant: %q", got[0], stmt)
	}
}

func TestBackslashes(t *testing.T) {
	stmt := `INSERT INTO t (path) VALUES (E'C:\\Users\\foo\\bar');`
	data := Encode([]string{stmt})
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got[0] != stmt {
		t.Errorf("backslash statement mangled:\ngot:  %q\nwant: %q", got[0], stmt)
	}
}

func TestLargeStatement(t *testing.T) {
	// Simulate a realistic large DDL statement (~10KB).
	var b strings.Builder
	b.WriteString("CREATE TABLE big (\n")
	for i := range 200 {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("  col_")
		b.WriteString(strings.Repeat("x", 20))
		b.WriteString("_")
		b.WriteString(strings.Repeat("0", 5))
		b.WriteString(" text NOT NULL")
	}
	b.WriteString("\n);")
	stmt := b.String()

	data := Encode([]string{stmt})
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got[0] != stmt {
		t.Errorf("large statement mangled (len got=%d, want=%d)", len(got[0]), len(stmt))
	}
}

func TestDecodeErrorBadCount(t *testing.T) {
	_, err := Decode([]byte("abc\n"))
	if err == nil {
		t.Fatal("expected error for non-numeric count")
	}
}

func TestDecodeErrorNegativeCount(t *testing.T) {
	_, err := Decode([]byte("-1\n"))
	if err == nil {
		t.Fatal("expected error for negative count")
	}
}

func TestDecodeErrorMissingCountNewline(t *testing.T) {
	_, err := Decode([]byte("2"))
	if err == nil {
		t.Fatal("expected error for missing newline after count")
	}
}

func TestDecodeErrorBadLength(t *testing.T) {
	_, err := Decode([]byte("1\nxyz\nhello\n"))
	if err == nil {
		t.Fatal("expected error for non-numeric length")
	}
}

func TestDecodeErrorNegativeLength(t *testing.T) {
	_, err := Decode([]byte("1\n-5\nhello\n"))
	if err == nil {
		t.Fatal("expected error for negative length")
	}
}

func TestDecodeErrorTruncatedPayload(t *testing.T) {
	_, err := Decode([]byte("1\n100\nshort\n"))
	if err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

func TestDecodeErrorMissingTrailingNewline(t *testing.T) {
	_, err := Decode([]byte("1\n5\nhello"))
	if err == nil {
		t.Fatal("expected error for missing trailing newline")
	}
}

func TestDecodeErrorTruncatedSecondStatement(t *testing.T) {
	// First statement is fine, second is truncated.
	_, err := Decode([]byte("2\n5\nhello\n"))
	if err == nil {
		t.Fatal("expected error for missing second statement")
	}
}

func TestEncodedOutputIsHumanReadable(t *testing.T) {
	stmts := []string{
		"SELECT 1;",
		"SELECT 2;",
	}
	data := Encode(stmts)
	lines := strings.Split(string(data), "\n")

	// Expected structure:
	// "2"           (count)
	// "9"           (length of "SELECT 1;")
	// "SELECT 1;"   (payload)
	// "9"           (length of "SELECT 2;")
	// "SELECT 2;"   (payload)
	// ""            (trailing newline produces empty final element)
	if lines[0] != "2" {
		t.Errorf("line 0: got %q, want %q", lines[0], "2")
	}
	if lines[1] != "9" {
		t.Errorf("line 1: got %q, want %q", lines[1], "9")
	}
	if lines[2] != "SELECT 1;" {
		t.Errorf("line 2: got %q, want %q", lines[2], "SELECT 1;")
	}
	if lines[3] != "9" {
		t.Errorf("line 3: got %q, want %q", lines[3], "9")
	}
	if lines[4] != "SELECT 2;" {
		t.Errorf("line 4: got %q, want %q", lines[4], "SELECT 2;")
	}
}

func TestEmptyStatementContent(t *testing.T) {
	// An empty string is a valid statement (0 bytes).
	stmts := []string{""}
	data := Encode(stmts)
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("got %v, want %v", got, stmts)
	}
}
