package model

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/typeinfo"
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
	} else if got := typeinfo.Reconstruct(col.PGType); got != "uuid" {
		t.Errorf("id.PGType = %q, want %q", got, "uuid")
	}

	if col := findCol("handle"); col == nil {
		t.Fatal("handle column not found")
	} else if got := typeinfo.Reconstruct(col.PGType); got != "slug" {
		t.Errorf("handle.PGType = %q, want %q", got, "slug")
	}

	if col := findCol("contact"); col == nil {
		t.Fatal("contact column not found")
	} else if got := typeinfo.Reconstruct(col.PGType); got != "email" {
		t.Errorf("contact.PGType = %q, want %q", got, "email")
	}

	if col := findCol("bio"); col == nil {
		t.Fatal("bio column not found")
	} else if got := typeinfo.Reconstruct(col.PGType); got != "short_text" {
		t.Errorf("bio.PGType = %q, want %q", got, "short_text")
	}

	if col := findCol("name"); col == nil {
		t.Fatal("name column not found")
	} else if got := typeinfo.Reconstruct(col.PGType); got != "short_text" {
		t.Errorf("name.PGType = %q, want %q", got, "short_text")
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
		if d.BaseType != typeinfo.T("text") {
			t.Errorf("slug domain BaseType = %v, want %v", d.BaseType, typeinfo.T("text"))
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
		if d.BaseType != typeinfo.T("text") {
			t.Errorf("email domain BaseType = %v, want %v", d.BaseType, typeinfo.T("text"))
		}
		if d.Check != "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'" {
			t.Errorf("email domain Check = %q, want %q", d.Check, "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'")
		}
	}

	// short_text domain (only one, despite two columns using it).
	if d, ok := domainsByName["short_text"]; !ok {
		t.Error("missing domain for short_text")
	} else {
		if d.BaseType != typeinfo.T("text") {
			t.Errorf("short_text domain BaseType = %v, want %v", d.BaseType, typeinfo.T("text"))
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
	if got := typeinfo.Reconstruct(nameCol.PGType); got != "short_text" {
		t.Errorf("name.PGType = %q, want %q", got, "short_text")
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
func boolPtr(b bool) *bool    { return &b }

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
	if f.Args[0].Name != "id" || f.Args[0].Type != typeinfo.T("uuid") {
		t.Errorf("expected arg id uuid, got %s %v", f.Args[0].Name, f.Args[0].Type)
	}
}

func TestBuild_FunctionAutoDepends_SQL(t *testing.T) {
	if testing.Short() {
		t.Skip("SQL function dependency detection requires WASM parser")
	}
	lang := "sql"
	returns := "bigint"
	body := "SELECT count(*) FROM users WHERE active = true"

	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Functions: []parse.RawFunction{
			{
				Name:     "count_active_users",
				Language: &lang,
				Returns:  &returns,
				Body:     &body,
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
	if len(f.DependsOn) != 1 {
		t.Fatalf("expected 1 DependsOn entry, got %d: %v", len(f.DependsOn), f.DependsOn)
	}
	if f.DependsOn[0] != "users" {
		t.Errorf("expected DependsOn[0] = %q, got %q", "users", f.DependsOn[0])
	}
}

func TestBuild_FunctionAutoDepends_PLpgSQL(t *testing.T) {
	lang := "plpgsql"
	returns := "void"
	body := "BEGIN INSERT INTO users (name) VALUES ('test'); END;"

	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Functions: []parse.RawFunction{
			{
				Name:     "add_test_user",
				Language: &lang,
				Returns:  &returns,
				Body:     &body,
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
	if len(f.DependsOn) != 0 {
		t.Errorf("expected empty DependsOn for plpgsql function, got %v", f.DependsOn)
	}
}

func TestBuild_FunctionAutoDepends_ExplicitNotOverwritten(t *testing.T) {
	lang := "sql"
	returns := "bigint"
	body := "SELECT count(*) FROM users WHERE active = true"

	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Functions: []parse.RawFunction{
			{
				Name:      "count_active_users",
				Language:  &lang,
				Returns:   &returns,
				Body:      &body,
				DependsOn: []string{"custom_dep"},
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
	if len(f.DependsOn) != 1 || f.DependsOn[0] != "custom_dep" {
		t.Errorf("expected DependsOn = [custom_dep], got %v", f.DependsOn)
	}
}

func TestBuildTriggers(t *testing.T) {
	forEach := "ROW"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "app", Version: 1},
		Tables: []parse.RawTable{
			{
				Name:    "orders",
				Comment: strPtr("Orders table"),
				PK:      []string{"id"},
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
				},
				Triggers: map[string]parse.RawTrigger{
					"audit_insert": {
						Name:     "audit_insert",
						Function: "audit_func",
						Events:   []string{"INSERT", "UPDATE"},
						Timing:   "after",
						ForEach:  &forEach,
					},
				},
			},
		},
	}
	reg := semtype.NewBuiltinRegistry()
	schema, diags := Build(raw, reg)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}
	table := schema.Tables[0]
	if len(table.Triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(table.Triggers))
	}
	trig := table.Triggers[0]
	if trig.Name != "audit_insert" {
		t.Errorf("expected trigger name 'audit_insert', got %q", trig.Name)
	}
	if trig.Function != "audit_func" {
		t.Errorf("expected function 'audit_func', got %q", trig.Function)
	}
	if trig.Timing != "AFTER" {
		t.Errorf("expected timing 'AFTER', got %q", trig.Timing)
	}
	if trig.ForEach != "ROW" {
		t.Errorf("expected for_each 'ROW', got %q", trig.ForEach)
	}
	if len(trig.Events) != 2 {
		t.Errorf("expected 2 events, got %d", len(trig.Events))
	}
}

func TestBuildTriggers_ConstraintMustBeAfter(t *testing.T) {
	constraint := true
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "app", Version: 1},
		Tables: []parse.RawTable{
			{
				Name:    "orders",
				Comment: strPtr("Orders"),
				PK:      []string{"id"},
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
				},
				Triggers: map[string]parse.RawTrigger{
					"bad_constraint": {
						Name:       "bad_constraint",
						Function:   "check_func",
						Events:     []string{"INSERT"},
						Timing:     "BEFORE",
						Constraint: &constraint,
					},
				},
			},
		},
	}
	reg := semtype.NewBuiltinRegistry()
	_, diags := Build(raw, reg)
	found := false
	for _, d := range diags {
		if d.Code == "E126" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected E126 diagnostic for constraint trigger with BEFORE timing")
	}
}

func TestBuild_StateMachineCreatesEnum(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	smType := semtype.UserTypeDef{
		Name: "order_status",
		Kind: "state_machine",
		States: []semtype.UserSMState{
			{Name: "pending"},
			{Name: "confirmed"},
			{Name: "shipped"},
			{Name: "delivered", Terminal: true},
		},
		Transitions: []semtype.UserSMTransition{
			{Name: "confirm", From: []string{"pending"}, To: "confirmed"},
			{Name: "ship", From: []string{"confirmed"}, To: "shipped"},
			{Name: "deliver", From: []string{"shipped"}, To: "delivered"},
		},
		InitialState: "pending",
	}
	diags := reg.LoadUserTypes([]semtype.UserTypeDef{smType})
	if diags.HasErrors() {
		t.Fatalf("failed to load SM type: %v", diags)
	}

	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Types: []parse.RawType{
			{Name: "order_status", Kind: "state_machine"},
		},
		Tables: []parse.RawTable{
			{
				Name: "orders",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "status", Type: "order_status"},
				},
			},
		},
	}

	schema, buildDiags := Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", buildDiags)
	}

	// SM should produce an enum.
	if len(schema.Enums) != 1 {
		t.Fatalf("expected 1 enum, got %d", len(schema.Enums))
	}
	e := schema.Enums[0]
	if e.Name != "order_status" {
		t.Errorf("enum name = %q, want %q", e.Name, "order_status")
	}
	if e.Schema != "public" {
		t.Errorf("enum schema = %q, want %q", e.Schema, "public")
	}
	expected := []string{"pending", "confirmed", "shipped", "delivered"}
	if len(e.Values) != len(expected) {
		t.Fatalf("enum values = %v, want %v", e.Values, expected)
	}
	for i, v := range expected {
		if e.Values[i] != v {
			t.Errorf("enum values[%d] = %q, want %q", i, e.Values[i], v)
		}
	}

	// Column PGType should be the enum name.
	var statusCol *Column
	for i := range schema.Tables[0].Columns {
		if schema.Tables[0].Columns[i].Name == "status" {
			statusCol = &schema.Tables[0].Columns[i]
			break
		}
	}
	if statusCol == nil {
		t.Fatal("status column not found")
	}
	if got := typeinfo.Reconstruct(statusCol.PGType); got != "order_status" {
		t.Errorf("status.PGType = %q, want %q", got, "order_status")
	}
}

