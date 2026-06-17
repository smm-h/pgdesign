package migrate

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/risk"
)

// GenerateMigration converts a SchemaDiff into a Migration with DDL/DML ops
// and safety diagnostics. The desired schema is used to look up full table
// definitions for create_table ops. tableStats provides estimated row counts
// from pg_stat_user_tables (nil when --db is not available or stats are
// unavailable). largeFKThreshold is the row count above which E300 is emitted
// for ADD CONSTRAINT without NOT VALID; pass 0 to use the default of 10000.
// expandContractThreshold is the row count above which set_not_null ops are
// decomposed into a DML backfill step followed by set_not_null; pass 0 to use
// the default of 10_000_000.
func GenerateMigration(d *diff.SchemaDiff, desired *model.Schema, version string, tableStats TableStats, largeFKThreshold int64, expandContractThreshold int64, extReg *extregistry.Registry) (*Migration, []diagnostic.Diagnostic) {
	if largeFKThreshold <= 0 {
		largeFKThreshold = 10_000
	}
	if expandContractThreshold <= 0 {
		expandContractThreshold = 10_000_000
	}
	m := &Migration{
		Version:     version,
		Description: generateDescription(d),
	}
	var diags []diagnostic.Diagnostic

	// tableCtx builds an OpContext with EstimatedRows and PGVersion populated
	// from tableStats and the desired schema's PGVersion.
	tableCtx := func(tableName string) risk.OpContext {
		return risk.OpContext{
			EstimatedRows: lookupRows(tableStats, tableName),
			PGVersion:     desired.PGVersion,
		}
	}

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
		diags = append(diags, classifyOp(op, risk.OpType("create_enum"), risk.OpContext{PGVersion: desired.PGVersion})...)
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
			diags = append(diags, classifyOp(op, risk.OpAlterEnumAddValue, risk.OpContext{PGVersion: desired.PGVersion})...)
		}
	}

	for _, tableName := range d.TablesAdded {
		table := findTable(desired, tableName)
		ctx := tableCtx(tableName)
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
		diags = append(diags, classifyOp(op, risk.OpCreateTable, ctx)...)

		// Add FKs, indexes, uniques, checks for new tables.
		if table != nil {
			for _, fk := range table.FKs {
				fkOp := makeFKOp(tableName, fk)
				m.DDLOps = append(m.DDLOps, fkOp)
				diags = append(diags, classifyOp(fkOp, risk.OpAddFK, ctx)...)
				diags = append(diags, checkE300(fkOp, ctx, largeFKThreshold)...)
			}
			for _, idx := range table.Indexes {
				idxOp := makeIndexOp(tableName, idx)
				m.DDLOps = append(m.DDLOps, idxOp)
				opType := risk.OpCreateIndex
				if strings.Contains(idxOp.Op, "concurrently") {
					opType = risk.OpCreateIndexConcurrently
				}
				diags = append(diags, classifyOp(idxOp, opType, ctx)...)
			}
			for _, uq := range table.Uniques {
				uqOp := makeUniqueOp(tableName, uq)
				m.DDLOps = append(m.DDLOps, uqOp)
				diags = append(diags, classifyOp(uqOp, risk.OpAddUnique, ctx)...)
			}
			for _, ck := range table.Checks {
				ckOp := makeCheckOp(tableName, ck)
				m.DDLOps = append(m.DDLOps, ckOp)
				diags = append(diags, classifyOp(ckOp, risk.OpAddCheck, ctx)...)
			}
			for _, exc := range table.Exclusions {
				excOp := makeExclusionOp(tableName, exc)
				m.DDLOps = append(m.DDLOps, excOp)
				diags = append(diags, classifyOp(excOp, risk.OpAddExclusion, ctx)...)
			}
		}
	}

	// Views (created after tables since they may reference tables).
	for _, viewName := range d.ViewsAdded {
		view := findView(desired, viewName)
		schema, _ := splitQualifiedName(viewName)
		op := DDLOp{
			Op:      "create_view",
			Name:    viewName,
			Schema:  schema,
			ViewDef: view,
			Down: &DownOp{
				Ops: []DDLOp{{Op: "drop_view", Name: viewName}},
			},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpCreateView, risk.OpContext{})...)
	}

	// Materialized views added.
	for _, mvName := range d.MaterializedViewsAdded {
		mv := findMaterializedView(desired, mvName)
		schema, _ := splitQualifiedName(mvName)
		op := DDLOp{
			Op:                  "create_materialized_view",
			Name:                mvName,
			Schema:              schema,
			MaterializedViewDef: mv,
			Down: &DownOp{
				Ops: []DDLOp{{Op: "drop_materialized_view", Name: mvName}},
			},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpCreateMaterializedView, risk.OpContext{})...)

		// Create indexes on the materialized view.
		if mv != nil {
			for _, idx := range mv.Indexes {
				idxOp := makeIndexOp(mvName, idx)
				m.DDLOps = append(m.DDLOps, idxOp)
			}
		}
	}

	// Sequences added.
	for _, seqName := range d.SequencesAdded {
		seq := findSequence(desired, seqName)
		schema, name := splitQualifiedName(seqName)
		op := DDLOp{
			Op:          "create_sequence",
			Name:        name,
			Schema:      schema,
			SequenceDef: seq,
			Down: &DownOp{
				Ops: []DDLOp{{Op: "drop_sequence", Name: name, Schema: schema}},
			},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpCreateSequence, risk.OpContext{PGVersion: desired.PGVersion})...)
	}

	// Phase 2: Table changes (add columns, alter columns, add constraints).
	for _, td := range d.TablesChanged {
		ctx := tableCtx(td.Name)

		// Added columns.
		for _, col := range td.ColumnsAdded {
			colCtx := ctx
			colCtx.IsNullable = !col.NotNull
			colCtx.HasDefault = col.Default != nil || col.DefaultExpr != ""
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
			if col.Generated != "" {
				op.Generated = col.Generated
				op.Stored = col.Stored
				op.PGVersion = desired.PGVersion
			} else if col.Default != nil {
				op.Default = *col.Default
			} else if col.DefaultExpr != "" {
				op.Default = col.DefaultExpr
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpAddColumn, colCtx)...)
		}

		// Changed columns.
		for _, cc := range td.ColumnsChanged {
			if cc.TypeChanged != nil {
				op := DDLOp{
					Op:     "alter_column_type",
					Table:  td.Name,
					Column: cc.Name,
					Type:   cc.TypeChanged[1], // new type
					Down:   &DownOp{Irreversible: true},
				}
				m.DDLOps = append(m.DDLOps, op)
				diags = append(diags, classifyOp(op, risk.OpAlterColumnType, ctx)...)

				// Type narrowing warning for large tables.
				if ctx.EstimatedRows > expandContractThreshold && !diff.IsWidening(cc.TypeChanged[0], cc.TypeChanged[1]) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity:   diagnostic.Warning,
						Code:       "EXPAND_CONTRACT_TYPE_NARROW",
						Table:      td.Name,
						Column:     cc.Name,
						Message:    fmt.Sprintf("Type narrowing on %s.%s (%s -> %s) on table with %d rows may require expand-contract migration", td.Name, cc.Name, cc.TypeChanged[0], cc.TypeChanged[1], ctx.EstimatedRows),
						Suggestion: "Consider an expand-contract approach: add new column, backfill, swap, drop old column",
					})
				}
			}
			if cc.NullableChanged != nil {
				if cc.NullableChanged[1] {
					// Becoming NOT NULL.
					if ctx.EstimatedRows > expandContractThreshold {
						// Decompose into backfill DML + set_not_null for large tables.
						backfillSQL := buildBackfillSQL(td.Name, cc.Name, desired)
						dmlOp := DMLOp{
							Op:   "backfill",
							SQL:  backfillSQL,
							Down: &DownOp{Irreversible: true},
						}
						m.DMLOps = append(m.DMLOps, dmlOp)
					}
					op := DDLOp{
						Op:     "set_not_null",
						Table:  td.Name,
						Column: cc.Name,
						Down: &DownOp{
							Ops: []DDLOp{{Op: "drop_not_null", Table: td.Name, Column: cc.Name}},
						},
					}
					m.DDLOps = append(m.DDLOps, op)
					diags = append(diags, classifyOp(op, risk.OpSetNotNull, ctx)...)
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
					diags = append(diags, classifyOp(op, risk.OpDropNotNull, ctx)...)
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
			if cc.ArrayChanged != nil {
				// Look up the column's base PGType from the desired schema.
				targetType := ""
				if table := findTable(desired, td.Name); table != nil {
					for _, col := range table.Columns {
						if col.Name == cc.Name {
							targetType = col.PGType
							if col.Array {
								targetType += "[]"
							}
							break
						}
					}
				}
				if targetType != "" {
					op := DDLOp{
						Op:     "alter_column_type",
						Table:  td.Name,
						Column: cc.Name,
						Type:   targetType,
						Down:   &DownOp{Irreversible: true},
					}
					m.DDLOps = append(m.DDLOps, op)
					diags = append(diags, classifyOp(op, risk.OpAlterColumnType, ctx)...)
				}
			}
			if cc.CollationChanged != nil {
				// PostgreSQL requires ALTER COLUMN TYPE ... COLLATE for collation changes.
				// Look up the full type from the desired schema.
				targetType := ""
				targetCollation := ""
				if table := findTable(desired, td.Name); table != nil {
					for _, col := range table.Columns {
						if col.Name == cc.Name {
							targetType = col.PGType
							if col.Array {
								targetType += "[]"
							}
							targetCollation = col.Collation
							break
						}
					}
				}
				if targetType != "" {
					op := DDLOp{
						Op:        "alter_column_type",
						Table:     td.Name,
						Column:    cc.Name,
						Type:      targetType,
						Collation: targetCollation,
						Down:      &DownOp{Irreversible: true},
					}
					m.DDLOps = append(m.DDLOps, op)
					diags = append(diags, classifyOp(op, risk.OpAlterColumnType, ctx)...)
				}
			}
			if cc.StatisticsChanged != nil {
				op := DDLOp{
					Op:         "set_statistics",
					Table:      td.Name,
					Column:     cc.Name,
					Statistics: cc.StatisticsChanged[1], // new value
					Down:       &DownOp{Irreversible: true},
				}
				m.DDLOps = append(m.DDLOps, op)
				// Statistics changes are safe -- no risk classification needed.
			}
		}

		// Added FKs.
		for _, fk := range td.FKsAdded {
			fkOp := makeFKOp(td.Name, fk)
			m.DDLOps = append(m.DDLOps, fkOp)
			diags = append(diags, classifyOp(fkOp, risk.OpAddFK, ctx)...)
			diags = append(diags, checkE300(fkOp, ctx, largeFKThreshold)...)
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
			diags = append(diags, classifyOp(idxOp, opType, ctx)...)
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
			diags = append(diags, classifyOp(op, risk.OpDropIndex, ctx)...)
		}

		// Changed indexes.
		for _, ic := range td.IndexesChanged {
			// When only WITH storage parameters changed and the index method
			// is a PostgreSQL builtin (btree, gin, gist, brin, hash), use
			// ALTER INDEX SET instead of DROP+CREATE. Extension methods
			// (e.g., hnsw from pgvector) may need a full rebuild for
			// parameter changes.
			if onlyWithChanged(ic.Old, ic.New) && extReg != nil {
				method := ic.New.Method
				if method == "" {
					method = "btree" // PostgreSQL default
				}
				_, isExtension := extReg.RequiredExtensionForMethod(method)
				if !isExtension {
					// Builtin method: ALTER INDEX SET is safe.
					op := DDLOp{
						Op:    "alter_index_set",
						Table: td.Name,
						Name:  ic.New.Name,
						With:  ic.New.With,
						Down: &DownOp{
							Ops: []DDLOp{{
								Op:    "alter_index_set",
								Table: td.Name,
								Name:  ic.Old.Name,
								With:  ic.Old.With,
							}},
						},
					}
					m.DDLOps = append(m.DDLOps, op)
					diags = append(diags, classifyOp(op, risk.OpAlterIndexSet, ctx)...)
					continue
				}
			}

			// Default: DROP + CREATE for extension methods or structural changes.
			dropOp := DDLOp{
				Op:    "drop_index",
				Table: td.Name,
				Name:  ic.Old.Name,
			}
			m.DDLOps = append(m.DDLOps, dropOp)
			diags = append(diags, classifyOp(dropOp, risk.OpDropIndex, ctx)...)

			createOp := makeIndexOp(td.Name, ic.New)
			m.DDLOps = append(m.DDLOps, createOp)
			opType := risk.OpCreateIndex
			if strings.Contains(createOp.Op, "concurrently") {
				opType = risk.OpCreateIndexConcurrently
			}
			diags = append(diags, classifyOp(createOp, opType, ctx)...)
		}

		// Added uniques.
		for _, uq := range td.UniquesAdded {
			uqOp := makeUniqueOp(td.Name, uq)
			m.DDLOps = append(m.DDLOps, uqOp)
			diags = append(diags, classifyOp(uqOp, risk.OpAddUnique, ctx)...)
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
			diags = append(diags, classifyOp(op, risk.OpDropUnique, ctx)...)
		}

		// Added checks.
		for _, ck := range td.ChecksAdded {
			ckOp := makeCheckOp(td.Name, ck)
			m.DDLOps = append(m.DDLOps, ckOp)
			diags = append(diags, classifyOp(ckOp, risk.OpAddCheck, ctx)...)
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
			diags = append(diags, classifyOp(op, risk.OpDropCheck, ctx)...)
		}

		// Added exclusions.
		for _, exc := range td.ExclusionsAdded {
			excOp := makeExclusionOp(td.Name, exc)
			m.DDLOps = append(m.DDLOps, excOp)
			diags = append(diags, classifyOp(excOp, risk.OpAddExclusion, ctx)...)
		}

		// Removed exclusions.
		for _, excName := range td.ExclusionsRemoved {
			op := DDLOp{
				Op:    "drop_exclusion",
				Table: td.Name,
				Name:  excName,
				Down:  &DownOp{Irreversible: true},
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpDropExclusion, ctx)...)
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
			diags = append(diags, classifyOp(op, risk.OpDropColumn, ctx)...)
		}

		// Partition changes.
		if td.PartitioningChanged != nil {
			pd := td.PartitioningChanged

			// Strategy change: emit warning, not yet supported.
			if pd.StrategyChanged != nil {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Warning,
					Code:     "PARTITION_STRATEGY_CHANGE",
					Table:    td.Name,
					Message:  fmt.Sprintf("partition strategy change on %s (%s -> %s) requires table rebuild (not yet supported)", td.Name, pd.StrategyChanged[0], pd.StrategyChanged[1]),
				})
			}

			// Added partition children.
			parentTable := findTable(desired, td.Name)
			for _, childKey := range pd.ChildrenAdded {
				childSpec := findPartitionChild(parentTable, childKey)
				if childSpec == nil {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Warning,
						Code:     "PARTITION_CHILD_NOT_FOUND",
						Table:    td.Name,
						Message:  fmt.Sprintf("partition child %q not found in desired schema for %s", childKey, td.Name),
					})
					continue
				}
				childName := partitionChildQualifiedName(td.Name, childSpec)
				op := DDLOp{
					Op:                 "create_partition",
					Table:              td.Name,
					ParentTable:        td.Name,
					PartitionChildSpec: childSpec,
					Down: &DownOp{
						Ops: []DDLOp{{Op: "drop_table", Table: childName}},
					},
				}
				m.DDLOps = append(m.DDLOps, op)
				diags = append(diags, classifyOp(op, risk.OpCreateTable, ctx)...)
			}

			// Removed partition children.
			for _, childKey := range pd.ChildrenRemoved {
				childName := partitionChildNameFromKey(td.Name, childKey)
				op := DDLOp{
					Op:    "drop_table",
					Table: childName,
					Down:  &DownOp{Irreversible: true},
				}
				m.DDLOps = append(m.DDLOps, op)
				diags = append(diags, classifyOp(op, risk.OpDropTable, ctx)...)
			}
		}

		// AppendOnly changes.
		if td.AppendOnlyChanged != nil {
			if td.AppendOnlyChanged[1] {
				// false -> true: add shared function (if needed) + trigger.
				op := DDLOp{
					Op:    "create_function",
					Table: td.Name,
					Name:  "pgdesign_deny_mutation",
					Down: &DownOp{
						Ops: []DDLOp{{Op: "drop_function", Table: td.Name, Name: "pgdesign_deny_mutation"}},
					},
				}
				m.DDLOps = append(m.DDLOps, op)
				triggerOp := DDLOp{
					Op:    "create_trigger",
					Table: td.Name,
					Name:  "deny_mutation",
					Down: &DownOp{
						Ops: []DDLOp{{Op: "drop_trigger", Table: td.Name, Name: "deny_mutation"}},
					},
				}
				m.DDLOps = append(m.DDLOps, triggerOp)
			} else {
				// true -> false: drop trigger, possibly drop shared function.
				triggerOp := DDLOp{
					Op:    "drop_trigger",
					Table: td.Name,
					Name:  "deny_mutation",
					Down: &DownOp{
						Ops: []DDLOp{{Op: "create_trigger", Table: td.Name, Name: "deny_mutation"}},
					},
				}
				m.DDLOps = append(m.DDLOps, triggerOp)
				// Check if any other table in the desired schema still has append_only.
				// If not, drop the shared function.
				otherAppendOnly := false
				if desired != nil {
					for _, t := range desired.Tables {
						if t.AppendOnly && migrateTableKey(t) != td.Name {
							otherAppendOnly = true
							break
						}
					}
				}
				if !otherAppendOnly {
					op := DDLOp{
						Op:    "drop_function",
						Table: td.Name,
						Name:  "pgdesign_deny_mutation",
						Down: &DownOp{
							Ops: []DDLOp{{Op: "create_function", Table: td.Name, Name: "pgdesign_deny_mutation"}},
						},
					}
					m.DDLOps = append(m.DDLOps, op)
				}
			}
		}
	}

	// View changes.
	for _, vd := range d.ViewsChanged {
		if vd.QueryChanged != nil {
			// Query changed: CREATE OR REPLACE VIEW.
			view := findView(desired, vd.Name)
			// Build an old view for the down op.
			var oldView *model.View
			if view != nil {
				oldView = &model.View{
					Name:   view.Name,
					Schema: view.Schema,
					Query:  vd.QueryChanged[0],
				}
			}
			op := DDLOp{
				Op:      "create_or_replace_view",
				Name:    vd.Name,
				ViewDef: view,
				Down: &DownOp{
					Ops: []DDLOp{{Op: "create_or_replace_view", Name: vd.Name, ViewDef: oldView}},
				},
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpCreateOrReplaceView, risk.OpContext{})...)
		}
		// Comment changes on views don't need separate migration ops in PostgreSQL;
		// COMMENT ON VIEW would require separate handling. For now, comment changes
		// are tracked in the diff but don't generate DDL.
	}

	// Materialized view changes.
	for _, mvd := range d.MaterializedViewsChanged {
		if mvd.QueryChanged != nil || mvd.WithDataChanged != nil {
			// Query or WITH DATA changed: must DROP + CREATE (no ALTER for matviews).
			mv := findMaterializedView(desired, mvd.Name)
			op := DDLOp{
				Op:   "drop_materialized_view",
				Name: mvd.Name,
				Down: &DownOp{Irreversible: true},
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpDropMaterializedView, risk.OpContext{})...)
			op = DDLOp{
				Op:                  "create_materialized_view",
				Name:                mvd.Name,
				MaterializedViewDef: mv,
				Down: &DownOp{
					Ops: []DDLOp{{Op: "drop_materialized_view", Name: mvd.Name}},
				},
			}
			m.DDLOps = append(m.DDLOps, op)
			diags = append(diags, classifyOp(op, risk.OpCreateMaterializedView, risk.OpContext{})...)

			// Recreate all indexes after recreating the matview.
			if mv != nil {
				for _, idx := range mv.Indexes {
					idxOp := makeIndexOp(mvd.Name, idx)
					m.DDLOps = append(m.DDLOps, idxOp)
				}
			}
		} else {
			// Only index changes (query/with_data unchanged).
			for _, idx := range mvd.IndexesAdded {
				idxOp := makeIndexOp(mvd.Name, idx)
				m.DDLOps = append(m.DDLOps, idxOp)
			}
			for _, idxName := range mvd.IndexesRemoved {
				op := DDLOp{
					Op:    "drop_index",
					Table: mvd.Name,
					Name:  idxName,
					Down:  &DownOp{Irreversible: true},
				}
				m.DDLOps = append(m.DDLOps, op)
			}
			for _, ic := range mvd.IndexesChanged {
				if onlyWithChanged(ic.Old, ic.New) && extReg != nil {
					method := ic.New.Method
					if method == "" {
						method = "btree"
					}
					_, isExtension := extReg.RequiredExtensionForMethod(method)
					if !isExtension {
						op := DDLOp{
							Op:    "alter_index_set",
							Table: mvd.Name,
							Name:  ic.New.Name,
							With:  ic.New.With,
							Down: &DownOp{
								Ops: []DDLOp{{
									Op:    "alter_index_set",
									Table: mvd.Name,
									Name:  ic.Old.Name,
									With:  ic.Old.With,
								}},
							},
						}
						m.DDLOps = append(m.DDLOps, op)
						continue
					}
				}
				// Drop old + create new index.
				m.DDLOps = append(m.DDLOps, DDLOp{
					Op:    "drop_index",
					Table: mvd.Name,
					Name:  ic.Name,
					Down:  &DownOp{Irreversible: true},
				})
				idxOp := makeIndexOp(mvd.Name, ic.New)
				m.DDLOps = append(m.DDLOps, idxOp)
			}
		}
	}

	// Sequence changes.
	for _, sd := range d.SequencesChanged {
		seq := findSequence(desired, sd.Name)
		schema, name := splitQualifiedName(sd.Name)
		op := DDLOp{
			Op:          "alter_sequence",
			Name:        name,
			Schema:      schema,
			SequenceDef: seq,
			Down:        &DownOp{Irreversible: true},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpAlterSequence, risk.OpContext{PGVersion: desired.PGVersion})...)
	}

	// Phase 3: Drops (enums last, tables before enums).
	for _, tableName := range d.TablesRemoved {
		ctx := tableCtx(tableName)
		op := DDLOp{
			Op:    "drop_table",
			Table: tableName,
			Down:  &DownOp{Irreversible: true},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpDropTable, ctx)...)
	}

	// Drop views (before dropping tables they may reference, though views on
	// dropped tables would already be broken).
	for _, viewName := range d.ViewsRemoved {
		op := DDLOp{
			Op:   "drop_view",
			Name: viewName,
			Down: &DownOp{Irreversible: true},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpDropView, risk.OpContext{})...)
	}

	// Drop materialized views.
	for _, mvName := range d.MaterializedViewsRemoved {
		op := DDLOp{
			Op:   "drop_materialized_view",
			Name: mvName,
			Down: &DownOp{Irreversible: true},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpDropMaterializedView, risk.OpContext{})...)
	}

	// Drop sequences.
	for _, seqName := range d.SequencesRemoved {
		op := DDLOp{
			Op:   "drop_sequence",
			Name: seqName,
			Down: &DownOp{Irreversible: true},
		}
		m.DDLOps = append(m.DDLOps, op)
		diags = append(diags, classifyOp(op, risk.OpDropSequence, risk.OpContext{})...)
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

// onlyWithChanged reports whether the only difference between old and new
// is the With storage parameters. All structural properties (columns, method,
// opclasses, where, include, unique, desc) must be identical.
func onlyWithChanged(old, new model.Index) bool {
	if old.Name != new.Name {
		return false
	}
	if !slices.Equal(old.Columns, new.Columns) {
		return false
	}
	if old.Method != new.Method {
		return false
	}
	if old.Where != new.Where {
		return false
	}
	if old.Unique != new.Unique {
		return false
	}
	if !slices.Equal(old.Include, new.Include) {
		return false
	}
	if !slices.Equal(old.Desc, new.Desc) {
		return false
	}
	if !maps.Equal(old.Opclasses, new.Opclasses) {
		return false
	}
	if !maps.Equal(old.Collations, new.Collations) {
		return false
	}
	// At this point, only With can differ.
	return !maps.Equal(old.With, new.With)
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
		Op:         "create_index",
		Table:      tableName,
		Name:       idx.Name,
		Columns:    idx.Columns,
		Desc:       idx.Desc,
		Method:     idx.Method,
		Opclasses:  idx.Opclasses,
		Collations: idx.Collations,
		Where:      idx.Where,
		Include:    idx.Include,
		With:       idx.With,
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

func makeExclusionOp(tableName string, exc model.ExclusionConstraint) DDLOp {
	cols := make([]string, len(exc.Elements))
	ops := make([]string, len(exc.Elements))
	for i, elem := range exc.Elements {
		cols[i] = elem.Column
		ops[i] = elem.Operator
	}
	return DDLOp{
		Op:                "add_exclusion",
		Table:             tableName,
		Name:              exc.Name,
		Columns:           cols,
		Method:            exc.Method,
		Where:             exc.Where,
		Operators:         ops,
		Deferrable:        exc.Deferrable,
		InitiallyDeferred: exc.InitiallyDeferred,
		Down: &DownOp{
			Ops: []DDLOp{{Op: "drop_exclusion", Table: tableName, Name: exc.Name}},
		},
	}
}

// checkE300 emits an E300 diagnostic when an add_fk op targets a table with
// more rows than the threshold. The diagnostic warns that ADD CONSTRAINT
// without NOT VALID will lock the table during validation.
func checkE300(op DDLOp, ctx risk.OpContext, threshold int64) []diagnostic.Diagnostic {
	if op.Op != "add_fk" || ctx.EstimatedRows <= threshold {
		return nil
	}
	return []diagnostic.Diagnostic{{
		Severity:   diagnostic.Warning,
		Code:       "E300",
		Table:      opTarget(op),
		Message:    fmt.Sprintf("ADD CONSTRAINT without NOT VALID on table with %d rows will lock the table; consider NOT VALID + VALIDATE CONSTRAINT", ctx.EstimatedRows),
		Suggestion: "Add with NOT VALID, then VALIDATE CONSTRAINT in a separate step",
	}}
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

func findView(schema *model.Schema, qualifiedName string) *model.View {
	s, name := splitQualifiedName(qualifiedName)
	for i := range schema.Views {
		v := &schema.Views[i]
		vSchema := v.Schema
		if vSchema == "" {
			vSchema = "public"
		}
		if vSchema == s && v.Name == name {
			return v
		}
	}
	return nil
}

func findMaterializedView(schema *model.Schema, qualifiedName string) *model.MaterializedView {
	s, name := splitQualifiedName(qualifiedName)
	for i := range schema.MaterializedViews {
		mv := &schema.MaterializedViews[i]
		mvSchema := mv.Schema
		if mvSchema == "" {
			mvSchema = "public"
		}
		if mvSchema == s && mv.Name == name {
			return mv
		}
	}
	return nil
}

func findSequence(schema *model.Schema, qualifiedName string) *model.Sequence {
	_, name := splitQualifiedName(qualifiedName)
	for i := range schema.Sequences {
		s := &schema.Sequences[i]
		if s.Name == name {
			return s
		}
	}
	return nil
}

func viewKey(v model.View) string {
	if v.Schema == "" || v.Schema == "public" {
		return v.Name
	}
	return v.Schema + "." + v.Name
}

func migrateTableKey(t model.Table) string {
	if t.Schema == "" || t.Schema == "public" {
		return t.Name
	}
	return t.Schema + "." + t.Name
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
	if len(d.ViewsAdded) > 0 {
		parts = append(parts, fmt.Sprintf("Add view %s", strings.Join(d.ViewsAdded, ", ")))
	}
	if len(d.ViewsRemoved) > 0 {
		parts = append(parts, fmt.Sprintf("Drop view %s", strings.Join(d.ViewsRemoved, ", ")))
	}
	if len(d.ViewsChanged) > 0 {
		names := make([]string, len(d.ViewsChanged))
		for i, vd := range d.ViewsChanged {
			names[i] = vd.Name
		}
		parts = append(parts, fmt.Sprintf("Alter view %s", strings.Join(names, ", ")))
	}
	if len(d.MaterializedViewsAdded) > 0 {
		parts = append(parts, fmt.Sprintf("Add materialized view %s", strings.Join(d.MaterializedViewsAdded, ", ")))
	}
	if len(d.MaterializedViewsRemoved) > 0 {
		parts = append(parts, fmt.Sprintf("Drop materialized view %s", strings.Join(d.MaterializedViewsRemoved, ", ")))
	}
	if len(d.MaterializedViewsChanged) > 0 {
		names := make([]string, len(d.MaterializedViewsChanged))
		for i, mvd := range d.MaterializedViewsChanged {
			names[i] = mvd.Name
		}
		parts = append(parts, fmt.Sprintf("Alter materialized view %s", strings.Join(names, ", ")))
	}
	if len(parts) == 0 {
		return "Schema migration"
	}
	return strings.Join(parts, ". ")
}

// partitionChildKey returns the key used to identify a partition child in diffs.
// Must match diff.partitionChildKey exactly.
func partitionChildKey(ps *model.PartitionSpec) string {
	if ps.Name != "" && ps.Bound != "" {
		return ps.Name + ":" + ps.Bound
	}
	if ps.Name != "" {
		return ps.Name
	}
	return ps.Bound
}

// findPartitionChild looks up a partition child spec by its diff key in the
// parent table's partitioning configuration.
func findPartitionChild(table *model.Table, childKey string) *model.PartitionSpec {
	if table == nil || table.Partitioning == nil {
		return nil
	}
	for i := range table.Partitioning.Children {
		child := &table.Partitioning.Children[i]
		if partitionChildKey(child) == childKey {
			return child
		}
	}
	return nil
}

// partitionChildQualifiedName derives the schema-qualified child table name
// from the parent table name and child spec. Uses the child's Name field;
// child partitions should always have a Name set.
func partitionChildQualifiedName(parentQualified string, child *model.PartitionSpec) string {
	schema, _ := splitQualifiedName(parentQualified)
	childName := child.Name
	if schema != "" && schema != "public" {
		return schema + "." + childName
	}
	return childName
}

// partitionChildNameFromKey extracts the child table name from a diff key and
// qualifies it with the parent's schema. The key format is "name:bound" or
// just "name".
func partitionChildNameFromKey(parentQualified string, childKey string) string {
	schema, _ := splitQualifiedName(parentQualified)
	childName := childKey
	if idx := strings.IndexByte(childKey, ':'); idx >= 0 {
		childName = childKey[:idx]
	}
	if schema != "" && schema != "public" {
		return schema + "." + childName
	}
	return childName
}

// buildBackfillSQL generates a DML statement to backfill NULL values for a
// column that is transitioning to NOT NULL. It looks up the column's default
// from the desired schema; if no default is found, it uses a type-appropriate
// zero value.
func buildBackfillSQL(tableName, colName string, desired *model.Schema) string {
	defaultVal := "0" // fallback
	table := findTable(desired, tableName)
	if table != nil {
		for _, col := range table.Columns {
			if col.Name == colName {
				if col.DefaultExpr != "" {
					defaultVal = col.DefaultExpr
				} else if col.Default != nil {
					defaultVal = formatDefault(*col.Default, col.PGType)
				} else {
					defaultVal = typeZeroValue(col.PGType)
				}
				break
			}
		}
	}
	return fmt.Sprintf("UPDATE %s SET %s = COALESCE(%s, %s) WHERE %s IS NULL",
		quoteQualified(tableName),
		quoteIdent(colName), quoteIdent(colName), defaultVal,
		quoteIdent(colName))
}

// quoteIdent is a convenience alias for sql.QuoteIdent used in backfill SQL.
func quoteIdent(name string) string {
	// Reuse the sql_gen.go import path.
	return quoteIdentSlice([]string{name})[0]
}

// typeZeroValue returns a safe zero-value literal for common PG types.
func typeZeroValue(pgType string) string {
	t := strings.ToLower(strings.TrimSpace(pgType))
	switch {
	case t == "boolean" || t == "bool":
		return "false"
	case t == "text" || strings.HasPrefix(t, "varchar") || strings.HasPrefix(t, "character") || t == "char" || t == "bpchar":
		return "''"
	case t == "uuid":
		return "'00000000-0000-0000-0000-000000000000'"
	case t == "jsonb" || t == "json":
		return "'{}'"
	case strings.Contains(t, "timestamp") || t == "date":
		return "now()"
	default:
		return "0"
	}
}
