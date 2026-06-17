package workload

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

// findByCode filters diagnostics to those matching the given code.
func findByCode(diags []diagnostic.Diagnostic, code string) []diagnostic.Diagnostic {
	var found []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == code {
			found = append(found, d)
		}
	}
	return found
}

// ---------------------------------------------------------------------------
// StructuralRecommendations
// ---------------------------------------------------------------------------

func TestStructural_W022_JsonbWithoutGIN(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "documents",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "data", PGType: "jsonb"},
			},
			PK: []string{"id"},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "W022")
	if len(found) != 1 {
		t.Fatalf("expected 1 W022, got %d", len(found))
	}
	if found[0].Table != "documents" {
		t.Errorf("expected table documents, got %s", found[0].Table)
	}
	if found[0].Column != "data" {
		t.Errorf("expected column data, got %s", found[0].Column)
	}
	if found[0].Severity != diagnostic.Warning {
		t.Errorf("expected Warning severity, got %d", found[0].Severity)
	}
}

func TestStructural_W022_JsonbWithGIN(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "documents",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "data", PGType: "jsonb"},
			},
			PK: []string{"id"},
			Indexes: []model.Index{{
				Name:    "idx_documents_data",
				Columns: []string{"data"},
				Method:  "gin",
			}},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "W022")
	if len(found) != 0 {
		t.Fatalf("expected no W022 when GIN index present, got %d", len(found))
	}
}

func TestStructural_W022_JsonbArrayNotTriggered(t *testing.T) {
	// A jsonb[] column should trigger W023 (array), not W022 (jsonb).
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "documents",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "data", PGType: "jsonb", Array: true},
			},
			PK: []string{"id"},
		}},
	}
	diags := StructuralRecommendations(schema)
	w022 := findByCode(diags, "W022")
	w023 := findByCode(diags, "W023")
	if len(w022) != 0 {
		t.Errorf("jsonb array column should not trigger W022, got %d", len(w022))
	}
	if len(w023) != 1 {
		t.Errorf("jsonb array column should trigger W023, got %d", len(w023))
	}
}

func TestStructural_W023_ArrayWithoutGIN(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "posts",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "tags", PGType: "text", Array: true},
			},
			PK: []string{"id"},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "W023")
	if len(found) != 1 {
		t.Fatalf("expected 1 W023, got %d", len(found))
	}
	if found[0].Column != "tags" {
		t.Errorf("expected column tags, got %s", found[0].Column)
	}
}

func TestStructural_W023_ArrayWithGIN(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "posts",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "tags", PGType: "text", Array: true},
			},
			PK: []string{"id"},
			Indexes: []model.Index{{
				Name:    "idx_posts_tags",
				Columns: []string{"tags"},
				Method:  "gin",
			}},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "W023")
	if len(found) != 0 {
		t.Fatalf("expected no W023 when GIN index present, got %d", len(found))
	}
}

func TestStructural_W024_TsvectorWithoutGIN(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "articles",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "search_vec", PGType: "tsvector"},
			},
			PK: []string{"id"},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "W024")
	if len(found) != 1 {
		t.Fatalf("expected 1 W024, got %d", len(found))
	}
	if found[0].Column != "search_vec" {
		t.Errorf("expected column search_vec, got %s", found[0].Column)
	}
}

func TestStructural_W024_TsvectorWithGIN(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "articles",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "search_vec", PGType: "tsvector"},
			},
			PK: []string{"id"},
			Indexes: []model.Index{{
				Name:    "idx_articles_search",
				Columns: []string{"search_vec"},
				Method:  "GIN",
			}},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "W024")
	if len(found) != 0 {
		t.Fatalf("expected no W024 when GIN index present, got %d", len(found))
	}
}

