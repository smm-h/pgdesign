package workload

import (
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

// StructuralRecommendations analyzes a schema and returns index recommendations
// based on column types and table properties, without requiring a live database.
func StructuralRecommendations(schema *model.Schema) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic
	for i := range schema.Tables {
		table := &schema.Tables[i]
		for j := range table.Columns {
			col := &table.Columns[j]

			hasGIN := columnHasIndexMethod(table, col.Name, "gin")

			// W022: JSONB columns without a GIN index
			if col.PGType.Base == "jsonb" && !col.Array && !hasGIN {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "W022",
					Table:      table.Name,
					Column:     col.Name,
					Message:    "JSONB column without GIN index",
					Suggestion: "Consider adding a GIN index for efficient containment queries",
				})
			}

			// W023: Array columns without a GIN index
			if col.Array && !hasGIN {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "W023",
					Table:      table.Name,
					Column:     col.Name,
					Message:    "Array column without GIN index",
					Suggestion: "Consider adding a GIN index for efficient array containment queries",
				})
			}

			// W024: tsvector columns without a GIN index
			if col.PGType.Base == "tsvector" && !hasGIN {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Warning,
					Code:       "W024",
					Table:      table.Name,
					Column:     col.Name,
					Message:    "tsvector column without GIN index",
					Suggestion: "Consider adding a GIN index for efficient full-text search",
				})
			}

			// I005: Timestamp columns on append_only tables without a BRIN index
			if table.AppendOnly && isTimestamp(col.PGType.Base) && !columnHasIndexMethod(table, col.Name, "brin") {
				diags = append(diags, diagnostic.Diagnostic{
					Severity:   diagnostic.Info,
					Code:       "I005",
					Table:      table.Name,
					Column:     col.Name,
					Message:    "Timestamp column on append-only table without BRIN index",
					Suggestion: "Consider adding a BRIN index for efficient range scans on naturally ordered data",
				})
			}
		}
	}
	return diags
}

// columnHasIndexMethod checks whether the given column is covered by an index
// using the specified method (e.g., "gin", "brin").
func columnHasIndexMethod(table *model.Table, colName, method string) bool {
	for _, idx := range table.Indexes {
		if !strings.EqualFold(idx.Method, method) {
			continue
		}
		for _, c := range idx.Columns {
			if c == colName {
				return true
			}
		}
	}
	return false
}

func isTimestamp(pgType string) bool {
	return pgType == "timestamptz" || pgType == "timestamp"
}
