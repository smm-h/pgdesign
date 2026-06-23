package codegen

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/graph"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sql"
)

// PythonDDLGenerator generates a Python file containing DDL statements as
// typed data tuples. Each statement is a (sql, kind, table_name_or_none, phase)
// tuple. The output mirrors the exact section order of generateSQL in the
// generate package.
type PythonDDLGenerator struct{}

// ddlTuple holds one DDL statement with its metadata.
type ddlTuple struct {
	SQL   string
	Kind  string
	Table string // empty string means None
	Phase int
}

// Generate produces a Python file with all DDL statements as data tuples.
func (g *PythonDDLGenerator) Generate(schema *model.Schema) ([]byte, []diagnostic.Diagnostic) {
	var tuples []ddlTuple
	var diags []diagnostic.Diagnostic

	// 1. CREATE SCHEMA (phase 1)
	if schema.Name != "" {
		tuples = append(tuples, ddlTuple{
			SQL:   sql.CreateSchema(schema.Name, false),
			Kind:  "schema",
			Phase: 1,
		})
	} else {
		seen := make(map[string]bool)
		for _, t := range schema.Tables {
			if t.Schema != "" && !seen[t.Schema] {
				seen[t.Schema] = true
				tuples = append(tuples, ddlTuple{
					SQL:   sql.CreateSchema(t.Schema, false),
					Kind:  "schema",
					Phase: 1,
				})
			}
		}
		for _, e := range schema.Enums {
			if e.Schema != "" && !seen[e.Schema] {
				seen[e.Schema] = true
				tuples = append(tuples, ddlTuple{
					SQL:   sql.CreateSchema(e.Schema, false),
					Kind:  "schema",
					Phase: 1,
				})
			}
		}
		for _, ct := range schema.CompositeTypes {
			if ct.Schema != "" && !seen[ct.Schema] {
				seen[ct.Schema] = true
				tuples = append(tuples, ddlTuple{
					SQL:   sql.CreateSchema(ct.Schema, false),
					Kind:  "schema",
					Phase: 1,
				})
			}
		}
	}

	// 2. CREATE EXTENSION (phase 2)
	for _, ext := range schema.Extensions {
		tuples = append(tuples, ddlTuple{
			SQL:   sql.CreateExtension(ext, false),
			Kind:  "extension",
			Phase: 2,
		})
	}

	// 2b. CREATE SEQUENCE (phase 2)
	for i := range schema.Sequences {
		tuples = append(tuples, ddlTuple{
			SQL:   sql.CreateSequence(schema.Sequences[i].Schema, &schema.Sequences[i]),
			Kind:  "sequence",
			Phase: 2,
		})
	}

	// 3. CREATE TYPE AS ENUM (phase 3)
	for _, e := range schema.Enums {
		tuples = append(tuples, ddlTuple{
			SQL:   sql.CreateEnum(e.Schema, e.Name, e.Values, false),
			Kind:  "enum",
			Phase: 3,
		})
	}

	// 3b. CREATE DOMAIN (phase 3)
	for _, d := range schema.Domains {
		tuples = append(tuples, ddlTuple{
			SQL:   sql.CreateDomain(d.Schema, d),
			Kind:  "domain",
			Phase: 3,
		})
	}

	// 3c. CREATE TYPE AS (composite) (phase 3)
	for _, ct := range schema.CompositeTypes {
		tuples = append(tuples, ddlTuple{
			SQL:   sql.CreateCompositeType(ct.Schema, ct),
			Kind:  "composite",
			Phase: 3,
		})
	}

	tables := schema.TableOrder()

	// 4. CREATE TABLE (phase 4)
	for i := range tables {
		tuples = append(tuples, ddlTuple{
			SQL:   sql.CreateTable(&tables[i], tables[i].Schema, false, schema.PGVersion, schema.Enums),
			Kind:  "table",
			Table: tables[i].Name,
			Phase: 4,
		})
	}

	// 5. CREATE TABLE ... PARTITION OF (phase 5)
	for i := range tables {
		t := &tables[i]
		if t.Partitioning != nil && len(t.Partitioning.Children) > 0 {
			collectPartitionTuples(t.Schema, t.Name, t.Partitioning.Children, &tuples)
		}
	}

	// 5b. pg_partman configuration (phase 5)
	for i := range tables {
		t := &tables[i]
		if t.Maintenance != nil && t.Partitioning != nil && hasExtension(schema, "pg_partman") {
			if len(t.Partitioning.Columns) > 1 {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E010",
					Table:    t.Name,
					Message:  fmt.Sprintf("pg_partman does not support multi-column partition keys on table %q", t.Name),
				})
				continue
			}
			tuples = append(tuples, ddlTuple{
				SQL:   sql.CreatePartmanParent(t.Schema, t.Name, t.Partitioning.Columns[0], t.Maintenance.Retention, t.Maintenance.Premake),
				Kind:  "partman",
				Table: t.Name,
				Phase: 5,
			})
			if t.Maintenance.Retention != "" {
				tuples = append(tuples, ddlTuple{
					SQL:   sql.UpdatePartmanConfig(t.Schema, t.Name, t.Maintenance.Retention, t.Maintenance.RetentionKeepTable),
					Kind:  "partman",
					Table: t.Name,
					Phase: 5,
				})
			}
		}
	}

	// 6. ALTER TABLE ADD CONSTRAINT FK (phase 6)
	for i := range tables {
		t := &tables[i]
		fks := sortedFKs(t.FKs)
		for _, fk := range fks {
			fkCopy := fk
			tuples = append(tuples, ddlTuple{
				SQL:   sql.AlterTableAddFK(t.Schema, t, &fkCopy, false),
				Kind:  "fk",
				Table: t.Name,
				Phase: 6,
			})
		}
	}

	// 7. ALTER TABLE ADD CONSTRAINT UNIQUE (phase 7)
	for i := range tables {
		t := &tables[i]
		uqs := sortedUniques(t.Uniques)
		for _, uq := range uqs {
			uqCopy := uq
			tuples = append(tuples, ddlTuple{
				SQL:   sql.AlterTableAddUnique(t.Schema, t.Name, &uqCopy, false),
				Kind:  "unique",
				Table: t.Name,
				Phase: 7,
			})
		}
	}

	// 8. ALTER TABLE ADD CONSTRAINT CHECK (phase 8)
	for i := range tables {
		t := &tables[i]
		cks := sortedChecks(t.Checks)
		for _, ck := range cks {
			ckCopy := ck
			tuples = append(tuples, ddlTuple{
				SQL:   sql.AlterTableAddCheck(t.Schema, t.Name, &ckCopy, false),
				Kind:  "check",
				Table: t.Name,
				Phase: 8,
			})
		}
	}

	// 8b. ALTER TABLE ADD CONSTRAINT EXCLUDE (phase 8)
	for i := range tables {
		t := &tables[i]
		excls := sortedExclusions(t.Exclusions)
		for _, exc := range excls {
			excCopy := exc
			tuples = append(tuples, ddlTuple{
				SQL:   sql.AlterTableAddExclusion(t.Schema, t.Name, &excCopy, false),
				Kind:  "exclusion",
				Table: t.Name,
				Phase: 8,
			})
		}
	}

	// 9. CREATE INDEX (phase 9)
	for i := range tables {
		t := &tables[i]
		idxs := sortedIndexes(t.Indexes)
		for _, idx := range idxs {
			idxCopy := idx
			tuples = append(tuples, ddlTuple{
				SQL:   sql.CreateIndex(t.Schema, &idxCopy, t.Name, false, false),
				Kind:  "index",
				Table: t.Name,
				Phase: 9,
			})
		}
	}

	// 9b. Append-only triggers (phase 9)
	{
		appendOnlySchemas := make(map[string]bool)
		for i := range tables {
			if tables[i].AppendOnly {
				appendOnlySchemas[tables[i].Schema] = true
			}
		}
		if len(appendOnlySchemas) > 0 {
			var schemaNames []string
			for s := range appendOnlySchemas {
				schemaNames = append(schemaNames, s)
			}
			sort.Strings(schemaNames)
			for _, s := range schemaNames {
				tuples = append(tuples, ddlTuple{
					SQL:   sql.CreateDenyMutationFunction(s),
					Kind:  "append_only_trigger",
					Phase: 9,
				})
			}
			for i := range tables {
				t := &tables[i]
				if t.AppendOnly {
					tuples = append(tuples, ddlTuple{
						SQL:   sql.CreateAppendOnlyTrigger(t.Schema, t.Name),
						Kind:  "append_only_trigger",
						Table: t.Name,
						Phase: 9,
					})
				}
			}
		}
	}

	// 10. COMMENT ON (phase 10)
	for i := range tables {
		t := &tables[i]
		if t.Comment != "" {
			qualified := sql.QualifiedName(t.Schema, t.Name)
			tuples = append(tuples, ddlTuple{
				SQL:   sql.CommentOn("TABLE", qualified, t.Comment),
				Kind:  "comment",
				Table: t.Name,
				Phase: 10,
			})
		}
		for _, col := range t.Columns {
			if col.Comment != "" {
				qualified := sql.QualifiedName(t.Schema, t.Name) + "." + sql.QuoteIdent(col.Name)
				tuples = append(tuples, ddlTuple{
					SQL:   sql.CommentOn("COLUMN", qualified, col.Comment),
					Kind:  "comment",
					Table: t.Name,
					Phase: 10,
				})
			}
		}
	}
	// Sequence comments
	for _, seq := range schema.Sequences {
		if seq.Comment != "" {
			qualified := sql.QualifiedName(seq.Schema, seq.Name)
			tuples = append(tuples, ddlTuple{
				SQL:   sql.CommentOn("SEQUENCE", qualified, seq.Comment),
				Kind:  "comment",
				Phase: 10,
			})
		}
	}

	// 10b. SET STATISTICS (phase 10)
	for i := range tables {
		t := &tables[i]
		for _, col := range t.Columns {
			if col.Statistics != nil {
				qualified := sql.QualifiedName(t.Schema, t.Name)
				tuples = append(tuples, ddlTuple{
					SQL:   fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET STATISTICS %d;", qualified, sql.QuoteIdent(col.Name), *col.Statistics),
					Kind:  "statistics",
					Table: t.Name,
					Phase: 10,
				})
			}
		}
	}

	// 11. ALTER TABLE OWNER (phase 11)
	for i := range tables {
		t := &tables[i]
		if t.Owner != "" {
			tuples = append(tuples, ddlTuple{
				SQL:   sql.AlterTableOwner(t.Schema, t.Name, t.Owner),
				Kind:  "owner",
				Table: t.Name,
				Phase: 11,
			})
		}
	}

	// 12. ENABLE RLS (phase 12)
	for i := range tables {
		t := &tables[i]
		if t.EnableRLS {
			tuples = append(tuples, ddlTuple{
				SQL:   sql.AlterTableEnableRLS(t.Schema, t.Name),
				Kind:  "rls_enable",
				Table: t.Name,
				Phase: 12,
			})
		}
	}

	// 12b. FORCE RLS (phase 12)
	for i := range tables {
		t := &tables[i]
		if t.ForceRLS {
			tuples = append(tuples, ddlTuple{
				SQL:   sql.AlterTableForceRLS(t.Schema, t.Name),
				Kind:  "rls_force",
				Table: t.Name,
				Phase: 12,
			})
		}
	}

	// 13. CREATE POLICY (phase 13)
	for i := range tables {
		t := &tables[i]
		policies := sortedPolicies(t.Policies)
		for _, p := range policies {
			tuples = append(tuples, ddlTuple{
				SQL:   sql.CreatePolicy(t.Schema, t.Name, p),
				Kind:  "policy",
				Table: t.Name,
				Phase: 13,
			})
		}
	}

	// 14. CREATE VIEW (phase 14)
	if len(schema.Views) > 0 {
		sorted, err := topoSortViews(schema.Views)
		if err != nil {
			sorted = schema.Views
			var cycleMembers []string
			for _, v := range sorted {
				cycleMembers = append(cycleMembers, v.Name)
			}
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Message:  fmt.Sprintf("dependency cycle detected among views: %s; emitted in declaration order", strings.Join(cycleMembers, ", ")),
			})
		}
		schemaName := schema.Name
		for i := range sorted {
			v := &sorted[i]
			if v.Schema != "" {
				schemaName = v.Schema
			}
			tuples = append(tuples, ddlTuple{
				SQL:   sql.CreateView(schemaName, v, false),
				Kind:  "view",
				Phase: 14,
			})
			if v.Comment != "" {
				tuples = append(tuples, ddlTuple{
					SQL:   sql.CommentOn("VIEW", sql.QualifiedName(schemaName, v.Name), v.Comment),
					Kind:  "comment",
					Phase: 14,
				})
			}
		}
	}

	// 15. CREATE MATERIALIZED VIEW (phase 15)
	if len(schema.MaterializedViews) > 0 {
		sorted, err := topoSortMaterializedViews(schema.MaterializedViews)
		if err != nil {
			sorted = schema.MaterializedViews
			var cycleMembers []string
			for _, mv := range sorted {
				cycleMembers = append(cycleMembers, mv.Name)
			}
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Message:  fmt.Sprintf("dependency cycle detected among materialized views: %s; emitted in declaration order", strings.Join(cycleMembers, ", ")),
			})
		}
		schemaName := schema.Name
		for i := range sorted {
			mv := &sorted[i]
			if mv.Schema != "" {
				schemaName = mv.Schema
			}
			tuples = append(tuples, ddlTuple{
				SQL:   sql.CreateMaterializedView(schemaName, mv),
				Kind:  "materialized_view",
				Phase: 15,
			})
			if mv.Comment != "" {
				tuples = append(tuples, ddlTuple{
					SQL:   sql.CommentOn("MATERIALIZED VIEW", sql.QualifiedName(schemaName, mv.Name), mv.Comment),
					Kind:  "comment",
					Phase: 15,
				})
			}
			for j := range mv.Indexes {
				idx := &mv.Indexes[j]
				tuples = append(tuples, ddlTuple{
					SQL:   sql.CreateIndex(schemaName, idx, mv.Name, false, false),
					Kind:  "index",
					Phase: 15,
				})
			}
		}
	}

	// 16. CREATE FUNCTION (phase 16)
	if len(schema.Functions) > 0 {
		sorted, err := topoSortFunctions(schema.Functions)
		if err != nil {
			sorted = schema.Functions
			var cycleMembers []string
			for _, f := range sorted {
				cycleMembers = append(cycleMembers, f.Name)
			}
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Warning,
				Message:  fmt.Sprintf("dependency cycle detected among functions: %s; emitted in declaration order", strings.Join(cycleMembers, ", ")),
			})
		}
		schemaName := schema.Name
		for i := range sorted {
			f := &sorted[i]
			if f.Schema != "" {
				schemaName = f.Schema
			}
			tuples = append(tuples, ddlTuple{
				SQL:   sql.CreateFunction(schemaName, *f),
				Kind:  "function",
				Phase: 16,
			})
			if f.Comment != "" {
				kind := "FUNCTION"
				if f.IsProc {
					kind = "PROCEDURE"
				}
				tuples = append(tuples, ddlTuple{
					SQL:   sql.CommentOn(kind, sql.QualifiedName(schemaName, f.Name), f.Comment),
					Kind:  "comment",
					Phase: 16,
				})
			}
		}
	}

	// 17. CREATE TRIGGER (phase 17)
	for i := range tables {
		t := &tables[i]
		triggers := sortedTriggers(t.Triggers)
		for _, trig := range triggers {
			tuples = append(tuples, ddlTuple{
				SQL:   sql.CreateTrigger(t.Schema, t.Name, trig),
				Kind:  "trigger",
				Table: t.Name,
				Phase: 17,
			})
		}
	}

	// Build output.
	var buf bytes.Buffer
	buf.WriteString("# Code generated by pgdesign -- do not edit.\n\n")
	buf.WriteString("from typing import Final\n\n")
	buf.WriteString("STATEMENTS: Final[list[tuple[str, str, str | None, int]]] = [\n")
	for _, t := range tuples {
		escapedSQL := pythonEscapeStr(t.SQL)
		tablePy := "None"
		if t.Table != "" {
			tablePy = fmt.Sprintf("%q", t.Table)
		}
		buf.WriteString(fmt.Sprintf("    (%s, %q, %s, %d),\n", escapedSQL, t.Kind, tablePy, t.Phase))
	}
	buf.WriteString("]\n\n")

	// TABLE_NAMES in dependency order.
	buf.WriteString("TABLE_NAMES: Final[tuple[str, ...]] = (")
	for i, t := range tables {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(fmt.Sprintf("%q", t.Name))
	}
	if len(tables) == 1 {
		buf.WriteString(",")
	}
	buf.WriteString(")\n")

	return buf.Bytes(), diags
}