func TestStructural_I005_TimestampAppendOnlyWithoutBRIN(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:       "events",
			AppendOnly: true,
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "created_at", PGType: "timestamptz"},
			},
			PK: []string{"id"},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "I005")
	if len(found) != 1 {
		t.Fatalf("expected 1 I005, got %d", len(found))
	}
	if found[0].Severity != diagnostic.Info {
		t.Errorf("expected Info severity, got %d", found[0].Severity)
	}
	if found[0].Column != "created_at" {
		t.Errorf("expected column created_at, got %s", found[0].Column)
	}
}

func TestStructural_I005_TimestampWithBRIN(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:       "events",
			AppendOnly: true,
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "created_at", PGType: "timestamptz"},
			},
			PK: []string{"id"},
			Indexes: []model.Index{{
				Name:    "idx_events_created",
				Columns: []string{"created_at"},
				Method:  "brin",
			}},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "I005")
	if len(found) != 0 {
		t.Fatalf("expected no I005 when BRIN index present, got %d", len(found))
	}
}

func TestStructural_I005_NonAppendOnlyNoTrigger(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:       "events",
			AppendOnly: false,
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "created_at", PGType: "timestamptz"},
			},
			PK: []string{"id"},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "I005")
	if len(found) != 0 {
		t.Fatalf("expected no I005 for non-append-only table, got %d", len(found))
	}
}

func TestStructural_I005_PlainTimestamp(t *testing.T) {
	// "timestamp" (without tz) should also trigger I005 on append-only tables.
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:       "logs",
			AppendOnly: true,
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "logged_at", PGType: "timestamp"},
			},
			PK: []string{"id"},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "I005")
	if len(found) != 1 {
		t.Fatalf("expected 1 I005 for plain timestamp, got %d", len(found))
	}
}

func TestStructural_MultipleColumnsMultipleDiags(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "mixed",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "payload", PGType: "jsonb"},
				{Name: "tags", PGType: "text", Array: true},
				{Name: "search", PGType: "tsvector"},
			},
			PK: []string{"id"},
		}},
	}
	diags := StructuralRecommendations(schema)
	if len(findByCode(diags, "W022")) != 1 {
		t.Error("expected 1 W022 for jsonb column")
	}
	if len(findByCode(diags, "W023")) != 1 {
		t.Error("expected 1 W023 for array column")
	}
	if len(findByCode(diags, "W024")) != 1 {
		t.Error("expected 1 W024 for tsvector column")
	}
}

func TestStructural_EmptySchema(t *testing.T) {
	schema := &model.Schema{}
	diags := StructuralRecommendations(schema)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for empty schema, got %d", len(diags))
	}
}

func TestStructural_GINMethodCaseInsensitive(t *testing.T) {
	// The columnHasIndexMethod check uses EqualFold, so "GIN" should match.
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "documents",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "data", PGType: "jsonb"},
			},
			PK: []string{"id"},
			Indexes: []model.Index{{
				Name:    "idx_documents_data",
				Columns: []string{"data"},
				Method:  "GIN",
			}},
		}},
	}
	diags := StructuralRecommendations(schema)
	found := findByCode(diags, "W022")
	if len(found) != 0 {
		t.Fatalf("expected GIN (uppercase) to suppress W022, got %d", len(found))
	}
}

// ---------------------------------------------------------------------------
// DetectNPlusOne
// ---------------------------------------------------------------------------

func TestNPlusOne_Detected(t *testing.T) {
	fkGraph := &model.FKGraph{
		Forward: map[string][]model.FKEdge{
			"order_items": {{
				FromTable:  "order_items",
				FromColumn: "order_id",
				ToTable:    "orders",
				ToColumn:   "id",
				OnDelete:   "CASCADE",
			}},
		},
		Reverse: map[string][]model.FKEdge{
			"orders": {{
				FromTable:  "order_items",
				FromColumn: "order_id",
				ToTable:    "orders",
				ToColumn:   "id",
				OnDelete:   "CASCADE",
			}},
		},
	}
	stats := []StatementStats{
		{Query: "SELECT * FROM order_items WHERE order_id = $1", Calls: 50000, Tables: []string{"order_items"}},
		{Query: "SELECT * FROM orders", Calls: 100, Tables: []string{"orders"}},
	}
	diags := DetectNPlusOne(fkGraph, stats)
	found := findByCode(diags, "W025")
	if len(found) != 1 {
		t.Fatalf("expected 1 W025, got %d", len(found))
	}
	if found[0].Table != "order_items" {
		t.Errorf("expected table order_items, got %s", found[0].Table)
	}
	if found[0].Severity != diagnostic.Warning {
		t.Errorf("expected Warning severity, got %d", found[0].Severity)
	}
}

