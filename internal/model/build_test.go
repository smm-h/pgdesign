package model

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
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

func TestBuild_MaterializedViewResolution(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	comment := "Monthly order statistics"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		MaterializedViews: []parse.RawMaterializedView{
			{
				Name:    "monthly_stats",
				Query:   "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
				Comment: &comment,
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.MaterializedViews) != 1 {
		t.Fatalf("expected 1 materialized view, got %d", len(schema.MaterializedViews))
	}

	mv := schema.MaterializedViews[0]
	// Name
	if mv.Name != "monthly_stats" {
		t.Errorf("name = %q, want %q", mv.Name, "monthly_stats")
	}
	// Schema
	if mv.Schema != "public" {
		t.Errorf("schema = %q, want %q", mv.Schema, "public")
	}
	// Query
	if mv.Query != "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1" {
		t.Errorf("query = %q, want %q", mv.Query, "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1")
	}
	// Comment (dereferenced from *string)
	if mv.Comment != "Monthly order statistics" {
		t.Errorf("comment = %q, want %q", mv.Comment, "Monthly order statistics")
	}
	// WithData defaults to true when not set
	if mv.WithData != true {
		t.Errorf("with_data = %v, want true", mv.WithData)
	}
	// DependsOn is nil/empty
	if len(mv.DependsOn) != 0 {
		t.Errorf("depends_on = %v, want empty", mv.DependsOn)
	}
	// Indexes is nil/empty
	if len(mv.Indexes) != 0 {
		t.Errorf("indexes = %v, want empty", mv.Indexes)
	}
}

func TestBuild_MaterializedViewWithDataFalse(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	falseVal := false
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		MaterializedViews: []parse.RawMaterializedView{
			{
				Name:     "empty_stats",
				Query:    "SELECT 1",
				WithData: &falseVal,
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.MaterializedViews) != 1 {
		t.Fatalf("expected 1 materialized view, got %d", len(schema.MaterializedViews))
	}

	mv := schema.MaterializedViews[0]
	if mv.WithData != false {
		t.Errorf("with_data = %v, want false", mv.WithData)
	}
}

func TestBuild_GeneratedColumnDefaultStored(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	trueVal := true
	falseVal := false
	genExpr := "price * quantity"

	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "orders",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "price", Type: "money"},
					{Name: "quantity", Type: "counter"},
					{Name: "total_default", Type: "money", Generated: &genExpr},
					{Name: "total_virtual", Type: "money", Generated: &genExpr, Stored: &falseVal},
					{Name: "total_stored", Type: "money", Generated: &genExpr, Stored: &trueVal},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", diags)
	}

	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}

	findCol := func(name string) *Column {
		for i := range schema.Tables[0].Columns {
			if schema.Tables[0].Columns[i].Name == name {
				return &schema.Tables[0].Columns[i]
			}
		}
		return nil
	}

	// stored omitted -> defaults to true
	col := findCol("total_default")
	if col == nil {
		t.Fatal("total_default column not found")
	}
	if col.Generated != "price * quantity" {
		t.Errorf("total_default.Generated = %q, want %q", col.Generated, "price * quantity")
	}
	if !col.Stored {
		t.Error("total_default.Stored = false, want true (default when stored omitted)")
	}

	// stored = false -> stays false
	col = findCol("total_virtual")
	if col == nil {
		t.Fatal("total_virtual column not found")
	}
	if col.Stored {
		t.Error("total_virtual.Stored = true, want false (explicitly set)")
	}

	// stored = true -> stays true
	col = findCol("total_stored")
	if col == nil {
		t.Fatal("total_stored column not found")
	}
	if !col.Stored {
		t.Error("total_stored.Stored = false, want true (explicitly set)")
	}
}