func TestBuildMulti_StateMachineEnumDedup(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	smType := semtype.UserTypeDef{
		Name: "ticket_status",
		Kind: "state_machine",
		States: []semtype.UserSMState{
			{Name: "open"},
			{Name: "closed", Terminal: true},
		},
		Transitions: []semtype.UserSMTransition{
			{Name: "close", From: []string{"open"}, To: "closed"},
		},
		InitialState: "open",
	}
	diags := reg.LoadUserTypes([]semtype.UserTypeDef{smType})
	if diags.HasErrors() {
		t.Fatalf("failed to load SM type: %v", diags)
	}

	// Two raw schemas both declaring the same SM type.
	raw1 := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Types: []parse.RawType{
			{Name: "ticket_status", Kind: "state_machine"},
		},
		Tables: []parse.RawTable{
			{
				Name: "tickets",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "status", Type: "ticket_status"},
				},
			},
		},
	}
	raw2 := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Types: []parse.RawType{
			{Name: "ticket_status", Kind: "state_machine"},
		},
		Tables: []parse.RawTable{
			{
				Name: "ticket_comments",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "ticket_status", Type: "ticket_status"},
				},
			},
		},
	}

	schema, buildDiags := BuildMulti([]*parse.RawSchema{raw1, raw2}, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", buildDiags)
	}

	// Should only have one enum after dedup.
	if len(schema.Enums) != 1 {
		t.Fatalf("expected 1 enum (deduped), got %d: %v", len(schema.Enums), schema.Enums)
	}
	if schema.Enums[0].Name != "ticket_status" {
		t.Errorf("enum name = %q, want %q", schema.Enums[0].Name, "ticket_status")
	}
}

