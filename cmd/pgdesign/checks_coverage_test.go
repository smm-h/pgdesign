package main

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

func findByCode(diags []diagnostic.Diagnostic, code string) []diagnostic.Diagnostic {
	var found []diagnostic.Diagnostic
	for _, d := range diags {
		if d.Code == code {
			found = append(found, d)
		}
	}
	return found
}

func TestC100_TableWithoutCheckConstraints(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "orders",
			Schema: "public",
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "user_id", PGType: "uuid"},
				{Name: "total", PGType: "numeric"},
			},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C100")
	if len(found) == 0 {
		t.Fatal("expected C100 for table without check constraints")
	}
	if found[0].Table != "orders" {
		t.Errorf("expected table 'orders', got %q", found[0].Table)
	}
}

func TestC100_SkipsSmallTable(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "tags",
			Schema: "public",
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "name", PGType: "text"},
			},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C100")
	if len(found) != 0 {
		t.Fatal("expected no C100 for table with only 2 columns")
	}
}

func TestC100_SkipsAppendOnly(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:       "events",
			Schema:     "public",
			AppendOnly: true,
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "data", PGType: "jsonb"},
				{Name: "created_at", PGType: "timestamptz"},
			},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C100")
	if len(found) != 0 {
		t.Fatal("expected no C100 for append-only table")
	}
}

func TestC100_PassWithChecks(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "orders",
			Schema: "public",
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "total", PGType: "numeric"},
				{Name: "status", PGType: "text"},
			},
			Checks: []model.CheckConstraint{
				{Name: "chk_total_positive", Expr: "total > 0"},
			},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C100")
	if len(found) != 0 {
		t.Fatal("expected no C100 for table with check constraints")
	}
}

func TestC101_FKWithoutIndex(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "orders",
			Schema: "public",
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "user_id", PGType: "uuid"},
			},
			FKs: []model.FK{{
				Name:      "fk_user",
				Columns:   []string{"user_id"},
				RefSchema: "public",
				RefTable:  "users",
				OnDelete:  "CASCADE",
			}},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C101")
	if len(found) == 0 {
		t.Fatal("expected C101 for FK without covering index")
	}
	if found[0].Table != "orders" {
		t.Errorf("expected table 'orders', got %q", found[0].Table)
	}
}

func TestC101_FKWithIndex(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "orders",
			Schema: "public",
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "user_id", PGType: "uuid"},
			},
			FKs: []model.FK{{
				Name:      "fk_user",
				Columns:   []string{"user_id"},
				RefSchema: "public",
				RefTable:  "users",
				OnDelete:  "CASCADE",
			}},
			Indexes: []model.Index{{
				Name:    "idx_orders_user_id",
				Columns: []string{"user_id"},
			}},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C101")
	if len(found) != 0 {
		t.Fatal("expected no C101 when FK has covering index")
	}
}

func TestC102_UnusedEnum(t *testing.T) {
	schema := &model.Schema{
		Enums: []model.Enum{{
			Name:   "status",
			Values: []string{"active", "inactive"},
		}},
		Tables: []model.Table{{
			Name:   "users",
			Schema: "public",
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "name", PGType: "text"},
			},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C102")
	if len(found) == 0 {
		t.Fatal("expected C102 for unused enum")
	}
}

func TestC102_UsedEnum(t *testing.T) {
	schema := &model.Schema{
		Enums: []model.Enum{{
			Name:   "status",
			Values: []string{"active", "inactive"},
		}},
		Tables: []model.Table{{
			Name:   "users",
			Schema: "public",
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "status", PGType: "status"},
			},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C102")
	if len(found) != 0 {
		t.Fatal("expected no C102 when enum is used")
	}
}

func TestC103_OrphanTable(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "orphan",
			Schema: "public",
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "name", PGType: "text"},
				{Name: "data", PGType: "jsonb"},
			},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C103")
	if len(found) == 0 {
		t.Fatal("expected C103 for orphan table")
	}
	if found[0].Table != "orphan" {
		t.Errorf("expected table 'orphan', got %q", found[0].Table)
	}
}

func TestC103_SkipsSmallTable(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "small",
			Schema: "public",
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "name", PGType: "text"},
			},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C103")
	if len(found) != 0 {
		t.Fatal("expected no C103 for table with only 2 columns")
	}
}

func TestC103_TableWithOutgoingFK(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{{
			Name:   "orders",
			Schema: "public",
			Columns: []model.Column{
				{Name: "id", PGType: "uuid"},
				{Name: "user_id", PGType: "uuid"},
				{Name: "total", PGType: "numeric"},
			},
			FKs: []model.FK{{
				Name:      "fk_user",
				Columns:   []string{"user_id"},
				RefSchema: "public",
				RefTable:  "users",
				OnDelete:  "CASCADE",
			}},
		}},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C103")
	if len(found) != 0 {
		t.Fatal("expected no C103 for table with outgoing FK")
	}
}

func TestC103_TableReferencedByOther(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid"},
					{Name: "name", PGType: "text"},
					{Name: "email", PGType: "text"},
				},
			},
			{
				Name:   "orders",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid"},
					{Name: "user_id", PGType: "uuid"},
					{Name: "total", PGType: "numeric"},
				},
				FKs: []model.FK{{
					Name:      "fk_user",
					Columns:   []string{"user_id"},
					RefSchema: "public",
					RefTable:  "users",
					OnDelete:  "CASCADE",
				}},
			},
		},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C103")
	for _, d := range found {
		if d.Table == "users" {
			t.Fatal("expected no C103 for table referenced by another")
		}
	}
}

func TestC104_SuggestsFilterIndex(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid"},
					{Name: "status", PGType: "text"},
					{Name: "name", PGType: "text"},
				},
			},
			{
				Name:   "orders",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid"},
					{Name: "user_id", PGType: "uuid"},
					{Name: "total", PGType: "numeric"},
				},
				FKs: []model.FK{{
					Name:       "fk_user",
					Columns:    []string{"user_id"},
					RefSchema:  "public",
					RefTable:   "users",
					RefColumns: []string{"id"},
					OnDelete:   "CASCADE",
				}},
			},
		},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C104")
	if len(found) == 0 {
		t.Fatal("expected C104 suggesting index for filtered joins")
	}
	if found[0].Table != "orders" {
		t.Errorf("expected table 'orders', got %q", found[0].Table)
	}
}

func TestC104_NoSuggestionWithoutFilterColumns(t *testing.T) {
	schema := &model.Schema{
		Tables: []model.Table{
			{
				Name:   "users",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid"},
					{Name: "name", PGType: "text"},
					{Name: "email", PGType: "text"},
				},
			},
			{
				Name:   "orders",
				Schema: "public",
				Columns: []model.Column{
					{Name: "id", PGType: "uuid"},
					{Name: "user_id", PGType: "uuid"},
					{Name: "total", PGType: "numeric"},
				},
				FKs: []model.FK{{
					Name:       "fk_user",
					Columns:    []string{"user_id"},
					RefSchema:  "public",
					RefTable:   "users",
					RefColumns: []string{"id"},
					OnDelete:   "CASCADE",
				}},
			},
		},
	}
	diags := analyzeCoverage(schema)
	found := findByCode(diags, "C104")
	if len(found) != 0 {
		t.Fatal("expected no C104 when referenced table has no filter columns")
	}
}
