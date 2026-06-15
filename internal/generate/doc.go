package generate

import (
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
)

// generateDoc produces a Markdown data dictionary from the resolved schema.
func generateDoc(schema *model.Schema) string {
	var b strings.Builder

	// Schema heading
	if schema.Name != "" {
		fmt.Fprintf(&b, "# Schema: %s\n", schema.Name)
	} else {
		b.WriteString("# Schema Documentation\n")
	}

	// Enums section
	if len(schema.Enums) > 0 {
		b.WriteString("\n## Enums\n")
		for _, e := range schema.Enums {
			fmt.Fprintf(&b, "\n### %s\n", e.Name)
			if e.Comment != "" {
				fmt.Fprintf(&b, "\n%s\n", e.Comment)
			}
			vals := make([]string, len(e.Values))
			for i, v := range e.Values {
				vals[i] = "`" + v + "`"
			}
			fmt.Fprintf(&b, "\nValues: %s\n", strings.Join(vals, ", "))
		}
	}

	// Tables in dependency order
	tables := schema.TableOrder()
	for _, t := range tables {
		fmt.Fprintf(&b, "\n## %s\n", t.Name)

		if t.Comment != "" {
			fmt.Fprintf(&b, "\n%s\n", t.Comment)
		}

		// Column table
		b.WriteString("\n| Column | Type | Nullable | Default | Comment |\n")
		b.WriteString("|--------|------|----------|---------|--------|\n")
		for _, col := range t.Columns {
			nullable := "NOT NULL"
			if !col.NotNull {
				nullable = "nullable"
			}
			def := ""
			if col.DefaultExpr != "" {
				def = col.DefaultExpr
			} else if col.Default != nil {
				def = *col.Default
			}
			comment := col.Comment
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", col.Name, col.PGType, nullable, def, comment)
		}

		// Primary Key
		if len(t.PK) > 0 {
			fmt.Fprintf(&b, "\n**Primary Key:** %s\n", strings.Join(t.PK, ", "))
		}

		// Foreign Keys
		if len(t.FKs) > 0 {
			b.WriteString("\n**Foreign Keys:**\n")
			for _, fk := range sortedFKs(t.FKs) {
				for i, col := range fk.Columns {
					refCol := fk.RefColumns[i]
					var ref string
					if fk.RefSchema != "" && fk.RefSchema != t.Schema {
						ref = fmt.Sprintf("%s.%s.%s", fk.RefSchema, fk.RefTable, refCol)
					} else {
						ref = fmt.Sprintf("%s.%s", fk.RefTable, refCol)
					}
					fmt.Fprintf(&b, "- %s -> %s (ON DELETE %s)\n", col, ref, fk.OnDelete)
				}
			}
		}

		// Indexes
		if len(t.Indexes) > 0 {
			b.WriteString("\n**Indexes:**\n")
			for _, idx := range sortedIndexes(t.Indexes) {
				var parts []string
				if idx.Method != "" {
					parts = append(parts, idx.Method)
				}
				if idx.Unique {
					parts = append(parts, "unique")
				}
				meta := ""
				if len(parts) > 0 {
					meta = " (" + strings.Join(parts, ", ") + ")"
				}
				where := ""
				if idx.Where != "" {
					where = " WHERE " + idx.Where
				}
				fmt.Fprintf(&b, "- %s%s on (%s)%s\n", idx.Name, meta, strings.Join(idx.Columns, ", "), where)
			}
		}

		// Unique Constraints
		if len(t.Uniques) > 0 {
			b.WriteString("\n**Unique Constraints:**\n")
			for _, uq := range sortedUniques(t.Uniques) {
				fmt.Fprintf(&b, "- %s on (%s)\n", uq.Name, strings.Join(uq.Columns, ", "))
			}
		}

		// Check Constraints
		if len(t.Checks) > 0 {
			b.WriteString("\n**Check Constraints:**\n")
			for _, ck := range sortedChecks(t.Checks) {
				fmt.Fprintf(&b, "- %s: %s\n", ck.Name, ck.Expr)
			}
		}

		// Policies
		if len(t.Policies) > 0 {
			b.WriteString("\n**Policies:**\n")
			for _, p := range sortedPolicies(t.Policies) {
				var pParts []string
				pParts = append(pParts, p.Operation)
				if p.Role != "" {
					pParts = append(pParts, "TO "+p.Role)
				}
				if p.Using != "" {
					pParts = append(pParts, "USING ("+p.Using+")")
				}
				if p.WithCheck != "" {
					pParts = append(pParts, "WITH CHECK ("+p.WithCheck+")")
				}
				fmt.Fprintf(&b, "- %s: %s\n", p.Name, strings.Join(pParts, " "))
			}
		}

		// Partitioning
		if t.Partitioning != nil {
			fmt.Fprintf(&b, "\n**Partitioning:** %s on %s\n", t.Partitioning.Strategy, t.Partitioning.Column)
		}

		// RLS
		if t.EnableRLS {
			b.WriteString("\n**Row Level Security:** enabled\n")
		}

		// Append Only
		if t.AppendOnly {
			b.WriteString("\n**Append Only:** yes\n")
		}
	}

	// Views section
	for _, v := range schema.Views {
		fmt.Fprintf(&b, "\n## %s\n", v.Name)

		if v.Comment != "" {
			fmt.Fprintf(&b, "\n%s\n", v.Comment)
		}

		b.WriteString("\n### Query\n")
		fmt.Fprintf(&b, "\n```sql\n%s\n```\n", v.Query)

		if len(v.DependsOn) > 0 {
			b.WriteString("\n### Dependencies\n\n")
			for _, dep := range v.DependsOn {
				fmt.Fprintf(&b, "- %s\n", dep)
			}
		}
	}

	return b.String()
}