func TestIsStateMachineColumn(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	smType := semtype.UserTypeDef{
		Name: "task_state",
		Kind: "state_machine",
		States: []semtype.UserSMState{
			{Name: "todo"},
			{Name: "done", Terminal: true},
		},
		Transitions: []semtype.UserSMTransition{
			{Name: "complete", From: []string{"todo"}, To: "done"},
		},
		InitialState: "todo",
	}
	diags := reg.LoadUserTypes([]semtype.UserTypeDef{smType})
	if diags.HasErrors() {
		t.Fatalf("failed to load SM type: %v", diags)
	}

	smCol := Column{Name: "state", PGType: typeinfo.T("task_state"), SemanticTypeName: "task_state"}
	if !IsStateMachineColumn(smCol, reg) {
		t.Error("expected IsStateMachineColumn = true for SM column")
	}

	regularCol := Column{Name: "name", PGType: typeinfo.T("text"), SemanticTypeName: "short_text"}
	if IsStateMachineColumn(regularCol, reg) {
		t.Error("expected IsStateMachineColumn = false for regular column")
	}

	noTypeCol := Column{Name: "bare", PGType: typeinfo.T("text")}
	if IsStateMachineColumn(noTypeCol, reg) {
		t.Error("expected IsStateMachineColumn = false for column with no semantic type")
	}
}

