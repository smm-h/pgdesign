package codegen

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

func TestExtractConstraints_Basic(t *testing.T) {
	schema := model.Schema{
		Enums: []model.Enum{
			{Name: "status", Values: []string{"active", "inactive", "pending"}},
		},
		Tables: []model.Table{
			{
				Name: "users",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("bigint"), NotNull: true},
					{Name: "name", PGType: typeinfo.MustParse("text"), NotNull: true},
					{Name: "email", PGType: typeinfo.MustParse("text"), NotNull: true},
					{Name: "status", PGType: typeinfo.MustParse("status"), NotNull: true},
					{Name: "bio", PGType: typeinfo.MustParse("text"), NotNull: false},
					{Name: "age", PGType: typeinfo.MustParse("integer"), NotNull: true},
					{Name: "profile", PGType: typeinfo.MustParse("jsonb"), NotNull: true, JSONSchema: "schemas/profile.json"},
					{Name: "auto_id", PGType: typeinfo.MustParse("bigint"), NotNull: true, Identity: "ALWAYS"},
					{Name: "computed", PGType: typeinfo.MustParse("text"), NotNull: true, Generated: "lower(name)"},
				},
				PK: []string{"id"},
				Checks: []model.CheckConstraint{
					{Name: "ck_users_age_range", Expr: "age >= 0 AND age <= 150"},
					{Name: "ck_users_email_fmt", Expr: "email LIKE '%@%'"},
					{Name: "ck_users_multi", Expr: "age >= 0 AND name <> ''"},
				},
			},
		},
	}

	cs := ExtractConstraints(schema.Tables[0], schema)

	// NOT NULL fields: name, email, status, age, profile
	// Excludes: id (PK), auto_id (identity), computed (generated), bio (nullable)
	wantNotNull := map[string]bool{
		"name": true, "email": true, "status": true, "age": true, "profile": true,
	}
	if len(cs.NotNullFields) != len(wantNotNull) {
		t.Fatalf("NotNullFields length = %d, want %d; got %v", len(cs.NotNullFields), len(wantNotNull), cs.NotNullFields)
	}
	for _, f := range cs.NotNullFields {
		if !wantNotNull[f] {
			t.Errorf("unexpected NotNullField: %q", f)
		}
	}

	// Enum fields.
	if vals, ok := cs.EnumFields["status"]; !ok {
		t.Error("EnumFields missing 'status'")
	} else {
		want := []string{"active", "inactive", "pending"}
		if len(vals) != len(want) {
			t.Fatalf("EnumFields[status] = %v, want %v", vals, want)
		}
		for i, v := range vals {
			if v != want[i] {
				t.Errorf("EnumFields[status][%d] = %q, want %q", i, v, want[i])
			}
		}
	}
	if len(cs.EnumFields) != 1 {
		t.Errorf("EnumFields length = %d, want 1", len(cs.EnumFields))
	}

	// CHECK expressions: single-column checks only.
	// "age >= 0 AND age <= 150" -> single column "age" (range)
	// "email LIKE '%@%'" -> single column "email"
	// "age >= 0 AND name <> ''" -> multi-column, excluded
	if expr, ok := cs.CheckExprs["age"]; !ok {
		t.Error("CheckExprs missing 'age'")
	} else if expr != "age >= 0 AND age <= 150" {
		t.Errorf("CheckExprs[age] = %q, want %q", expr, "age >= 0 AND age <= 150")
	}
	if expr, ok := cs.CheckExprs["email"]; !ok {
		t.Error("CheckExprs missing 'email'")
	} else if expr != "email LIKE '%@%'" {
		t.Errorf("CheckExprs[email] = %q, want %q", expr, "email LIKE '%@%'")
	}
	if _, ok := cs.CheckExprs["name"]; ok {
		t.Error("CheckExprs should not contain multi-column check column 'name'")
	}
	if len(cs.CheckExprs) != 2 {
		t.Errorf("CheckExprs length = %d, want 2", len(cs.CheckExprs))
	}

	// JSON schemas.
	if path, ok := cs.JSONSchemas["profile"]; !ok {
		t.Error("JSONSchemas missing 'profile'")
	} else if path != "schemas/profile.json" {
		t.Errorf("JSONSchemas[profile] = %q, want %q", path, "schemas/profile.json")
	}
	if len(cs.JSONSchemas) != 1 {
		t.Errorf("JSONSchemas length = %d, want 1", len(cs.JSONSchemas))
	}

	// HasConstraints should be true.
	if !cs.HasConstraints() {
		t.Error("HasConstraints() = false, want true")
	}
}