// pythonEscapeStr returns a Python string literal for the given SQL.
// Multi-line strings use triple quotes; single-line strings use regular quotes.
func pythonEscapeStr(s string) string {
	s = strings.TrimRight(s, "\n")
	if strings.Contains(s, "\n") {
		// Use triple-quoted string for multi-line SQL.
		escaped := strings.ReplaceAll(s, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"""`, `\"\"\"`)
		return `"""` + escaped + `"""`
	}
	// Single-line: use regular quoted string.
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// collectPartitionTuples recursively collects partition DDL tuples.
func collectPartitionTuples(schemaName, parentTable string, children []model.PartitionSpec, tuples *[]ddlTuple) {
	for i := range children {
		child := &children[i]
		*tuples = append(*tuples, ddlTuple{
			SQL:   sql.CreatePartitionOf(schemaName, child, parentTable, false),
			Kind:  "partition",
			Table: child.Name,
			Phase: 5,
		})
		if len(child.Children) > 0 {
			collectPartitionTuples(schemaName, child.Name, child.Children, tuples)
		}
	}
}

// hasExtension returns true if the schema declares the named extension.
func hasExtension(schema *model.Schema, name string) bool {
	for _, ext := range schema.Extensions {
		if ext == name {
			return true
		}
	}
	return false
}

// Helper sort functions (duplicated from generate package to avoid circular deps).
func sortedFKs(fks []model.FK) []model.FK {
	result := make([]model.FK, len(fks))
	copy(result, fks)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func sortedUniques(uqs []model.UniqueConstraint) []model.UniqueConstraint {
	result := make([]model.UniqueConstraint, len(uqs))
	copy(result, uqs)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func sortedChecks(cks []model.CheckConstraint) []model.CheckConstraint {
	result := make([]model.CheckConstraint, len(cks))
	copy(result, cks)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func sortedExclusions(excls []model.ExclusionConstraint) []model.ExclusionConstraint {
	sorted := make([]model.ExclusionConstraint, len(excls))
	copy(sorted, excls)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func sortedIndexes(idxs []model.Index) []model.Index {
	result := make([]model.Index, len(idxs))
	copy(result, idxs)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func sortedPolicies(pols []model.Policy) []model.Policy {
	result := make([]model.Policy, len(pols))
	copy(result, pols)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func sortedTriggers(trigs []model.Trigger) []model.Trigger {
	result := make([]model.Trigger, len(trigs))
	copy(result, trigs)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// topoSortViews sorts views by DependsOn.
func topoSortViews(views []model.View) ([]model.View, error) {
	sorted, cycles := graph.TopoSort(views,
		func(v model.View) string { return v.Name },
		func(v model.View) []string { return v.DependsOn },
	)
	if len(cycles) > 0 {
		return nil, fmt.Errorf("cycle detected in view dependencies")
	}
	return sorted, nil
}

// topoSortMaterializedViews sorts materialized views by DependsOn.
func topoSortMaterializedViews(mvs []model.MaterializedView) ([]model.MaterializedView, error) {
	sorted, cycles := graph.TopoSort(mvs,
		func(mv model.MaterializedView) string { return mv.Name },
		func(mv model.MaterializedView) []string { return mv.DependsOn },
	)
	if len(cycles) > 0 {
		return nil, fmt.Errorf("cycle detected in materialized view dependencies")
	}
	return sorted, nil
}

// topoSortFunctions sorts functions by DependsOn.
func topoSortFunctions(funcs []model.Function) ([]model.Function, error) {
	sorted, cycles := graph.TopoSort(funcs,
		func(f model.Function) string { return f.Name },
		func(f model.Function) []string { return f.DependsOn },
	)
	if len(cycles) > 0 {
		return nil, fmt.Errorf("cycle detected in function dependencies")
	}
	return sorted, nil
}
