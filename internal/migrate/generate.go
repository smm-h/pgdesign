package migrate

import (
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/risk"
)

// GenerateMigration converts a SchemaDiff into a Migration with DDL/DML ops
// and safety diagnostics. The desired schema is used to look up full table
// definitions for create_table ops.
func GenerateMigration(d *diff.SchemaDiff, desired *model.Schema, version string) (*Migration, []diagnostic.Diagnostic) {
	m := &Migration{
		Version:     version,
		Description: generateDescription(d),
	}
	var diags []diagnostic.Diagnostic

	// Phase 1: Creates (enums first, then tables).
	for _, enumName := range d.EnumsAdded {
		schema, name := splitQualifiedName(enumName)
		var values []string
		for _, e := range desired.Enums {
			if enumKey(e) == enumName {
				values = e.Values
				break
			}
		}
		op := DDLOp{
			Op:     "create_enum",
			Name:   name,
			Schema: schema,
			Values: values,
			Down: &DownOp{
				Ops: []DDLOp{{Op: "drop_enum", Name: name, Schema: schema}},
			},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpType("create_enum"), risk.OpContext{})...)
	}

	for _, enumDiff := range d.EnumsChanged {
		for _, val := range enumDiff.ValuesAdded {
			schema, name := splitQualifiedName(enumDiff.Name)
			op := DDLOp{
				Op:     "alter_enum_add_value",
				Name:   name,
				Schema: schema,
				Values: []string{val},
				Down:   &DownOp{Irreversible: true},
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpAlterEnumAddValue, risk.OpContext{})...)
		}
	}

	for _, tableName := range d.TablesAdded {
		table := findTable(desired, tableName)
		op := DDLOp{
			Op:       "create_table",
			Table:    tableName,
			Comment:  tableComment(table),
			PK:       tablePK(table),
			TableDef: table,
			Down: &DownOp{
				Ops: []DDLOp{{Op: "drop_table", Table: tableName}},
			},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpCreateTable, risk.OpContext{})...)

		// Add FKs, indexes, uniques, checks for new tables.
		if table != nil {
			for _, fk := range table.FKs {
				fkOp := makeFKOp(tableName, fk)
				m.DDLOps = append(m.DDLOps, fkOp)
				diags = append(diags, classifyOp(fkOp, risk.OpAddFK, risk.OpContext{})...)
			}
			for _, idx := range table.Indexes {
				idxOp := makeIndexOp(tableName, idx)
				m.DDLOps = append(m.DDLOps, idxOp)
				opType := risk.OpCreateIndex
				if strings.Contains(idxOp.Op, "concurrently") {
					opType = risk.OpCreateIndexConcurrently
				}
				diags = append(diags, classifyOp(idxOp, opType, risk.OpContext{})...)
			}
			for _, uq := range table.Uniques {
				uqOp := makeUniqueOp(tableName, uq)
				m.DDLOps = append(m.DDLOps, uqOp)
				diags = append(diags, classifyOp(uqOp, risk.OpAddUnique, risk.OpContext{})...)
			}
			for _, ck := range table.Checks {
				ckOp := makeCheckOp(tableName, ck)
				m.DDLOps = append(m.DDLOps, ckOp)
				diags = append(diags, classifyOp(ckOp, risk.OpAddCheck, risk.OpContext{})...)
			}
		}
	}

	// Phase 2: Table changes (add columns, alter columns, add constraints).
	for _, td := range d.TablesChanged {
		// Added columns.
		for _, col := range td.ColumnsAdded {
			ctx := risk.OpContext{
				IsNullable: !col.NotNull,
				HasDefault: col.Default != "" || col.DefaultExpr != "",
			}
			op := DDLOp{
				Op:      "add_column",
				Table:   td.Name,
				Column:  col.Name,
				Type:    col.PGType,
				NotNull: col.NotNull,
				Down: &DownOp{
					Ops: []DDLOp{{Op: "drop_column", Table: td.Name, Column: col.Name}},
				},
			}
			if col.Default != "" {
				op.Default = col.Default
			} else if col.DefaultExpr != "" {
				op.Default = col.DefaultExpr
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpAddColumn, ctx)...)
		}

		// Changed columns.
		for _, cc := range td.ColumnsChanged {
			if cc.TypeChanged != nil {
				ctx := risk.OpContext{}
				op := DDLOp{
					Op:     "alter_column_type",
					Table:  td.Name,
					Column: cc.Name,
					Type:   cc.TypeChanged[1], // new type
					Down:   &DownOp{Irreversible: true},
				}
				m.DDLOps = append(m.DDLOps, op)
				diags = append(diags, classifyOp(op, risk.OpAlterColumnType, ctx)...)
			}
			if cc.NullableChanged != nil {
				if cc.NullableChanged[1] {
					// Becoming NOT NULL.
					op := DDLOp{
						Op:     "set_not_null",
						Table:  td.Name,
						Column: cc.Name,
						Down: &DownOp{
							Ops: []DDLOp{{Op: "drop_not_null", Table: td.Name, Column: cc.Name}},
						},
					}
					m.DDLOps = append(m.DDLOps, op)
					diags = append(diags, classifyOp(op, risk.OpSetNotNull, risk.OpContext{})...)
				} else {
					// Becoming nullable.
					op := DDLOp{
						Op:     "drop_not_null",
						Table:  td.Name,
						Column: cc.Name,
						Down: &DownOp{
							Ops: []DDLOp{{Op: "set_not_null", Table: td.Name, Column: cc.Name}},
						},
					}
					m.DDLOps = append(m.DDLOps, op)
					diags = append(diags, classifyOp(op, risk.OpDropNotNull, risk.OpContext{})...)
				}
			}
			if cc.DefaultChanged != nil {
				if cc.DefaultChanged[1] == "" {
					op := DDLOp{
						Op:     "drop_column_default",
						Table:  td.Name,
						Column: cc.Name,
						Down: &DownOp{
							Ops: []DDLOp{{
								Op:      "alter_column_default",
								Table:   td.Name,
								Column:  cc.Name,
								Default: cc.DefaultChanged[0],
							}},
						},
					}
					m.DDLOps = append(m.DDLOps, op)
				} else {
					op := DDLOp{
						Op:      "alter_column_default",
						Table:   td.Name,
						Column:  cc.Name,
						Default: cc.DefaultChanged[1],
						Down: &DownOp{
							Ops: []DDLOp{{
								Op:      "alter_column_default",
								Table:   td.Name,
								Column:  cc.Name,
								Default: cc.DefaultChanged[0],
							}},
						},
					}
					m.DDLOps = append(m.DDLOps, op)
				}
			}
		}

		// Added FKs.
		for _, fk := range td.FKsAdded {
			fkOp := makeFKOp(td.Name, fk)
			m.DDLOps = append(m.DDLOps, fkOp)
			diags = append(diags, classifyOp(fkOp, risk.OpAddFK, risk.OpContext{})...)
		}

		// Removed FKs.
		for _, fkName := range td.FKsRemoved {
			op := DDLOp{
				Op:    "drop_fk",
				Table: td.Name,
				Name:  fkName,
				Down:  &DownOp{Irreversible: true},
			}
			m.DDLOps = append(m.DDLOps, op)
		}

		// Added indexes.
		for _, idx := range td.IndexesAdded {
			idxOp := makeIndexOp(td.Name, idx)
			m.DDLOps = append(m.DDLOps, idxOp)
			opType := risk.OpCreateIndex
			if strings.Contains(idxOp.Op, "concurrently") {
				opType = risk.OpCreateIndexConcurrently
			}
			diags = append(diags, classifyOp(idxOp, opType, risk.OpContext{})...)
		}

		// Removed indexes.
		for _, idxName := range td.IndexesRemoved {
			op := DDLOp{
				Op:    "drop_index",
				Table: td.Name,
				Name:  idxName,
				Down:  &DownOp{Irreversible: true},
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpDropIndex, risk.OpContext{})...)
		}

		// Added uniques.
		for _, uq := range td.UniquesAdded {
			uqOp := makeUniqueOp(td.Name, uq)
			m.DDLOps = append(m.DDLOps, uqOp)
			diags = append(diags, classifyOp(uqOp, risk.OpAddUnique, risk.OpContext{})...)
		}

		// Removed uniques.
		for _, uqName := range td.UniquesRemoved {
			op := DDLOp{
				Op:    "drop_unique",
				Table: td.Name,
				Name:  uqName,
				Down:  &DownOp{Irreversible: true},
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpDropUnique, risk.OpContext{})...)
		}

		// Added checks.
		for _, ck := range td.ChecksAdded {
			ckOp := makeCheckOp(td.Name, ck)
			m.DDLOps = append(m.DDLOps, ckOp)
			diags = append(diags, classifyOp(ckOp, risk.OpAddCheck, risk.OpContext{})...)
		}

		// Removed checks.
		for _, ckName := range td.ChecksRemoved {
			op := DDLOp{
				Op:    "drop_check",
				Table: td.Name,
				Name:  ckName,
				Down:  &DownOp{Irreversible: true},
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpDropCheck, risk.OpContext{})...)
		}

		// Removed columns.
		for _, colName := range td.ColumnsRemoved {
			op := DDLOp{
				Op:     "drop_column",
				Table:  td.Name,
				Column: colName,
				Down:   &DownOp{Irreversible: true},
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpDropColumn, risk.OpContext{})...)
		}
	}

	// Phase 3: Drops (enums last, tables before enums).
	for _, tableName := range d.TablesRemoved {
		op := DDLOp{
			Op:    "drop_table",
			Table: tableName,
			Down:  &DownOp{Irreversible: true},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpDropTable, risk.OpContext{})...)
	}

	for _, enumName := range d.EnumsRemoved {
		schema, name := splitQualifiedName(enumName)
		op := DDLOp{
			Op:     "drop_enum",
			Name:   name,
			Schema: schema,
			Down:   &DownOp{Irreversible: true},
		}
		m.DDLOps = append(m.DDLOps, op)
	}

	return m, diags
}