func TestBuild_MaterializedViewIndexes(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	uniqueVal := true
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		MaterializedViews: []parse.RawMaterializedView{
			{
				Name:  "monthly_stats",
				Query: "SELECT date_trunc('month', created_at) AS month, count(*) FROM orders GROUP BY 1",
				Indexes: map[string]parse.RawIndex{
					"idx_month": {
						Columns: []string{"month"},
						Unique:  &uniqueVal,
					},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.MaterializedViews) != 1 {
		t.Fatalf("expected 1 materialized view, got %d", len(schema.MaterializedViews))
	}

	mv := schema.MaterializedViews[0]
	if len(mv.Indexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(mv.Indexes))
	}

	idx := mv.Indexes[0]
	if idx.Name != "idx_month" {
		t.Errorf("index name = %q, want %q", idx.Name, "idx_month")
	}
	if len(idx.Columns) != 1 || idx.Columns[0] != "month" {
		t.Errorf("index columns = %v, want [month]", idx.Columns)
	}
	if idx.Unique != true {
		t.Errorf("index unique = %v, want true", idx.Unique)
	}
}

func TestBuild_DomainResolution(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "profiles",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "handle", Type: "slug"},
					{Name: "contact", Type: "email"},
					{Name: "bio", Type: "short_text"},
					{Name: "name", Type: "short_text"},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}

	tbl := schema.Tables[0]

	// Semantic type CHECKs should NOT be on the table (domains carry them now).
	if len(tbl.Checks) != 0 {
		t.Errorf("expected 0 table CHECK constraints, got %d: %v", len(tbl.Checks), tbl.Checks)
	}

	// Columns should use domain names as PGType.
	findCol := func(name string) *Column {
		for i := range tbl.Columns {
			if tbl.Columns[i].Name == name {
				return &tbl.Columns[i]
			}
		}
		return nil
	}

	if col := findCol("id"); col == nil {
		t.Fatal("id column not found")
	} else if col.PGType != "uuid" {
		t.Errorf("id.PGType = %q, want %q", col.PGType, "uuid")
	}

	if col := findCol("handle"); col == nil {
		t.Fatal("handle column not found")
	} else if col.PGType != "slug" {
		t.Errorf("handle.PGType = %q, want %q", col.PGType, "slug")
	}

	if col := findCol("contact"); col == nil {
		t.Fatal("contact column not found")
	} else if col.PGType != "email" {
		t.Errorf("contact.PGType = %q, want %q", col.PGType, "email")
	}

	if col := findCol("bio"); col == nil {
		t.Fatal("bio column not found")
	} else if col.PGType != "short_text" {
		t.Errorf("bio.PGType = %q, want %q", col.PGType, "short_text")
	}

	if col := findCol("name"); col == nil {
		t.Fatal("name column not found")
	} else if col.PGType != "short_text" {
		t.Errorf("name.PGType = %q, want %q", col.PGType, "short_text")
	}

	// Domains should be built for slug, email, short_text (3 unique types).
	if len(schema.Domains) != 3 {
		t.Fatalf("expected 3 domains, got %d", len(schema.Domains))
	}

	domainsByName := make(map[string]Domain)
	for _, d := range schema.Domains {
		domainsByName[d.Name] = d
	}

	// slug domain.
	if d, ok := domainsByName["slug"]; !ok {
		t.Error("missing domain for slug")
	} else {
		if d.BaseType != "text" {
			t.Errorf("slug domain BaseType = %q, want %q", d.BaseType, "text")
		}
		if d.Check != "VALUE ~ '^[a-z0-9-]+$'" {
			t.Errorf("slug domain Check = %q, want %q", d.Check, "VALUE ~ '^[a-z0-9-]+$'")
		}
		if d.Schema != "public" {
			t.Errorf("slug domain Schema = %q, want %q", d.Schema, "public")
		}
	}

	// email domain.
	if d, ok := domainsByName["email"]; !ok {
		t.Error("missing domain for email")
	} else {
		if d.BaseType != "text" {
			t.Errorf("email domain BaseType = %q, want %q", d.BaseType, "text")
		}
		if d.Check != "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'" {
			t.Errorf("email domain Check = %q, want %q", d.Check, "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'")
		}
	}

	// short_text domain (only one, despite two columns using it).
	if d, ok := domainsByName["short_text"]; !ok {
		t.Error("missing domain for short_text")
	} else {
		if d.BaseType != "text" {
			t.Errorf("short_text domain BaseType = %q, want %q", d.BaseType, "text")
		}
		if d.Check != "LENGTH(VALUE) <= 255" {
			t.Errorf("short_text domain Check = %q, want %q", d.Check, "LENGTH(VALUE) <= 255")
		}
	}

	// Types without checks (id, ref, timestamp, etc.) should NOT produce domains.
	for _, d := range schema.Domains {
		if d.Name == "id" || d.Name == "uuid" {
			t.Errorf("id/uuid type should not produce a domain, got domain %q", d.Name)
		}
	}
}

