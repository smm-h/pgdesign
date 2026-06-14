package introspect

import (
	"fmt"

	tomledit "github.com/smm-h/go-toml-edit"
	"github.com/smm-h/pgdesign/internal/model"
)

// Export serializes a model.Schema to pgdesign TOML format using go-toml-edit
// AST building. The output is produced by Format for consistent formatting.
func Export(schema *model.Schema) ([]byte, error) {
	doc, err := tomledit.Parse([]byte(""))
	if err != nil {
		return nil, fmt.Errorf("create document: %w", err)
	}

	// [meta]
	if err := doc.NewTable("meta"); err != nil {
		return nil, fmt.Errorf("create meta table: %w", err)
	}
	if err := doc.SetCreate("meta.version", 1); err != nil {
		return nil, fmt.Errorf("set meta.version: %w", err)
	}
	if schema.Name != "" {
		if err := doc.SetCreate("meta.schema", schema.Name); err != nil {
			return nil, fmt.Errorf("set meta.schema: %w", err)
		}
	}
	if len(schema.Extensions) > 0 {
		if err := doc.SetCreate("meta.extensions", toAnySlice(schema.Extensions)); err != nil {
			return nil, fmt.Errorf("set meta.extensions: %w", err)
		}
	}

	// [types.*] for enums
	for _, e := range schema.Enums {
		path := "types." + e.Name
		if err := doc.NewTable(path); err != nil {
			return nil, fmt.Errorf("create type %s: %w", e.Name, err)
		}
		if err := doc.SetCreate(path+".kind", "enum"); err != nil {
			return nil, fmt.Errorf("set %s.kind: %w", path, err)
		}
		if err := doc.SetCreate(path+".values", toAnySlice(e.Values)); err != nil {
			return nil, fmt.Errorf("set %s.values: %w", path, err)
		}
		if e.Comment != "" {
			if err := doc.SetCreate(path+".comment", e.Comment); err != nil {
				return nil, fmt.Errorf("set %s.comment: %w", path, err)
			}
		}
	}

	// [tables.*]
	for _, t := range schema.Tables {
		tblPath := "tables." + t.Name
		if err := doc.NewTable(tblPath); err != nil {
			return nil, fmt.Errorf("create table %s: %w", t.Name, err)
		}
		if t.Comment != "" {
			if err := doc.SetCreate(tblPath+".comment", t.Comment); err != nil {
				return nil, fmt.Errorf("set %s.comment: %w", tblPath, err)
			}
		}
		if len(t.PK) > 0 {
			if err := doc.SetCreate(tblPath+".pk", toAnySlice(t.PK)); err != nil {
				return nil, fmt.Errorf("set %s.pk: %w", tblPath, err)
			}
		}

		// Columns
		for _, c := range t.Columns {
			colPath := tblPath + ".columns." + c.Name
			if err := doc.NewTable(colPath); err != nil {
				return nil, fmt.Errorf("create column %s: %w", colPath, err)
			}
			if err := doc.SetCreate(colPath+".type", c.PGType); err != nil {
				return nil, fmt.Errorf("set %s.type: %w", colPath, err)
			}
			if !c.NotNull {
				if err := doc.SetCreate(colPath+".nullable", true); err != nil {
					return nil, fmt.Errorf("set %s.nullable: %w", colPath, err)
				}
			}
			if c.Default != nil {
				if err := doc.SetCreate(colPath+".default", *c.Default); err != nil {
					return nil, fmt.Errorf("set %s.default: %w", colPath, err)
				}
			}
			if c.DefaultExpr != "" {
				if err := doc.SetCreate(colPath+".default_expr", c.DefaultExpr); err != nil {
					return nil, fmt.Errorf("set %s.default_expr: %w", colPath, err)
				}
			}
			if c.Identity != "" {
				if err := doc.SetCreate(colPath+".identity", c.Identity); err != nil {
					return nil, fmt.Errorf("set %s.identity: %w", colPath, err)
				}
			}
			if c.Generated != "" {
				if err := doc.SetCreate(colPath+".generated", c.Generated); err != nil {
					return nil, fmt.Errorf("set %s.generated: %w", colPath, err)
				}
				if c.Stored {
					if err := doc.SetCreate(colPath+".stored", true); err != nil {
						return nil, fmt.Errorf("set %s.stored: %w", colPath, err)
					}
				}
			}
			if c.Array {
				if err := doc.SetCreate(colPath+".array", true); err != nil {
					return nil, fmt.Errorf("set %s.array: %w", colPath, err)
				}
			}
			if c.Comment != "" {
				if err := doc.SetCreate(colPath+".comment", c.Comment); err != nil {
					return nil, fmt.Errorf("set %s.comment: %w", colPath, err)
				}
			}
		}

		// Foreign keys
		for _, fk := range t.FKs {
			fkPath := tblPath + ".fks." + fk.Name
			if err := doc.NewTable(fkPath); err != nil {
				return nil, fmt.Errorf("create fk %s: %w", fkPath, err)
			}
			if err := doc.SetCreate(fkPath+".columns", toAnySlice(fk.Columns)); err != nil {
				return nil, fmt.Errorf("set %s.columns: %w", fkPath, err)
			}
			if err := doc.SetCreate(fkPath+".ref_table", fk.RefTable); err != nil {
				return nil, fmt.Errorf("set %s.ref_table: %w", fkPath, err)
			}
			if err := doc.SetCreate(fkPath+".ref_columns", toAnySlice(fk.RefColumns)); err != nil {
				return nil, fmt.Errorf("set %s.ref_columns: %w", fkPath, err)
			}
			if fk.OnDelete != "" {
				if err := doc.SetCreate(fkPath+".on_delete", fk.OnDelete); err != nil {
					return nil, fmt.Errorf("set %s.on_delete: %w", fkPath, err)
				}
			}
		}

		// Indexes
		for _, idx := range t.Indexes {
			idxPath := tblPath + ".indexes." + idx.Name
			if err := doc.NewTable(idxPath); err != nil {
				return nil, fmt.Errorf("create index %s: %w", idxPath, err)
			}
			if err := doc.SetCreate(idxPath+".columns", toAnySlice(indexColumnsWithDir(idx.Columns, idx.Desc))); err != nil {
				return nil, fmt.Errorf("set %s.columns: %w", idxPath, err)
			}
			if idx.Method != "" && idx.Method != "btree" {
				if err := doc.SetCreate(idxPath+".method", idx.Method); err != nil {
					return nil, fmt.Errorf("set %s.method: %w", idxPath, err)
				}
			}
			if len(idx.Opclasses) > 0 {
				if err := setOpclass(doc, idxPath, idx); err != nil {
					return nil, err
				}
			}
			if idx.Where != "" {
				if err := doc.SetCreate(idxPath+".where", idx.Where); err != nil {
					return nil, fmt.Errorf("set %s.where: %w", idxPath, err)
				}
			}
			if len(idx.Include) > 0 {
				if err := doc.SetCreate(idxPath+".include", toAnySlice(idx.Include)); err != nil {
					return nil, fmt.Errorf("set %s.include: %w", idxPath, err)
				}
			}
		}

		// Unique constraints
		for _, uq := range t.Uniques {
			uqPath := tblPath + ".unique." + uq.Name
			if err := doc.NewTable(uqPath); err != nil {
				return nil, fmt.Errorf("create unique %s: %w", uqPath, err)
			}
			if err := doc.SetCreate(uqPath+".columns", toAnySlice(uq.Columns)); err != nil {
				return nil, fmt.Errorf("set %s.columns: %w", uqPath, err)
			}
		}

		// Check constraints
		for _, ck := range t.Checks {
			ckPath := tblPath + ".checks." + ck.Name
			if err := doc.NewTable(ckPath); err != nil {
				return nil, fmt.Errorf("create check %s: %w", ckPath, err)
			}
			if err := doc.SetCreate(ckPath+".expr", ck.Expr); err != nil {
				return nil, fmt.Errorf("set %s.expr: %w", ckPath, err)
			}
		}
	}

	return doc.Format(), nil
}