func TestNPlusOne_RatioBelowThreshold(t *testing.T) {
	fkGraph := &model.FKGraph{
		Forward: map[string][]model.FKEdge{
			"order_items": {{
				FromTable:  "order_items",
				FromColumn: "order_id",
				ToTable:    "orders",
				ToColumn:   "id",
				OnDelete:   "CASCADE",
			}},
		},
	}
	stats := []StatementStats{
		{Query: "SELECT * FROM order_items WHERE order_id = $1", Calls: 500, Tables: []string{"order_items"}},
		{Query: "SELECT * FROM orders", Calls: 100, Tables: []string{"orders"}},
	}
	diags := DetectNPlusOne(fkGraph, stats)
	found := findByCode(diags, "W025")
	if len(found) != 0 {
		t.Fatalf("expected no W025 for ratio 5:1 (below threshold), got %d", len(found))
	}
}

func TestNPlusOne_ParentCallsBelowMinimum(t *testing.T) {
	fkGraph := &model.FKGraph{
		Forward: map[string][]model.FKEdge{
			"order_items": {{
				FromTable:  "order_items",
				FromColumn: "order_id",
				ToTable:    "orders",
				ToColumn:   "id",
				OnDelete:   "CASCADE",
			}},
		},
	}
	stats := []StatementStats{
		{Query: "SELECT * FROM order_items WHERE order_id = $1", Calls: 5000, Tables: []string{"order_items"}},
		{Query: "SELECT * FROM orders", Calls: 10, Tables: []string{"orders"}},
	}
	diags := DetectNPlusOne(fkGraph, stats)
	found := findByCode(diags, "W025")
	if len(found) != 0 {
		t.Fatalf("expected no W025 when parent calls below MinSignificantCalls, got %d", len(found))
	}
}

func TestNPlusOne_ChildCallsBelowMinimum(t *testing.T) {
	fkGraph := &model.FKGraph{
		Forward: map[string][]model.FKEdge{
			"order_items": {{
				FromTable:  "order_items",
				FromColumn: "order_id",
				ToTable:    "orders",
				ToColumn:   "id",
				OnDelete:   "CASCADE",
			}},
		},
	}
	stats := []StatementStats{
		{Query: "SELECT * FROM order_items WHERE order_id = $1", Calls: 50, Tables: []string{"order_items"}},
		{Query: "SELECT * FROM orders", Calls: 100, Tables: []string{"orders"}},
	}
	diags := DetectNPlusOne(fkGraph, stats)
	found := findByCode(diags, "W025")
	if len(found) != 0 {
		t.Fatalf("expected no W025 when child calls below MinSignificantCalls, got %d", len(found))
	}
}

func TestNPlusOne_ExactThreshold(t *testing.T) {
	fkGraph := &model.FKGraph{
		Forward: map[string][]model.FKEdge{
			"order_items": {{
				FromTable:  "order_items",
				FromColumn: "order_id",
				ToTable:    "orders",
				ToColumn:   "id",
				OnDelete:   "CASCADE",
			}},
		},
	}
	stats := []StatementStats{
		{Query: "SELECT * FROM order_items", Calls: 10000, Tables: []string{"order_items"}},
		{Query: "SELECT * FROM orders", Calls: 100, Tables: []string{"orders"}},
	}
	diags := DetectNPlusOne(fkGraph, stats)
	found := findByCode(diags, "W025")
	// 10000/100 = 100, exactly at threshold (>= 100)
	if len(found) != 1 {
		t.Fatalf("expected 1 W025 at exact threshold (100:1), got %d", len(found))
	}
}