// --- Integration tests: extends through Build() ---

func TestBuild_ExtendsEnum(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	userTypes := []semtype.UserTypeDef{
		{
			Name:   "base_status",
			Kind:   "enum",
			Values: []string{"a", "b"},
		},
		{
			Name:    "ext_status",
			Extends: "base_status",
			Values:  []string{"c"},
		},
	}
	diags := reg.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("failed to load types: %v", diags)
	}

	extends := "base_status"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Types: []parse.RawType{
			{Name: "base_status", Kind: "enum", Values: []string{"a", "b"}},
			{Name: "ext_status", Extends: &extends, Values: []string{"c"}},
		},
	}

	schema, buildDiags := Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", buildDiags)
	}

	// Should have 2 enums: base_status and ext_status.
	if len(schema.Enums) != 2 {
		t.Fatalf("expected 2 enums, got %d", len(schema.Enums))
	}

	// Find ext_status enum.
	var ext *Enum
	for i := range schema.Enums {
		if schema.Enums[i].Name == "ext_status" {
			ext = &schema.Enums[i]
			break
		}
	}
	if ext == nil {
		t.Fatal("ext_status enum not found")
	}
	// ext_status should have merged values: a, b, c.
	expected := []string{"a", "b", "c"}
	if len(ext.Values) != len(expected) {
		t.Fatalf("ext_status values = %v, want %v", ext.Values, expected)
	}
	for i, v := range expected {
		if ext.Values[i] != v {
			t.Errorf("ext_status values[%d] = %q, want %q", i, ext.Values[i], v)
		}
	}
}

func TestBuild_ExtendsComposite(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	userTypes := []semtype.UserTypeDef{
		{
			Name: "base_comp",
			Kind: "composite",
			Fields: []semtype.CompositeField{
				{Name: "x", PGType: "integer"},
				{Name: "y", PGType: "text"},
			},
		},
		{
			Name:    "ext_comp",
			Extends: "base_comp",
			Fields: []semtype.CompositeField{
				{Name: "z", PGType: "boolean"},
			},
		},
	}
	diags := reg.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("failed to load types: %v", diags)
	}

	extends := "base_comp"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Types: []parse.RawType{
			{Name: "base_comp", Kind: "composite", Fields: []parse.RawCompositeField{{Name: "x", Type: "integer"}, {Name: "y", Type: "text"}}},
			{Name: "ext_comp", Extends: &extends, Fields: []parse.RawCompositeField{{Name: "z", Type: "boolean"}}},
		},
	}

	schema, buildDiags := Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", buildDiags)
	}

	// Should have 2 composite types: base_comp and ext_comp.
	if len(schema.CompositeTypes) != 2 {
		t.Fatalf("expected 2 composite types, got %d", len(schema.CompositeTypes))
	}

	// Find ext_comp.
	var ext *CompositeType
	for i := range schema.CompositeTypes {
		if schema.CompositeTypes[i].Name == "ext_comp" {
			ext = &schema.CompositeTypes[i]
			break
		}
	}
	if ext == nil {
		t.Fatal("ext_comp composite type not found")
	}
	// ext_comp should have merged fields: x (integer), y (text), z (boolean).
	if len(ext.Fields) != 3 {
		t.Fatalf("ext_comp fields count = %d, want 3; fields = %v", len(ext.Fields), ext.Fields)
	}
	expectedFields := []CompositeField{
		{Name: "x", PGType: typeinfo.Parse("integer")},
		{Name: "y", PGType: typeinfo.Parse("text")},
		{Name: "z", PGType: typeinfo.Parse("boolean")},
	}
	for i, f := range expectedFields {
		if ext.Fields[i].Name != f.Name {
			t.Errorf("ext_comp fields[%d].Name = %q, want %q", i, ext.Fields[i].Name, f.Name)
		}
		if ext.Fields[i].PGType != f.PGType {
			t.Errorf("ext_comp fields[%d].PGType = %v, want %v", i, ext.Fields[i].PGType, f.PGType)
		}
	}
}