func TestBuild_DomainResolution_ExplicitChecksPreserved(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "products",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "name", Type: "short_text"},
					{Name: "price", Type: "money"},
				},
				Checks: map[string]parse.RawCheck{
					"ck_products_positive_price": {Expr: "price > 0"},
				},
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	tbl := schema.Tables[0]

	// Explicit check from TOML should be preserved.
	if len(tbl.Checks) != 1 {
		t.Fatalf("expected 1 explicit CHECK constraint, got %d: %v", len(tbl.Checks), tbl.Checks)
	}
	if tbl.Checks[0].Name != "ck_products_positive_price" {
		t.Errorf("check name = %q, want %q", tbl.Checks[0].Name, "ck_products_positive_price")
	}
	if tbl.Checks[0].Expr != "price > 0" {
		t.Errorf("check expr = %q, want %q", tbl.Checks[0].Expr, "price > 0")
	}

	// name column should use short_text domain (has CHECK).
	var nameCol *Column
	for i := range tbl.Columns {
		if tbl.Columns[i].Name == "name" {
			nameCol = &tbl.Columns[i]
			break
		}
	}
	if nameCol == nil {
		t.Fatal("name column not found")
	}
	if nameCol.PGType != "short_text" {
		t.Errorf("name.PGType = %q, want %q", nameCol.PGType, "short_text")
	}

	// Domain should be created for short_text.
	if len(schema.Domains) != 1 {
		t.Fatalf("expected 1 domain (short_text), got %d", len(schema.Domains))
	}
	if schema.Domains[0].Name != "short_text" {
		t.Errorf("domain name = %q, want %q", schema.Domains[0].Name, "short_text")
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestBuild_SequenceResolution(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	trueVal := true
	comment := "Order ID sequence"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Sequences: []parse.RawSequence{
			{
				Name:      "order_seq",
				Start:     int64Ptr(100),
				Increment: int64Ptr(2),
				MinValue:  int64Ptr(1),
				MaxValue:  int64Ptr(999999),
				Cache:     int64Ptr(10),
				Cycle:     &trueVal,
				Comment:   &comment,
			},
		},
	}

	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	if len(schema.Sequences) != 1 {
		t.Fatalf("expected 1 sequence, got %d", len(schema.Sequences))
	}

	seq := schema.Sequences[0]
	if seq.Name != "order_seq" {
		t.Errorf("name = %q, want %q", seq.Name, "order_seq")
	}
	if seq.Schema != "public" {
		t.Errorf("schema = %q, want %q", seq.Schema, "public")
	}
	if seq.Start == nil || *seq.Start != 100 {
		t.Errorf("start = %v, want 100", seq.Start)
	}
	if seq.Increment == nil || *seq.Increment != 2 {
		t.Errorf("increment = %v, want 2", seq.Increment)
	}
	if seq.MinValue == nil || *seq.MinValue != 1 {
		t.Errorf("min_value = %v, want 1", seq.MinValue)
	}
	if seq.MaxValue == nil || *seq.MaxValue != 999999 {
		t.Errorf("max_value = %v, want 999999", seq.MaxValue)
	}
	if seq.Cache == nil || *seq.Cache != 10 {
		t.Errorf("cache = %v, want 10", seq.Cache)
	}
	if !seq.Cycle {
		t.Error("cycle = false, want true")
	}
	if seq.OwnedBy != "" {
		t.Errorf("owned_by = %q, want empty", seq.OwnedBy)
	}
	if seq.Comment != "Order ID sequence" {
		t.Errorf("comment = %q, want %q", seq.Comment, "Order ID sequence")
	}
}

func TestBuild_SequenceOwnedByValid(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	ownedBy := "orders.total"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "orders",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "total", Type: "counter"},
				},
			},
		},
		Sequences: []parse.RawSequence{
			{
				Name:    "order_seq",
				OwnedBy: &ownedBy,
			},
		},
	}

	_, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
}