func TestNPlusOne_EmptyStats(t *testing.T) {
	fkGraph := &model.FKGraph{
		Forward: map[string][]model.FKEdge{
			"order_items": {{
				FromTable:  "order_items",
				FromColumn: "order_id",
				ToTable:    "orders",
				ToColumn:   "id",
				OnDelete:   "CASCADE",
			}},
		},
	}
	diags := DetectNPlusOne(fkGraph, nil)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics with empty stats, got %d", len(diags))
	}
}

func TestNPlusOne_EmptyGraph(t *testing.T) {
	fkGraph := &model.FKGraph{
		Forward: map[string][]model.FKEdge{},
	}
	stats := []StatementStats{
		{Query: "SELECT * FROM order_items", Calls: 50000, Tables: []string{"order_items"}},
		{Query: "SELECT * FROM orders", Calls: 100, Tables: []string{"orders"}},
	}
	diags := DetectNPlusOne(fkGraph, stats)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics with empty FK graph, got %d", len(diags))
	}
}

func TestNPlusOne_DedupPerPair(t *testing.T) {
	// Multiple FK edges between same table pair should produce only one W025.
	fkGraph := &model.FKGraph{
		Forward: map[string][]model.FKEdge{
			"order_items": {
				{FromTable: "order_items", FromColumn: "order_id", ToTable: "orders", ToColumn: "id"},
				{FromTable: "order_items", FromColumn: "created_by_order", ToTable: "orders", ToColumn: "id"},
			},
		},
	}
	stats := []StatementStats{
		{Query: "SELECT * FROM order_items", Calls: 50000, Tables: []string{"order_items"}},
		{Query: "SELECT * FROM orders", Calls: 100, Tables: []string{"orders"}},
	}
	diags := DetectNPlusOne(fkGraph, stats)
	found := findByCode(diags, "W025")
	if len(found) != 1 {
		t.Fatalf("expected 1 W025 (dedup per pair), got %d", len(found))
	}
}

// ---------------------------------------------------------------------------
// DetectSeqScanHeavy
// ---------------------------------------------------------------------------

func TestSeqScanHeavy_Detected(t *testing.T) {
	stats := []TableScanStats{{Schema: "public", Table: "users", SeqScan: 10000, IdxScan: 10}}
	diags := DetectSeqScanHeavy(stats)
	found := findByCode(diags, "W026")
	if len(found) != 1 {
		t.Fatalf("expected 1 W026, got %d", len(found))
	}
	if found[0].Table != "users" {
		t.Errorf("expected table users, got %s", found[0].Table)
	}
	if found[0].Severity != diagnostic.Warning {
		t.Errorf("expected Warning severity, got %d", found[0].Severity)
	}
}

func TestSeqScanHeavy_BalancedScans(t *testing.T) {
	stats := []TableScanStats{{Schema: "public", Table: "users", SeqScan: 100, IdxScan: 100}}
	diags := DetectSeqScanHeavy(stats)
	found := findByCode(diags, "W026")
	if len(found) != 0 {
		t.Fatalf("expected no W026 for balanced scans, got %d", len(found))
	}
}

func TestSeqScanHeavy_ZeroSeqScan(t *testing.T) {
	stats := []TableScanStats{{Schema: "public", Table: "users", SeqScan: 0, IdxScan: 100}}
	diags := DetectSeqScanHeavy(stats)
	found := findByCode(diags, "W026")
	if len(found) != 0 {
		t.Fatalf("expected no W026 for zero seq scans, got %d", len(found))
	}
}

func TestSeqScanHeavy_ZeroBothScans(t *testing.T) {
	stats := []TableScanStats{{Schema: "public", Table: "users", SeqScan: 0, IdxScan: 0}}
	diags := DetectSeqScanHeavy(stats)
	found := findByCode(diags, "W026")
	if len(found) != 0 {
		t.Fatalf("expected no W026 for zero scans, got %d", len(found))
	}
}

