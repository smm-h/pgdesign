package enc

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// TestKeysKindQualified: a table x and a function x — same schema, same name —
// are DISTINCT manifest keys. Kind-qualification is what prevents the collision.
func TestKeysKindQualified(t *testing.T) {
	testenv.Isolate(t)
	tbl := model.Table{Name: "x", Schema: "public"}
	fn := model.Function{Name: "x", Schema: "public"}

	tk := KeyForTable(tbl)
	fk := KeyForFunction(fn)

	if tk == fk {
		t.Fatalf("table and function keys collided: %+v", tk)
	}
	if tk.String() == fk.String() {
		t.Fatalf("table and function key strings collided: %q", tk.String())
	}
	if tk.String() != "table:public.x" {
		t.Errorf("table key string = %q, want table:public.x", tk.String())
	}
	if fk.String() != "function:public.x()" {
		t.Errorf("function key string = %q, want function:public.x()", fk.String())
	}
}

// TestFunctionOverloadsDistinct: two functions with the same name but different
// argument types are distinct manifest keys (overloads coexist).
func TestFunctionOverloadsDistinct(t *testing.T) {
	testenv.Isolate(t)
	f1 := model.Function{Name: "f", Schema: "public", Args: []model.FunctionArg{
		{Name: "a", Type: typeinfo.Type{Base: "int4"}},
	}}
	f2 := model.Function{Name: "f", Schema: "public", Args: []model.FunctionArg{
		{Name: "a", Type: typeinfo.Type{Base: "text"}},
	}}
	f3 := model.Function{Name: "f", Schema: "public", Args: []model.FunctionArg{
		{Name: "a", Type: typeinfo.Type{Base: "int4"}},
		{Name: "b", Type: typeinfo.Type{Base: "text"}},
	}}

	k1, k2, k3 := KeyForFunction(f1), KeyForFunction(f2), KeyForFunction(f3)
	if k1 == k2 || k1 == k3 || k2 == k3 {
		t.Fatalf("overload keys collided: %q %q %q", k1, k2, k3)
	}
	if k1.String() != "function:public.f(int4)" {
		t.Errorf("k1 = %q", k1.String())
	}
	if k3.String() != "function:public.f(int4,text)" {
		t.Errorf("k3 = %q", k3.String())
	}
}

// TestKeyStringFormsStable pins the textual key forms for the remaining kinds.
func TestKeyStringForms(t *testing.T) {
	testenv.Isolate(t)
	cases := map[string]string{
		Key{Kind: KindView, Schema: "s", Name: "v"}.String():      "view:s.v",
		Key{Kind: KindMatView, Schema: "s", Name: "m"}.String():   "matview:s.m",
		Key{Kind: KindSequence, Schema: "s", Name: "q"}.String():  "sequence:s.q",
		Key{Kind: KindEnum, Schema: "s", Name: "e"}.String():      "enum:s.e",
		Key{Kind: KindDomain, Schema: "s", Name: "d"}.String():    "domain:s.d",
		Key{Kind: KindComposite, Schema: "s", Name: "c"}.String(): "composite:s.c",
		Key{Kind: KindSchemaMeta, Name: "public"}.String():        "schema:public",
		Key{Kind: KindRegistrySnap}.String():                      "registry:",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("key string = %q, want %q", got, want)
		}
	}
}

// TestPseudoTargetGrammar pins the edge_format.md TENSION 2 grammar for DML/raw
// pseudo-targets: byte-stable and identity-load-bearing.
func TestPseudoTargetGrammar(t *testing.T) {
	testenv.Isolate(t)
	cases := map[string]string{
		KeyForDML(0).String(): "dml:0",
		KeyForDML(7).String(): "dml:7",
		KeyForRaw(0).String(): "raw:0",
		KeyForRaw(3).String(): "raw:3",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("pseudo-target = %q, want %q", got, want)
		}
	}
}