// TestBuild_CompositeFieldDeclarationOrder verifies that composite type
// fields survive the full parse -> Build pipeline in TOML declaration order.
// Order is semantic: it becomes the PostgreSQL composite field order in
// CREATE TYPE ... AS (...), ROW(...) construction, and tuple comparison.
// Fields are declared deliberately non-alphabetically.
func TestBuild_CompositeFieldDeclarationOrder(t *testing.T) {
	const src = `
[meta]
version = 1
schema = "public"

[types.mailing_address]
kind = "composite"
comment = "Postal address"

[types.mailing_address.fields]
street = "text"
city = "text"
zip = "text"
building = "integer"
apartment = "integer"
`
	raw, parseDiags := parse.Bytes([]byte(src))
	if raw == nil {
		t.Fatalf("parse failed: %v", parseDiags)
	}
	reg := semtype.NewBuiltinRegistry()
	if diags := reg.LoadUserTypes(parse.CollectUserTypes(raw)); diags.HasErrors() {
		t.Fatalf("LoadUserTypes errors: %v", diags)
	}
	schema, buildDiags := Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", buildDiags)
	}
	if len(schema.CompositeTypes) != 1 {
		t.Fatalf("expected 1 composite type, got %d", len(schema.CompositeTypes))
	}
	want := []string{"street", "city", "zip", "building", "apartment"}
	ct := schema.CompositeTypes[0]
	if len(ct.Fields) != len(want) {
		t.Fatalf("fields count = %d, want %d; fields = %v", len(ct.Fields), len(want), ct.Fields)
	}
	for i, name := range want {
		if ct.Fields[i].Name != name {
			t.Errorf("fields[%d].Name = %q, want %q (declaration order lost)", i, ct.Fields[i].Name, name)
		}
	}
}

func TestBuild_ExtendsStateMachine(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	userTypes := []semtype.UserTypeDef{
		{
			Name: "base_sm",
			Kind: "state_machine",
			States: []semtype.UserSMState{
				{Name: "created"},
				{Name: "active"},
			},
			Transitions: []semtype.UserSMTransition{
				{Name: "activate", From: []string{"created"}, To: "active"},
			},
			InitialState:   "created",
			EnforceTrigger: boolPtr(true),
		},
		{
			Name:    "ext_sm",
			Extends: "base_sm",
			States: []semtype.UserSMState{
				{Name: "archived", Terminal: true},
			},
			Transitions: []semtype.UserSMTransition{
				{Name: "archive", From: []string{"active"}, To: "archived"},
			},
		},
	}
	diags := reg.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("failed to load types: %v", diags)
	}

	extends := "base_sm"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Types: []parse.RawType{
			{Name: "base_sm", Kind: "state_machine"},
			{Name: "ext_sm", Extends: &extends},
		},
		Tables: []parse.RawTable{
			{
				Name: "items",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "status", Type: "ext_sm"},
				},
			},
		},
	}

	schema, buildDiags := Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", buildDiags)
	}

	// Should have 2 enums: base_sm and ext_sm (both SMs produce enums).
	if len(schema.Enums) != 2 {
		t.Fatalf("expected 2 enums, got %d: %v", len(schema.Enums), schema.Enums)
	}

	// Find ext_sm enum.
	var ext *Enum
	for i := range schema.Enums {
		if schema.Enums[i].Name == "ext_sm" {
			ext = &schema.Enums[i]
			break
		}
	}
	if ext == nil {
		t.Fatal("ext_sm enum not found")
	}
	// ext_sm should have merged states: created, active, archived.
	expected := []string{"created", "active", "archived"}
	if len(ext.Values) != len(expected) {
		t.Fatalf("ext_sm values = %v, want %v", ext.Values, expected)
	}
	for i, v := range expected {
		if ext.Values[i] != v {
			t.Errorf("ext_sm values[%d] = %q, want %q", i, ext.Values[i], v)
		}
	}
}