func TestSeqScanHeavy_BoundaryExactly10x(t *testing.T) {
	// SeqScan must be strictly greater than 10*IdxScan.
	stats := []TableScanStats{{Schema: "public", Table: "users", SeqScan: 100, IdxScan: 10}}
	diags := DetectSeqScanHeavy(stats)
	found := findByCode(diags, "W026")
	if len(found) != 0 {
		t.Fatalf("expected no W026 at exact 10x boundary (not strictly greater), got %d", len(found))
	}
}

func TestSeqScanHeavy_ZeroIdxScan(t *testing.T) {
	// SeqScan > 0 and IdxScan = 0: 10*0 = 0, so SeqScan > 0 triggers.
	stats := []TableScanStats{{Schema: "public", Table: "users", SeqScan: 1, IdxScan: 0}}
	diags := DetectSeqScanHeavy(stats)
	found := findByCode(diags, "W026")
	if len(found) != 1 {
		t.Fatalf("expected 1 W026 when idx_scan is zero, got %d", len(found))
	}
}

func TestSeqScanHeavy_EmptyStats(t *testing.T) {
	diags := DetectSeqScanHeavy(nil)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for empty stats, got %d", len(diags))
	}
}

func TestSeqScanHeavy_MultipleTables(t *testing.T) {
	stats := []TableScanStats{
		{Schema: "public", Table: "users", SeqScan: 10000, IdxScan: 10},
		{Schema: "public", Table: "posts", SeqScan: 50, IdxScan: 100},
		{Schema: "public", Table: "logs", SeqScan: 5000, IdxScan: 1},
	}
	diags := DetectSeqScanHeavy(stats)
	found := findByCode(diags, "W026")
	if len(found) != 2 {
		t.Fatalf("expected 2 W026 (users and logs), got %d", len(found))
	}
}

// ---------------------------------------------------------------------------
// DetectLowSelectivityIndexes
// ---------------------------------------------------------------------------

func TestLowSelectivity_I006_Detected(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "users",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "is_active", PGType: "boolean"},
			},
			PK: []string{"id"},
			Indexes: []model.Index{{
				Name:    "idx_users_active",
				Columns: []string{"is_active"},
			}},
		}},
	}
	diags := DetectLowSelectivityIndexes(schema)
	found := findByCode(diags, "I006")
	if len(found) != 1 {
		t.Fatalf("expected 1 I006, got %d", len(found))
	}
	if found[0].Table != "users" {
		t.Errorf("expected table users, got %s", found[0].Table)
	}
	if found[0].Column != "is_active" {
		t.Errorf("expected column is_active, got %s", found[0].Column)
	}
	if found[0].Severity != diagnostic.Info {
		t.Errorf("expected Info severity, got %d", found[0].Severity)
	}
}

func TestLowSelectivity_I006_NoBooleanIndex(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "users",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "is_active", PGType: "boolean"},
			},
			PK: []string{"id"},
		}},
	}
	diags := DetectLowSelectivityIndexes(schema)
	found := findByCode(diags, "I006")
	if len(found) != 0 {
		t.Fatalf("expected no I006 when no index on boolean column, got %d", len(found))
	}
}

func TestLowSelectivity_I006_MultiColumnIndex(t *testing.T) {
	// Boolean in a multi-column index should NOT trigger I006.
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "users",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "is_active", PGType: "boolean"},
				{Name: "name", PGType: "text"},
			},
			PK: []string{"id"},
			Indexes: []model.Index{{
				Name:    "idx_users_active_name",
				Columns: []string{"is_active", "name"},
			}},
		}},
	}
	diags := DetectLowSelectivityIndexes(schema)
	found := findByCode(diags, "I006")
	if len(found) != 0 {
		t.Fatalf("expected no I006 for multi-column index with boolean, got %d", len(found))
	}
}