func makeFKOp(tableName string, fk model.FK) DDLOp {
	refTable := fk.RefTable
	if fk.RefSchema != "" && fk.RefSchema != "public" {
		refTable = fk.RefSchema + "." + fk.RefTable
	}
	return DDLOp{
		Op:       "add_fk",
		Table:    tableName,
		Name:     fk.Name,
		Columns:  fk.Columns,
		RefTable: refTable,
		RefCols:  fk.RefColumns,
		OnDelete: fk.OnDelete,
		Down: &DownOp{
			Ops: []DDLOp{{Op: "drop_fk", Table: tableName, Name: fk.Name}},
		},
	}
}

func makeIndexOp(tableName string, idx model.Index) DDLOp {
	return DDLOp{
		Op:        "create_index",
		Table:     tableName,
		Name:      idx.Name,
		Columns:   idx.Columns,
		Method:    idx.Method,
		Opclasses: idx.Opclasses,
		Where:     idx.Where,
		Include:   idx.Include,
		Down: &DownOp{
			Ops: []DDLOp{{Op: "drop_index", Table: tableName, Name: idx.Name}},
		},
	}
}

func makeUniqueOp(tableName string, uq model.UniqueConstraint) DDLOp {
	return DDLOp{
		Op:      "add_unique",
		Table:   tableName,
		Name:    uq.Name,
		Columns: uq.Columns,
		Down: &DownOp{
			Ops: []DDLOp{{Op: "drop_unique", Table: tableName, Name: uq.Name}},
		},
	}
}

