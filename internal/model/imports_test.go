package model

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/typeinfo"
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

// importedUsersTable is the reference table a framework provides, already stamped
// into its target schema "app" (how ExtractSurface leaves it).
func importedUsersTable() Table {
	return Table{
		Name:   "users",
		Schema: "app",
		Columns: []Column{
			{Name: "id", PGType: typeinfo.MustParse("uuid"), NotNull: true},
			{Name: "email", PGType: typeinfo.MustParse("text"), NotNull: true},
		},
		PK: []string{"id"},
	}
}

func TestImportedTables_TableByNameResolves(t *testing.T) {
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
	schema, diags := Build(raw, reg,
		WithImports(map[string]string{"framework": "app"}),
		WithImportedTables([]Table{importedUsersTable()}),
	)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	// The imported table must be present in ImportedTables, NOT in Tables.
	if len(schema.Tables) != 1 {
		t.Fatalf("Tables should contain only the owned table, got %d", len(schema.Tables))
	}
	if len(schema.ImportedTables) != 1 {
		t.Fatalf("ImportedTables should carry the reference table, got %d", len(schema.ImportedTables))
	}
	// TableByName must resolve the imported FK target through the union.
	if got := schema.TableByName("app", "users"); got == nil {
		t.Fatalf("TableByName(app, users) = nil; imported target did not resolve through the union")
	}
}

func TestImportedTables_FKGraphEdgeFlaggedAndKeyed(t *testing.T) {
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
	schema, diags := Build(raw, reg,
		WithImports(map[string]string{"framework": "app"}),
		WithImportedTables([]Table{importedUsersTable()}),
	)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	toKey := TableKey("app", "users")
	edges := schema.FKGraph.Reverse[toKey]
	if len(edges) != 1 {
		t.Fatalf("imported target node not keyed in Reverse graph; got %d edges", len(edges))
	}
	if !edges[0].Imported {
		t.Errorf("edge to imported table should have Imported=true, got %+v", edges[0])
	}
	// The projection must carry the flag and the node.
	proj := schema.FKGraph.Project()
	foundNode := false
	for _, n := range proj.Nodes {
		if n.Schema == "app" && n.Name == "users" {
			foundNode = true
		}
	}
	if !foundNode {
		t.Errorf("projection missing imported node app.users: %+v", proj.Nodes)
	}
}

func TestImportedTables_OwnedShadowsImported(t *testing.T) {
	// A local table with the same (schema,name) as an imported reference wins in
	// the lookup map — the project generates the local one.
	s := &Schema{
		Tables:         []Table{{Name: "users", Schema: "app", Comment: "local"}},
		ImportedTables: []Table{{Name: "users", Schema: "app", Comment: "imported"}},
	}
	s.buildTablesByName()
	got := s.TableByName("app", "users")
	if got == nil || got.Comment != "local" {
		t.Fatalf("owned table should shadow imported; got %+v", got)
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