func TestBuild_ExtendsEnumDedupValues(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	userTypes := []semtype.UserTypeDef{
		{
			Name:   "base_status",
			Kind:   "enum",
			Values: []string{"a", "b"},
		},
		{
			Name:    "ext_status",
			Extends: "base_status",
			Values:  []string{"b", "c"},
		},
	}
	diags := reg.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("failed to load types: %v", diags)
	}

	extends := "base_status"
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Types: []parse.RawType{
			{Name: "base_status", Kind: "enum", Values: []string{"a", "b"}},
			{Name: "ext_status", Extends: &extends, Values: []string{"b", "c"}},
		},
	}

	schema, buildDiags := Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", buildDiags)
	}

	var ext *Enum
	for i := range schema.Enums {
		if schema.Enums[i].Name == "ext_status" {
			ext = &schema.Enums[i]
			break
		}
	}
	if ext == nil {
		t.Fatal("ext_status enum not found")
	}
	// Deduped: [a, b, c].
	expected := []string{"a", "b", "c"}
	if len(ext.Values) != len(expected) {
		t.Fatalf("ext_status values = %v, want %v", ext.Values, expected)
	}
	for i, v := range expected {
		if ext.Values[i] != v {
			t.Errorf("ext_status values[%d] = %q, want %q", i, ext.Values[i], v)
		}
	}
}

