package introspect

import (
	"testing"
)

func TestParseFunctionArgs_Simple(t *testing.T) {
	args := parseFunctionArgs("order_id uuid, amount numeric")
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
	if args[0].Name != "order_id" || args[0].Type != "uuid" {
		t.Errorf("arg[0] = %+v, want name=order_id type=uuid", args[0])
	}
	if args[1].Name != "amount" || args[1].Type != "numeric" {
		t.Errorf("arg[1] = %+v, want name=amount type=numeric", args[1])
	}
}

func TestParseFunctionArgs_WithDefault(t *testing.T) {
	args := parseFunctionArgs("tax_rate numeric DEFAULT 0.1")
	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(args))
	}
	if args[0].Name != "tax_rate" || args[0].Type != "numeric" || args[0].Default != "0.1" {
		t.Errorf("arg = %+v, want name=tax_rate type=numeric default=0.1", args[0])
	}
}

func TestParseFunctionArgs_Empty(t *testing.T) {
	args := parseFunctionArgs("")
	if len(args) != 0 {
		t.Errorf("expected 0 args for empty string, got %d", len(args))
	}
}

func TestParseFunctionArgs_ParenthesizedType(t *testing.T) {
	args := parseFunctionArgs("price numeric(10,2), name varchar(255)")
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
	if args[0].Name != "price" || args[0].Type != "numeric(10,2)" {
		t.Errorf("arg[0] = %+v, want name=price type=numeric(10,2)", args[0])
	}
	if args[1].Name != "name" || args[1].Type != "varchar(255)" {
		t.Errorf("arg[1] = %+v, want name=name type=varchar(255)", args[1])
	}
}

func TestParseFunctionArgs_ModePrefix(t *testing.T) {
	args := parseFunctionArgs("IN x integer, OUT y text")
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
	if args[0].Name != "x" || args[0].Type != "integer" {
		t.Errorf("arg[0] = %+v, want name=x type=integer", args[0])
	}
	if args[1].Name != "y" || args[1].Type != "text" {
		t.Errorf("arg[1] = %+v, want name=y type=text", args[1])
	}
}

func TestExtractFunctionBody_DollarQuote(t *testing.T) {
	funcdef := `CREATE OR REPLACE FUNCTION public.calc(amount numeric)
 RETURNS numeric
 LANGUAGE plpgsql
AS $$
BEGIN
  RETURN amount * 0.1;
END;
$$`
	body := extractFunctionBody(funcdef)
	expected := "BEGIN\n  RETURN amount * 0.1;\nEND;"
	if body != expected {
		t.Errorf("body = %q, want %q", body, expected)
	}
}

func TestExtractFunctionBody_NamedTag(t *testing.T) {
	funcdef := `CREATE OR REPLACE FUNCTION public.calc()
 RETURNS void
 LANGUAGE plpgsql
AS $func$
BEGIN
  RAISE NOTICE 'hello';
END;
$func$`
	body := extractFunctionBody(funcdef)
	expected := "BEGIN\n  RAISE NOTICE 'hello';\nEND;"
	if body != expected {
		t.Errorf("body = %q, want %q", body, expected)
	}
}

func TestExtractFunctionBody_Empty(t *testing.T) {
	body := extractFunctionBody("some random text with no dollar quotes")
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}
