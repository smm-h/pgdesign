package model

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
)

func TestJSONSchemaToChecks(t *testing.T) {
	content := []byte(`{
		"type": "object",
		"required": ["title", "price"],
		"properties": {
			"title": {"type": "string"},
			"price": {"type": "number"},
			"tags": {"type": "array"},
			"active": {"type": "boolean"}
		}
	}`)

	checks := jsonSchemaToChecks("metadata", content)

	if len(checks) != 2 {
		t.Fatalf("expected 2 checks (for required fields), got %d", len(checks))
	}

	if checks[0].Name != "ck_metadata_title_type" {
		t.Errorf("check[0].name = %q, want %q", checks[0].Name, "ck_metadata_title_type")
	}
	if checks[0].Expr != "metadata ? 'title' AND jsonb_typeof(metadata->'title') = 'string'" {
		t.Errorf("check[0].expr = %q", checks[0].Expr)
	}

	if checks[1].Name != "ck_metadata_price_type" {
		t.Errorf("check[1].name = %q, want %q", checks[1].Name, "ck_metadata_price_type")
	}
	if checks[1].Expr != "metadata ? 'price' AND jsonb_typeof(metadata->'price') = 'number'" {
		t.Errorf("check[1].expr = %q", checks[1].Expr)
	}
}

func TestJSONSchemaToChecks_RequiredNoType(t *testing.T) {
	content := []byte(`{
		"type": "object",
		"required": ["name"],
		"properties": {}
	}`)

	checks := jsonSchemaToChecks("data", content)

	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Name != "ck_data_name_exists" {
		t.Errorf("name = %q, want %q", checks[0].Name, "ck_data_name_exists")
	}
	if checks[0].Expr != "data ? 'name'" {
		t.Errorf("expr = %q, want %q", checks[0].Expr, "data ? 'name'")
	}
}

func TestJSONSchemaToChecks_IntegerMapsToNumber(t *testing.T) {
	content := []byte(`{
		"type": "object",
		"required": ["count"],
		"properties": {
			"count": {"type": "integer"}
		}
	}`)

	checks := jsonSchemaToChecks("payload", content)

	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Expr != "payload ? 'count' AND jsonb_typeof(payload->'count') = 'number'" {
		t.Errorf("expr = %q; expected integer mapped to number", checks[0].Expr)
	}
}

func TestJSONSchemaToChecks_NoRequired(t *testing.T) {
	content := []byte(`{
		"type": "object",
		"properties": {
			"optional_field": {"type": "string"}
		}
	}`)

	checks := jsonSchemaToChecks("data", content)

	if len(checks) != 0 {
		t.Errorf("expected 0 checks for no required fields, got %d", len(checks))
	}
}

func TestJSONSchemaToChecks_InvalidJSON(t *testing.T) {
	content := []byte(`not json`)
	checks := jsonSchemaToChecks("data", content)
	if checks != nil {
		t.Errorf("expected nil checks for invalid JSON, got %d", len(checks))
	}
}

func TestBuild_ArrayPropagation(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	trueVal := true
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "items",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "tags", Type: "short_text", Array: &trueVal},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	var tagsCol *Column
	for i := range schema.Tables[0].Columns {
		if schema.Tables[0].Columns[i].Name == "tags" {
			tagsCol = &schema.Tables[0].Columns[i]
			break
		}
	}
	if tagsCol == nil {
		t.Fatal("tags column not found")
	}
	if !tagsCol.Array {
		t.Error("expected tags.Array = true after Build, got false")
	}
}

func TestBuild_AppendOnlyPropagation(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	trueVal := true
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name:       "events",
				AppendOnly: &trueVal,
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "data", Type: "short_text"},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if !schema.Tables[0].AppendOnly {
		t.Error("expected AppendOnly = true after Build, got false")
	}
}

func TestBuild_ViewResolution(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	comment := "Active users only"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "users",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "active", Type: "flag"},
				},
			},
		},
		Views: []parse.RawView{
			{
				Name:      "active_users",
				Query:     "SELECT id FROM users WHERE active",
				Comment:   &comment,
				DependsOn: []string{"users"},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(schema.Views))
	}

	v := schema.Views[0]
	if v.Name != "active_users" {
		t.Errorf("view name = %q, want %q", v.Name, "active_users")
	}
	if v.Schema != "public" {
		t.Errorf("view schema = %q, want %q", v.Schema, "public")
	}
	if v.Query != "SELECT id FROM users WHERE active" {
		t.Errorf("view query = %q, want %q", v.Query, "SELECT id FROM users WHERE active")
	}
	if v.Comment != "Active users only" {
		t.Errorf("view comment = %q, want %q", v.Comment, "Active users only")
	}
	if len(v.DependsOn) != 1 || v.DependsOn[0] != "users" {
		t.Errorf("view depends_on = %v, want [users]", v.DependsOn)
	}
}

func TestBuild_JSONSchemaPropagation(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	schemaFile := "schema.json"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "products",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "metadata", Type: "json", JSONSchema: &schemaFile},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	var metaCol *Column
	for i := range schema.Tables[0].Columns {
		if schema.Tables[0].Columns[i].Name == "metadata" {
			metaCol = &schema.Tables[0].Columns[i]
			break
		}
	}
	if metaCol == nil {
		t.Fatal("metadata column not found")
	}
	if metaCol.JSONSchema != "schema.json" {
		t.Errorf("expected JSONSchema = %q, got %q", "schema.json", metaCol.JSONSchema)
	}
}