func TestBuildMulti_SourceFile(t *testing.T) {
	tracePath := filepath.Join("..", "codegen", "testdata", "split_trace.toml")
	dispatchPath := filepath.Join("..", "codegen", "testdata", "split_dispatch.toml")

	rawTrace, diags := parse.File(tracePath)
	if len(diags) > 0 {
		for _, d := range diags {
			if d.Severity == diagnostic.Error {
				t.Fatalf("parse error on split_trace.toml: %v", diags)
			}
		}
	}
	if rawTrace == nil {
		t.Fatal("rawTrace is nil")
	}

	rawDispatch, diags := parse.File(dispatchPath)
	if len(diags) > 0 {
		for _, d := range diags {
			if d.Severity == diagnostic.Error {
				t.Fatalf("parse error on split_dispatch.toml: %v", diags)
			}
		}
	}
	if rawDispatch == nil {
		t.Fatal("rawDispatch is nil")
	}

	// Verify parse-level SourceFile is set.
	if !strings.Contains(rawTrace.SourceFile, "split_trace.toml") {
		t.Errorf("rawTrace.SourceFile = %q, want to contain %q", rawTrace.SourceFile, "split_trace.toml")
	}
	if !strings.Contains(rawDispatch.SourceFile, "split_dispatch.toml") {
		t.Errorf("rawDispatch.SourceFile = %q, want to contain %q", rawDispatch.SourceFile, "split_dispatch.toml")
	}

	// Register the trace_status enum type in the registry so columns can resolve.
	reg := semtype.NewBuiltinRegistry()
	loadDiags := reg.LoadUserTypes([]semtype.UserTypeDef{
		{
			Name:   "trace_status",
			Kind:   "enum",
			Values: []string{"pending", "active", "done"},
		},
	})
	if loadDiags.HasErrors() {
		t.Fatalf("failed to load user types: %v", loadDiags)
	}

	schema, buildDiags := BuildMulti([]*parse.RawSchema{rawTrace, rawDispatch}, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", buildDiags)
	}

	// Verify tables from trace have SourceFile containing "split_trace.toml".
	for _, tbl := range schema.Tables {
		switch tbl.Name {
		case "spans", "events":
			if !strings.Contains(tbl.SourceFile, "split_trace.toml") {
				t.Errorf("table %q SourceFile = %q, want to contain %q", tbl.Name, tbl.SourceFile, "split_trace.toml")
			}
		case "tasks":
			if !strings.Contains(tbl.SourceFile, "split_dispatch.toml") {
				t.Errorf("table %q SourceFile = %q, want to contain %q", tbl.Name, tbl.SourceFile, "split_dispatch.toml")
			}
		default:
			t.Errorf("unexpected table %q", tbl.Name)
		}
	}

	// Verify SourceFile survives topo sort (tables are reordered but retain SourceFile).
	// spans must come before events (FK dependency), but both keep their SourceFile.
	foundSpans := false
	foundEvents := false
	for i, tbl := range schema.Tables {
		if tbl.Name == "spans" {
			foundSpans = true
			if tbl.SourceFile == "" {
				t.Errorf("table spans at index %d lost SourceFile after topo sort", i)
			}
		}
		if tbl.Name == "events" {
			foundEvents = true
			if tbl.SourceFile == "" {
				t.Errorf("table events at index %d lost SourceFile after topo sort", i)
			}
		}
	}
	if !foundSpans {
		t.Error("table spans not found in schema.Tables")
	}
	if !foundEvents {
		t.Error("table events not found in schema.Tables")
	}

	// Verify the trace_status Enum has SourceFile containing "split_trace.toml".
	foundEnum := false
	for _, e := range schema.Enums {
		if e.Name == "trace_status" {
			foundEnum = true
			if !strings.Contains(e.SourceFile, "split_trace.toml") {
				t.Errorf("enum trace_status SourceFile = %q, want to contain %q", e.SourceFile, "split_trace.toml")
			}
			break
		}
	}
	if !foundEnum {
		t.Error("enum trace_status not found in schema.Enums")
	}
}

func TestTypeKind_Enum(t *testing.T) {
	reg := semtype.NewBuiltinRegistry()
	userTypes := []semtype.UserTypeDef{
		{
			Name:   "priority",
			Kind:   "enum",
			Values: []string{"low", "medium", "high"},
		},
	}
	diags := reg.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("failed to load user types: %v", diags)
	}

	raw := &parse.RawSchema{
		Meta: parse.RawMeta{Schema: "public"},
		Types: []parse.RawType{
			{Name: "priority", Kind: "enum", Values: []string{"low", "medium", "high"}},
		},
		Tables: []parse.RawTable{
			{
				Name: "tasks",
				Columns: []parse.RawColumn{
					{Name: "id", Type: "id"},
					{Name: "priority", Type: "priority"},
					{Name: "title", Type: "short_text"},
				},
			},
		},
	}

	schema, buildDiags := Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("unexpected build errors: %v", buildDiags)
	}

	var priorityCol *Column
	var titleCol *Column
	for i := range schema.Tables[0].Columns {
		switch schema.Tables[0].Columns[i].Name {
		case "priority":
			priorityCol = &schema.Tables[0].Columns[i]
		case "title":
			titleCol = &schema.Tables[0].Columns[i]
		}
	}

	if priorityCol == nil {
		t.Fatal("priority column not found")
	}
	if priorityCol.TypeKind != "enum" {
		t.Errorf("priority.TypeKind = %q, want %q", priorityCol.TypeKind, "enum")
	}

	if titleCol == nil {
		t.Fatal("title column not found")
	}
	// Builtin types should have TypeKind = "scalar" (the default Kind).
	if titleCol.TypeKind != "scalar" {
		t.Errorf("title.TypeKind = %q, want %q", titleCol.TypeKind, "scalar")
	}
}
