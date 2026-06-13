package model

import (
	"testing"
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