func TestLowSelectivity_I006_NonBooleanColumn(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name: "users",
			Columns: []model.Column{
				{Name: "id", PGType: "bigint"},
				{Name: "status", PGType: "text"},
			},
			PK: []string{"id"},
			Indexes: []model.Index{{
				Name:    "idx_users_status",
				Columns: []string{"status"},
			}},
		}},
	}
	diags := DetectLowSelectivityIndexes(schema)
	found := findByCode(diags, "I006")
	if len(found) != 0 {
		t.Fatalf("expected no I006 for non-boolean column, got %d", len(found))
	}
}

// ---------------------------------------------------------------------------
// DetectExcessiveIndexes
// ---------------------------------------------------------------------------

func TestExcessiveIndexes_I007_Detected(t *testing.T) {
	indexes := make([]model.Index, 10)
	for i := range indexes {
		indexes[i] = model.Index{Name: "idx_" + string(rune('a'+i)), Columns: []string{"col"}}
	}
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "big_table",
			Columns: []model.Column{{Name: "col", PGType: "text"}},
			PK:      []string{"col"},
			Indexes: indexes,
		}},
	}
	diags := DetectExcessiveIndexes(schema)
	found := findByCode(diags, "I007")
	if len(found) != 1 {
		t.Fatalf("expected 1 I007, got %d", len(found))
	}
	if found[0].Table != "big_table" {
		t.Errorf("expected table big_table, got %s", found[0].Table)
	}
	if found[0].Severity != diagnostic.Info {
		t.Errorf("expected Info severity, got %d", found[0].Severity)
	}
}

func TestExcessiveIndexes_I007_BelowThreshold(t *testing.T) {
	indexes := make([]model.Index, 5)
	for i := range indexes {
		indexes[i] = model.Index{Name: "idx_" + string(rune('a'+i)), Columns: []string{"col"}}
	}
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "normal_table",
			Columns: []model.Column{{Name: "col", PGType: "text"}},
			PK:      []string{"col"},
			Indexes: indexes,
		}},
	}
	diags := DetectExcessiveIndexes(schema)
	found := findByCode(diags, "I007")
	if len(found) != 0 {
		t.Fatalf("expected no I007 for 5 indexes, got %d", len(found))
	}
}

func TestExcessiveIndexes_I007_ExactlyNine(t *testing.T) {
	indexes := make([]model.Index, 9)
	for i := range indexes {
		indexes[i] = model.Index{Name: "idx_" + string(rune('a'+i)), Columns: []string{"col"}}
	}
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "borderline",
			Columns: []model.Column{{Name: "col", PGType: "text"}},
			PK:      []string{"col"},
			Indexes: indexes,
		}},
	}
	diags := DetectExcessiveIndexes(schema)
	found := findByCode(diags, "I007")
	if len(found) != 0 {
		t.Fatalf("expected no I007 for 9 indexes, got %d", len(found))
	}
}

func TestExcessiveIndexes_I007_NoIndexes(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:    "empty",
			Columns: []model.Column{{Name: "id", PGType: "bigint"}},
			PK:      []string{"id"},
		}},
	}
	diags := DetectExcessiveIndexes(schema)
	found := findByCode(diags, "I007")
	if len(found) != 0 {
		t.Fatalf("expected no I007 for zero indexes, got %d", len(found))
	}
}

// ---------------------------------------------------------------------------
// FindDuplicateIndexes
// ---------------------------------------------------------------------------

func TestFindDuplicateIndexes_Detected(t *testing.T) {
	indexes := []IndexInfo{
		{Schema: "public", Table: "users", Name: "idx_a", Columns: []string{"a"}},
		{Schema: "public", Table: "users", Name: "idx_ab", Columns: []string{"a", "b"}},
	}
	dups := FindDuplicateIndexes(indexes)
	if len(dups) != 1 {
		t.Fatalf("expected 1 duplicate, got %d", len(dups))
	}
	if dups[0].Index != "idx_a" {
		t.Errorf("expected subsumed index idx_a, got %s", dups[0].Index)
	}
	if dups[0].SupersetIndex != "idx_ab" {
		t.Errorf("expected superset index idx_ab, got %s", dups[0].SupersetIndex)
	}
	if dups[0].Schema != "public" {
		t.Errorf("expected schema public, got %s", dups[0].Schema)
	}
	if dups[0].Table != "users" {
		t.Errorf("expected table users, got %s", dups[0].Table)
	}
}