func TestBuild_SequenceOwnedByInvalidFormat(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	ownedBy := "no_dot"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Sequences: []parse.RawSequence{
			{
				Name:    "bad_seq",
				OwnedBy: &ownedBy,
			},
		},
	}

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected errors for invalid owned_by format")
	}

	var hasE124 bool
	for _, d := range diags {
		if d.Severity == diagnostic.Error && d.Code == "E124" {
			hasE124 = true
			break
		}
	}
	if !hasE124 {
		t.Error("expected E124 error for invalid owned_by format")
	}
}

func TestBuild_SequenceOwnedByUnknownTable(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	ownedBy := "nonexistent.col"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Sequences: []parse.RawSequence{
			{
				Name:    "bad_seq",
				OwnedBy: &ownedBy,
			},
		},
	}

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected errors for unknown table in owned_by")
	}

	var hasE124 bool
	for _, d := range diags {
		if d.Severity == diagnostic.Error && d.Code == "E124" {
			hasE124 = true
			break
		}
	}
	if !hasE124 {
		t.Error("expected E124 error for unknown table in owned_by")
	}
}

func TestBuild_SequenceOwnedByUnknownColumn(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	ownedBy := "users.nonexistent"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "users",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "name", Type: "short_text"},
				},
			},
		},
		Sequences: []parse.RawSequence{
			{
				Name:    "bad_seq",
				OwnedBy: &ownedBy,
			},
		},
	}

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected errors for unknown column in owned_by")
	}

	var hasE124 bool
	for _, d := range diags {
		if d.Severity == diagnostic.Error && d.Code == "E124" {
			hasE124 = true
			break
		}
	}
	if !hasE124 {
		t.Error("expected E124 error for unknown column in owned_by")
	}
}

func TestBuild_SequenceOwnedByIdentityColumn(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	ownedBy := "users.id"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Tables: []parse.RawTable{
			{
				Name: "users",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "auto_id"},
					{Name: "name", Type: "short_text"},
				},
			},
		},
		Sequences: []parse.RawSequence{
			{
				Name:    "bad_seq",
				OwnedBy: &ownedBy,
			},
		},
	}

	_, diags := Build(raw, reg)
	if !diags.HasErrors() {
		t.Fatal("expected errors for identity column in owned_by")
	}

	var hasE124 bool
	for _, d := range diags {
		if d.Severity == diagnostic.Error && d.Code == "E124" {
			hasE124 = true
			break
		}
	}
	if !hasE124 {
		t.Error("expected E124 error for identity column in owned_by")
	}
}

func strPtr(s string) *string { return &s }

func TestBuildFunctions(t *testing.T) {
	lang := "plpgsql"
	returns := "numeric"
	body := "SELECT 1;"
	volatility := "stable"

	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "test", Version: 1},
		Functions: []parse.RawFunction{
			{
				Name:       "get_total",
				Language:   &lang,
				Returns:    &returns,
				Body:       &body,
				Volatility: &volatility,
				Args: []parse.RawFunctionArg{
					{Name: "id", Type: "uuid"},
				},
			},
		},
	}
	reg := semtype.NewBuiltinRegistry()
	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if len(schema.Functions) != 1 {
		t.Fatalf("expected 1 function, got %d", len(schema.Functions))
	}
	f := schema.Functions[0]
	if f.Name != "get_total" {
		t.Errorf("expected name get_total, got %s", f.Name)
	}
	if f.Schema != "test" {
		t.Errorf("expected schema test, got %s", f.Schema)
	}
	if f.Language != "plpgsql" {
		t.Errorf("expected language plpgsql, got %s", f.Language)
	}
	if f.ReturnType != "numeric" {
		t.Errorf("expected return type numeric, got %s", f.ReturnType)
	}
	if f.Volatility != "STABLE" {
		t.Errorf("expected volatility STABLE (uppercased), got %s", f.Volatility)
	}
	if len(f.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(f.Args))
	}
	if f.Args[0].Name != "id" || f.Args[0].Type != "uuid" {
		t.Errorf("expected arg id uuid, got %s %s", f.Args[0].Name, f.Args[0].Type)
	}
}
