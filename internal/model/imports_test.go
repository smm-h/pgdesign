package model

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/parse"
)

// findFK returns the FK named name on the table named table, or fails.
func findFK(t *testing.T, s *Schema, table, name string) FK {
	t.Helper()
	for _, tbl := range s.Tables {
		if tbl.Name != table {
			continue
		}
		for _, fk := range tbl.FKs {
			if fk.Name == name {
				return fk
			}
		}
	}
	t.Fatalf("FK %q on table %q not found", name, table)
	return FK{}
}

func diagHasCode(diags diagnostic.Diagnostics, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestResolveFK_AliasReference(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "orders",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "user_id", Type: "ref"}},
				FKs: map[string]parse.RawFK{
					"fk_orders_user": {Columns: []string{"user_id"}, RefTable: "framework:users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}
	imports := map[string]string{"framework": "app"}
	schema, diags := Build(raw, reg, WithImports(imports))
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	fk := findFK(t, schema, "orders", "fk_orders_user")
	if fk.RefSchema != "app" {
		t.Errorf("RefSchema = %q, want %q (import target schema)", fk.RefSchema, "app")
	}
	if fk.RefTable != "users" {
		t.Errorf("RefTable = %q, want %q", fk.RefTable, "users")
	}
	if fk.RefAlias != "framework" {
		t.Errorf("RefAlias = %q, want %q (provenance)", fk.RefAlias, "framework")
	}
}

func TestResolveFK_UnknownAlias(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "orders",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "user_id", Type: "ref"}},
				FKs: map[string]parse.RawFK{
					"fk_orders_user": {Columns: []string{"user_id"}, RefTable: "typo:users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}
	_, diags := Build(raw, reg, WithImports(map[string]string{"framework": "app"}))
	if !diagHasCode(diags, "E230") {
		t.Fatalf("expected E230 (unknown alias), got: %v", diags)
	}
}

func TestResolveFK_MalformedAliasQualifiedTarget(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "orders",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "user_id", Type: "ref"}},
				FKs: map[string]parse.RawFK{
					"fk_orders_user": {Columns: []string{"user_id"}, RefTable: "framework:app.users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}
	_, diags := Build(raw, reg, WithImports(map[string]string{"framework": "app"}))
	if !diagHasCode(diags, "E232") {
		t.Fatalf("expected E232 (malformed alias reference), got: %v", diags)
	}
}

func TestResolveFK_DotSplitStillWorks(t *testing.T) {
	// With no ':' the classic schema.table dot-split must be unchanged.
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:    "orders",
				Columns: []parse.RawColumn{{Name: "id", Type: "id"}, {Name: "user_id", Type: "ref"}},
				FKs: map[string]parse.RawFK{
					"fk_orders_user": {Columns: []string{"user_id"}, RefTable: "auth.users", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
			},
		},
	}
	schema, diags := Build(raw, reg, WithImports(map[string]string{"framework": "app"}))
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	fk := findFK(t, schema, "orders", "fk_orders_user")
	if fk.RefSchema != "auth" || fk.RefTable != "users" || fk.RefAlias != "" {
		t.Errorf("dot-split FK mis-resolved: schema=%q table=%q alias=%q", fk.RefSchema, fk.RefTable, fk.RefAlias)
	}
}

func TestAliasScoping_ViewDependsOnRejected(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{Name: "orders", Columns: []parse.RawColumn{{Name: "id", Type: "id"}}},
		},
		Views: []parse.RawView{
			{Name: "v", Query: "SELECT 1", DependsOn: []string{"framework:users"}},
		},
	}
	_, diags := Build(raw, reg, WithImports(map[string]string{"framework": "app"}))
	if !diagHasCode(diags, "E231") {
		t.Fatalf("expected E231 (alias outside FK ref_table), got: %v", diags)
	}
}

func TestAliasScoping_ViewQueryRejected(t *testing.T) {
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{Name: "orders", Columns: []parse.RawColumn{{Name: "id", Type: "id"}}},
		},
		Views: []parse.RawView{
			{Name: "v", Query: "SELECT * FROM framework:users"},
		},
	}
	_, diags := Build(raw, reg, WithImports(map[string]string{"framework": "app"}))
	if !diagHasCode(diags, "E231") {
		t.Fatalf("expected E231 for alias in view query, got: %v", diags)
	}
}

func TestAliasScoping_NoImportsNoPolicing(t *testing.T) {
	// With no declared imports, a colon in a query is not an alias reference.
	reg := testRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{Name: "orders", Columns: []parse.RawColumn{{Name: "id", Type: "id"}}},
		},
		Views: []parse.RawView{
			{Name: "v", Query: "SELECT '12:30'::time"},
		},
	}
	_, diags := Build(raw, reg)
	if diagHasCode(diags, "E231") {
		t.Fatalf("did not expect E231 with no imports declared, got: %v", diags)
	}
}