// setOpclass sets the opclass key on an index table. If all opclasses are the
// same, it uses a compact string form. Otherwise, it uses an inline table with
// per-column opclass values.
func setOpclass(doc *tomledit.DocumentNode, idxPath string, idx model.Index) error {
	// Check if all opclasses are the same -- use compact string form.
	allSame := true
	var singleVal string
	for _, v := range idx.Opclasses {
		if singleVal == "" {
			singleVal = v
		} else if v != singleVal {
			allSame = false
			break
		}
	}
	if allSame && singleVal != "" {
		return doc.SetCreate(idxPath+".opclass", singleVal)
	}
	// Per-column inline table. Build a map[string]any preserving column order
	// by iterating idx.Columns (the ordered source).
	m := make(map[string]any, len(idx.Opclasses))
	for _, col := range idx.Columns {
		if oc, ok := idx.Opclasses[col]; ok {
			m[col] = oc
		}
	}
	return doc.SetCreate(idxPath+".opclass", m)
}

// toAnySlice converts a []string to []any for go-toml-edit's valueToNode.
func toAnySlice(ss []string) []any {
	result := make([]any, len(ss))
	for i, s := range ss {
		result[i] = s
	}
	return result
}


// indexColumnsWithDir returns column strings with " DESC" appended for
// columns that have desc=true. ASC columns are returned bare (PostgreSQL default).
func indexColumnsWithDir(columns []string, desc []bool) []string {
	result := make([]string, len(columns))
	for i, col := range columns {
		if i < len(desc) && desc[i] {
			result[i] = col + " DESC"
		} else {
			result[i] = col
		}
	}
	return result
}