func TestExtractConstraints_EmptyTable(t *testing.T) {
	schema := model.Schema{
		Tables: []model.Table{
			{
				Name: "empty",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.MustParse("bigint"), NotNull: true},
					{Name: "data", PGType: typeinfo.MustParse("text"), NotNull: false},
				},
				PK: []string{"id"},
			},
		},
	}

	cs := ExtractConstraints(schema.Tables[0], schema)

	if cs.HasConstraints() {
		t.Error("HasConstraints() = true, want false")
	}
	if len(cs.NotNullFields) != 0 {
		t.Errorf("NotNullFields = %v, want empty", cs.NotNullFields)
	}
	if cs.EnumFields == nil {
		t.Error("EnumFields should be non-nil (empty map)")
	}
	if len(cs.EnumFields) != 0 {
		t.Errorf("EnumFields length = %d, want 0", len(cs.EnumFields))
	}
	if cs.CheckExprs == nil {
		t.Error("CheckExprs should be non-nil (empty map)")
	}
	if len(cs.CheckExprs) != 0 {
		t.Errorf("CheckExprs length = %d, want 0", len(cs.CheckExprs))
	}
	if cs.JSONSchemas == nil {
		t.Error("JSONSchemas should be non-nil (empty map)")
	}
	if len(cs.JSONSchemas) != 0 {
		t.Errorf("JSONSchemas length = %d, want 0", len(cs.JSONSchemas))
	}
}

func TestClassifyCheck(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		wantType string // "range", "comparison", "length", "like", or "" for nil
	}{
		{"range", "age >= 0 AND age <= 150", "range"},
		{"comparison >=", "total >= 0", "comparison"},
		{"comparison >", "price > 0", "comparison"},
		{"length <=", "LENGTH(name) <= 100", "length"},
		{"length func variant", "CHAR_LENGTH(slug) <= 50", "length"},
		{"like", "email LIKE '%@%'", "like"},
		{"not like", "name NOT LIKE '%admin%'", "like"},
		{"multi-column", "a >= 0 AND b <= 10", ""},
		{"unparseable", "invalid sql {{}", ""},
		{"no column ref", "1 = 1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pat := classifyCheck(tt.expr)
			if tt.wantType == "" {
				if pat != nil {
					t.Errorf("expected nil, got %s", pat.patternType())
				}
				return
			}
			if pat == nil {
				t.Fatalf("expected %s pattern, got nil", tt.wantType)
			}
			if pat.patternType() != tt.wantType {
				t.Errorf("expected %s, got %s", tt.wantType, pat.patternType())
			}
		})
	}
}

func TestClassifyCheck_RangeDetails(t *testing.T) {
	pat := classifyCheck("score >= 0 AND score <= 100")
	rp, ok := pat.(*rangePattern)
	if !ok {
		t.Fatalf("expected rangePattern, got %T", pat)
	}
	if rp.Column != "score" {
		t.Errorf("Column = %q, want %q", rp.Column, "score")
	}
	if rp.Low != "0" {
		t.Errorf("Low = %q, want %q", rp.Low, "0")
	}
	if rp.High != "100" {
		t.Errorf("High = %q, want %q", rp.High, "100")
	}
	if !rp.LowIncl {
		t.Error("LowIncl should be true")
	}
	if !rp.HighIncl {
		t.Error("HighIncl should be true")
	}
}

func TestClassifyCheck_LengthDetails(t *testing.T) {
	pat := classifyCheck("LENGTH(slug) <= 50")
	lp, ok := pat.(*lengthPattern)
	if !ok {
		t.Fatalf("expected lengthPattern, got %T", pat)
	}
	if lp.Column != "slug" {
		t.Errorf("Column = %q, want %q", lp.Column, "slug")
	}
	if lp.Op != "<=" {
		t.Errorf("Op = %q, want %q", lp.Op, "<=")
	}
	if lp.Value != 50 {
		t.Errorf("Value = %d, want %d", lp.Value, 50)
	}
}

func TestLikeToRegex(t *testing.T) {
	tests := []struct {
		pattern string
		want    string
	}{
		{"%@%", "^.*@.*$"},
		{"%.com", "^.*\\.com$"},
		{"a_b", "^a.b$"},
		{"hello", "^hello$"},
		{"test%", "^test.*$"},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got := likeToRegex(tt.pattern)
			if got != tt.want {
				t.Errorf("likeToRegex(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}
