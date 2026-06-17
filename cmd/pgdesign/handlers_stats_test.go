package main

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/workload"
)

func TestFindDuplicateIndexes(t *testing.T) {
	tests := []struct {
		name    string
		indexes []workload.IndexInfo
		want    int
	}{
		{
			name:    "no indexes",
			indexes: nil,
			want:    0,
		},
		{
			name: "no duplicates",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "users", Name: "idx_a", Columns: []string{"a"}},
				{Schema: "public", Table: "users", Name: "idx_b", Columns: []string{"b"}},
			},
			want: 0,
		},
		{
			name: "simple prefix",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "users", Name: "idx_a", Columns: []string{"a"}},
				{Schema: "public", Table: "users", Name: "idx_a_b", Columns: []string{"a", "b"}},
			},
			want: 1,
		},
		{
			name: "not prefix different first column",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "users", Name: "idx_b", Columns: []string{"b"}},
				{Schema: "public", Table: "users", Name: "idx_a_b", Columns: []string{"a", "b"}},
			},
			want: 0,
		},
		{
			name: "equal columns not duplicate",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "users", Name: "idx_a_1", Columns: []string{"a", "b"}},
				{Schema: "public", Table: "users", Name: "idx_a_2", Columns: []string{"a", "b"}},
			},
			want: 0,
		},
		{
			name: "different tables not duplicate",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "users", Name: "idx_a", Columns: []string{"a"}},
				{Schema: "public", Table: "orders", Name: "idx_a_b", Columns: []string{"a", "b"}},
			},
			want: 0,
		},
		{
			name: "multiple duplicates",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "users", Name: "idx_a", Columns: []string{"a"}},
				{Schema: "public", Table: "users", Name: "idx_a_b", Columns: []string{"a", "b"}},
				{Schema: "public", Table: "users", Name: "idx_a_b_c", Columns: []string{"a", "b", "c"}},
			},
			want: 3,
		},
		{
			name: "different schemas not duplicate",
			indexes: []workload.IndexInfo{
				{Schema: "public", Table: "users", Name: "idx_a", Columns: []string{"a"}},
				{Schema: "private", Table: "users", Name: "idx_a_b", Columns: []string{"a", "b"}},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workload.FindDuplicateIndexes(tt.indexes)
			if len(got) != tt.want {
				t.Errorf("workload.FindDuplicateIndexes() returned %d duplicates, want %d", len(got), tt.want)
				for _, d := range got {
					t.Logf("  %s is a prefix of %s (table: %s.%s)", d.Index, d.SupersetIndex, d.Schema, d.Table)
				}
			}
		})
	}
}

func TestFindDuplicateIndexesFields(t *testing.T) {
	indexes := []workload.IndexInfo{
		{Schema: "public", Table: "users", Name: "idx_email", Columns: []string{"email"}},
		{Schema: "public", Table: "users", Name: "idx_email_name", Columns: []string{"email", "name"}},
	}
	got := workload.FindDuplicateIndexes(indexes)
	if len(got) != 1 {
		t.Fatalf("expected 1 duplicate, got %d", len(got))
	}
	d := got[0]
	if d.Schema != "public" {
		t.Errorf("Schema = %q, want %q", d.Schema, "public")
	}
	if d.Table != "users" {
		t.Errorf("Table = %q, want %q", d.Table, "users")
	}
	if d.Index != "idx_email" {
		t.Errorf("Index = %q, want %q", d.Index, "idx_email")
	}
	if d.SupersetIndex != "idx_email_name" {
		t.Errorf("SupersetIndex = %q, want %q", d.SupersetIndex, "idx_email_name")
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1,000"},
		{1234567, "1,234,567"},
		{-1234, "-1,234"},
	}
	for _, tt := range tests {
		got := formatNumber(tt.n)
		if got != tt.want {
			t.Errorf("formatNumber(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