func TestFindDuplicateIndexes_SameColumnsNotDuplicate(t *testing.T) {
	indexes := []IndexInfo{
		{Schema: "public", Table: "users", Name: "idx_a1", Columns: []string{"a", "b"}},
		{Schema: "public", Table: "users", Name: "idx_a2", Columns: []string{"a", "b"}},
	}
	dups := FindDuplicateIndexes(indexes)
	if len(dups) != 0 {
		t.Fatalf("expected no duplicates for same columns (not strict prefix), got %d", len(dups))
	}
}

func TestFindDuplicateIndexes_DifferentTables(t *testing.T) {
	indexes := []IndexInfo{
		{Schema: "public", Table: "users", Name: "idx_users_a", Columns: []string{"a"}},
		{Schema: "public", Table: "posts", Name: "idx_posts_ab", Columns: []string{"a", "b"}},
	}
	dups := FindDuplicateIndexes(indexes)
	if len(dups) != 0 {
		t.Fatalf("expected no duplicates across different tables, got %d", len(dups))
	}
}

func TestFindDuplicateIndexes_DifferentSchemas(t *testing.T) {
	indexes := []IndexInfo{
		{Schema: "public", Table: "users", Name: "idx_a", Columns: []string{"a"}},
		{Schema: "private", Table: "users", Name: "idx_ab", Columns: []string{"a", "b"}},
	}
	dups := FindDuplicateIndexes(indexes)
	if len(dups) != 0 {
		t.Fatalf("expected no duplicates across different schemas, got %d", len(dups))
	}
}

func TestFindDuplicateIndexes_MultiLevel(t *testing.T) {
	// a < a,b < a,b,c: both a and a,b are subsumed by a,b,c; a is also subsumed by a,b.
	indexes := []IndexInfo{
		{Schema: "public", Table: "users", Name: "idx_a", Columns: []string{"a"}},
		{Schema: "public", Table: "users", Name: "idx_ab", Columns: []string{"a", "b"}},
		{Schema: "public", Table: "users", Name: "idx_abc", Columns: []string{"a", "b", "c"}},
	}
	dups := FindDuplicateIndexes(indexes)
	// idx_a subsumed by idx_ab, idx_a subsumed by idx_abc, idx_ab subsumed by idx_abc
	if len(dups) != 3 {
		t.Fatalf("expected 3 duplicates (multi-level prefix chain), got %d", len(dups))
	}
}

func TestFindDuplicateIndexes_NonPrefixOverlap(t *testing.T) {
	// (a, c) is not a prefix of (a, b, c).
	indexes := []IndexInfo{
		{Schema: "public", Table: "users", Name: "idx_ac", Columns: []string{"a", "c"}},
		{Schema: "public", Table: "users", Name: "idx_abc", Columns: []string{"a", "b", "c"}},
	}
	dups := FindDuplicateIndexes(indexes)
	if len(dups) != 0 {
		t.Fatalf("expected no duplicates for non-prefix overlap, got %d", len(dups))
	}
}

func TestFindDuplicateIndexes_Empty(t *testing.T) {
	dups := FindDuplicateIndexes(nil)
	if len(dups) != 0 {
		t.Fatalf("expected no duplicates for empty input, got %d", len(dups))
	}
}

func TestFindDuplicateIndexes_SingleIndex(t *testing.T) {
	indexes := []IndexInfo{
		{Schema: "public", Table: "users", Name: "idx_a", Columns: []string{"a"}},
	}
	dups := FindDuplicateIndexes(indexes)
	if len(dups) != 0 {
		t.Fatalf("expected no duplicates for single index, got %d", len(dups))
	}
}