func makeCheckOp(tableName string, ck model.CheckConstraint) DDLOp {
	return DDLOp{
		Op:   "add_check",
		Table: tableName,
		Name:  ck.Name,
		Expr:  ck.Expr,
		Down: &DownOp{
			Ops: []DDLOp{{Op: "drop_check", Table: tableName, Name: ck.Name}},
		},
	}
}

func classifyOp(op DDLOp, opType risk.OpType, ctx risk.OpContext) []diagnostic.Diagnostic {
	c := risk.Classify(opType, ctx)
	if c.RiskLevel == risk.Safe {
		return nil
	}

	sev := diagnostic.Warning
	if c.RiskLevel == risk.Dangerous {
		sev = diagnostic.Error
	}

	msg := fmt.Sprintf("%s on %s", op.Op, opTarget(op))
	if c.DataLoss {
		msg += " (data loss possible)"
	}

	d := diagnostic.Diagnostic{
		Severity: sev,
		Code:     "MIGRATE_RISK",
		Table:    opTarget(op),
		Message:  msg,
	}
	if c.Suggestion != "" {
		d.Suggestion = c.Suggestion
	}
	return []diagnostic.Diagnostic{d}
}

func opTarget(op DDLOp) string {
	if op.Table != "" {
		return op.Table
	}
	if op.Name != "" {
		return op.Name
	}
	return "unknown"
}

func findTable(schema *model.Schema, qualifiedName string) *model.Table {
	s, name := splitQualifiedName(qualifiedName)
	t := schema.TableByName(s, name)
	if t != nil {
		return t
	}
	// Also try with empty schema (for "public" tables stored without schema).
	return schema.TableByName("", qualifiedName)
}

func enumKey(e model.Enum) string {
	if e.Schema == "" || e.Schema == "public" {
		return e.Name
	}
	return e.Schema + "." + e.Name
}

func tableComment(t *model.Table) string {
	if t == nil {
		return ""
	}
	return t.Comment
}

func tablePK(t *model.Table) []string {
	if t == nil {
		return nil
	}
	return t.PK
}

func generateDescription(d *diff.SchemaDiff) string {
	var parts []string
	if len(d.TablesAdded) > 0 {
		parts = append(parts, fmt.Sprintf("Add %s", strings.Join(d.TablesAdded, ", ")))
	}
	if len(d.TablesRemoved) > 0 {
		parts = append(parts, fmt.Sprintf("Drop %s", strings.Join(d.TablesRemoved, ", ")))
	}
	for _, td := range d.TablesChanged {
		var changes []string
		if len(td.ColumnsAdded) > 0 {
			names := make([]string, len(td.ColumnsAdded))
			for i, c := range td.ColumnsAdded {
				names[i] = c.Name
			}
			changes = append(changes, fmt.Sprintf("add %s", strings.Join(names, ", ")))
		}
		if len(td.ColumnsRemoved) > 0 {
			changes = append(changes, fmt.Sprintf("drop %s", strings.Join(td.ColumnsRemoved, ", ")))
		}
		if len(td.ColumnsChanged) > 0 {
			names := make([]string, len(td.ColumnsChanged))
			for i, c := range td.ColumnsChanged {
				names[i] = c.Name
			}
			changes = append(changes, fmt.Sprintf("alter %s", strings.Join(names, ", ")))
		}
		if len(changes) > 0 {
			parts = append(parts, fmt.Sprintf("%s: %s", td.Name, strings.Join(changes, "; ")))
		}
	}
	if len(d.EnumsAdded) > 0 {
		parts = append(parts, fmt.Sprintf("Add enum %s", strings.Join(d.EnumsAdded, ", ")))
	}
	if len(parts) == 0 {
		return "Schema migration"
	}
	return strings.Join(parts, ". ")
}
