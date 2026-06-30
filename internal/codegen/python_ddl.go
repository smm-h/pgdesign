package codegen

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/graph"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sql"
)

// SplitMode controls how PythonDDLGenerator distributes output across files.
type SplitMode string

const (
	// SplitModeNone produces the default two-file output (schema_ddl.py + schema_executor.py).
	SplitModeNone SplitMode = ""
	// SplitModeFaceted splits output by concern (extensions, types, tables per source, post-tables).
	SplitModeFaceted SplitMode = "faceted"
	// SplitModeSelfContained splits output so each file is independently executable.
	SplitModeSelfContained SplitMode = "self-contained"
)

// AllSplitModes lists the valid non-empty split modes.
var AllSplitModes = []SplitMode{SplitModeFaceted, SplitModeSelfContained}

// PythonDDLGenerator generates a Python file containing DDL statements as
// DDLStmt namedtuples with 7 fields: sql, idempotent_sql, kind, name, table,
// phase, transactional. The output mirrors the exact section order of
// generateSQL in the generate package.
type PythonDDLGenerator struct {
	SplitMode SplitMode
}

// ddlTuple holds one DDL statement with its metadata.
type ddlTuple struct {
	SQL           string
	IdempotentSQL string // empty means no idempotent variant available
	Kind          string
	Name          string // human-readable name for the DDL op (e.g. table name, constraint name)
	Table         string // empty string means None
	Phase         int
	Transactional bool   // false for CONCURRENTLY indexes, ALTER TYPE ADD VALUE
	SourceFile    string // original TOML source file; empty for source-independent tuples
}

// buildTuples collects all DDL statements from the schema into a flat list of
// ddlTuples. Each tuple has both normal and idempotent SQL (when available),
// a human-readable name, and transactional metadata.
func buildTuples(schema *model.Schema) ([]ddlTuple, []model.Table, []diagnostic.Diagnostic) {
	var tuples []ddlTuple
	var diags []diagnostic.Diagnostic

	// 1. CREATE SCHEMA (phase 1)
	if schema.Name != "" {
		tuples = append(tuples, ddlTuple{
			SQL:           sql.CreateSchema(schema.Name, false),
			IdempotentSQL: sql.CreateSchema(schema.Name, true),
			Kind:          "schema",
			Name:          schema.Name,
			Phase:         1,
			Transactional: true,
		})
	} else {
		seen := make(map[string]bool)
		for _, t := range schema.Tables {
			if t.Schema != "" && !seen[t.Schema] {
				seen[t.Schema] = true
				tuples = append(tuples, ddlTuple{
					SQL:           sql.CreateSchema(t.Schema, false),
					IdempotentSQL: sql.CreateSchema(t.Schema, true),
					Kind:          "schema",
					Name:          t.Schema,
					Phase:         1,
					Transactional: true,
				})
			}
		}
		for _, e := range schema.Enums {
			if e.Schema != "" && !seen[e.Schema] {
				seen[e.Schema] = true
				tuples = append(tuples, ddlTuple{
					SQL:           sql.CreateSchema(e.Schema, false),
					IdempotentSQL: sql.CreateSchema(e.Schema, true),
					Kind:          "schema",
					Name:          e.Schema,
					Phase:         1,
					Transactional: true,
				})
			}
		}
		for _, ct := range schema.CompositeTypes {
			if ct.Schema != "" && !seen[ct.Schema] {
				seen[ct.Schema] = true
				tuples = append(tuples, ddlTuple{
					SQL:           sql.CreateSchema(ct.Schema, false),
					IdempotentSQL: sql.CreateSchema(ct.Schema, true),
					Kind:          "schema",
					Name:          ct.Schema,
					Phase:         1,
					Transactional: true,
				})
			}
		}
	}

	// 2. CREATE EXTENSION (phase 2)
	for _, ext := range schema.Extensions {
		tuples = append(tuples, ddlTuple{
			SQL:           sql.CreateExtension(ext, false),
			IdempotentSQL: sql.CreateExtension(ext, true),
			Kind:          "extension",
			Name:          ext,
			Phase:         2,
			Transactional: true,
		})
	}

	// 2b. CREATE SEQUENCE (phase 2)
	for i := range schema.Sequences {
		seq := &schema.Sequences[i]
		tuples = append(tuples, ddlTuple{
			SQL:           sql.CreateSequence(seq.Schema, seq, false),
			Kind:          "sequence",
			Name:          seq.Name,
			Phase:         2,
			Transactional: true,
			SourceFile:    seq.SourceFile,
		})
	}

	// 3. CREATE TYPE AS ENUM (phase 3)
	for _, e := range schema.Enums {
		tuples = append(tuples, ddlTuple{
			SQL:           sql.CreateEnum(e.Schema, e.Name, e.Values, false),
			IdempotentSQL: sql.CreateEnum(e.Schema, e.Name, e.Values, true),
			Kind:          "enum",
			Name:          e.Name,
			Phase:         3,
			Transactional: true,
			SourceFile:    e.SourceFile,
		})
	}

	// 3b. CREATE DOMAIN (phase 3)
	for _, d := range schema.Domains {
		tuples = append(tuples, ddlTuple{
			SQL:           sql.CreateDomain(d.Schema, d, false),
			IdempotentSQL: sql.CreateDomain(d.Schema, d, true),
			Kind:          "domain",
			Name:          d.Name,
			Phase:         3,
			Transactional: true,
			SourceFile:    d.SourceFile,
		})
	}

	// 3c. CREATE TYPE AS (composite) (phase 3)
	for _, ct := range schema.CompositeTypes {
		tuples = append(tuples, ddlTuple{
			SQL:           sql.CreateCompositeType(ct.Schema, ct, false),
			IdempotentSQL: sql.CreateCompositeType(ct.Schema, ct, true),
			Kind:          "composite",
			Name:          ct.Name,
			Phase:         3,
			Transactional: true,
			SourceFile:    ct.SourceFile,
		})
	}

	tables := schema.TableOrder()

	// 4. CREATE TABLE (phase 4)
	for i := range tables {
		tuples = append(tuples, ddlTuple{
			SQL:           sql.CreateTable(&tables[i], tables[i].Schema, false, schema.PGVersion, schema.Enums),
			IdempotentSQL: sql.CreateTable(&tables[i], tables[i].Schema, true, schema.PGVersion, schema.Enums),
			Kind:          "table",
			Name:          tables[i].Name,
			Table:         tables[i].Name,
			Phase:         4,
			Transactional: true,
			SourceFile:    tables[i].SourceFile,
		})
	}

	// 5. CREATE TABLE ... PARTITION OF (phase 5)
	for i := range tables {
		t := &tables[i]
		if t.Partitioning != nil && len(t.Partitioning.Children) > 0 {
			collectPartitionTuples(t.Schema, t.Name, t.SourceFile, t.Partitioning.Children, &tuples)
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
				SQL:           sql.CreatePartmanParent(t.Schema, t.Name, t.Partitioning.Columns[0], t.Maintenance.Retention, t.Maintenance.Premake),
				Kind:          "partman",
				Name:          t.Name,
				Table:         t.Name,
				Phase:         5,
				Transactional: true,
				SourceFile:    t.SourceFile,
			})
			if t.Maintenance.Retention != "" {
				tuples = append(tuples, ddlTuple{
					SQL:           sql.UpdatePartmanConfig(t.Schema, t.Name, t.Maintenance.Retention, t.Maintenance.RetentionKeepTable),
					Kind:          "partman",
					Name:          t.Name + "_config",
					Table:         t.Name,
					Phase:         5,
					Transactional: true,
					SourceFile:    t.SourceFile,
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
			constraintName := fk.Name
			if constraintName == "" {
				constraintName = sql.ConstraintName(t.Name, "fk", fk.RefTable)
			}
			tuples = append(tuples, ddlTuple{
				SQL:           sql.AlterTableAddFK(t.Schema, t, &fkCopy, false),
				IdempotentSQL: sql.AlterTableAddFK(t.Schema, t, &fkCopy, true),
				Kind:          "fk",
				Name:          constraintName,
				Table:         t.Name,
				Phase:         6,
				Transactional: true,
				SourceFile:    t.SourceFile,
			})
		}
	}

	// 7. ALTER TABLE ADD CONSTRAINT UNIQUE (phase 7)
	for i := range tables {
		t := &tables[i]
		uqs := sortedUniques(t.Uniques)
		for _, uq := range uqs {
			uqCopy := uq
			constraintName := uq.Name
			if constraintName == "" {
				constraintName = sql.ConstraintName(t.Name, "uq", uq.Columns...)
			}
			tuples = append(tuples, ddlTuple{
				SQL:           sql.AlterTableAddUnique(t.Schema, t.Name, &uqCopy, false),
				IdempotentSQL: sql.AlterTableAddUnique(t.Schema, t.Name, &uqCopy, true),
				Kind:          "unique",
				Name:          constraintName,
				Table:         t.Name,
				Phase:         7,
				Transactional: true,
				SourceFile:    t.SourceFile,
			})
		}
	}

	// 8. ALTER TABLE ADD CONSTRAINT CHECK (phase 8)
	for i := range tables {
		t := &tables[i]
		cks := sortedChecks(t.Checks)
		for _, ck := range cks {
			ckCopy := ck
			constraintName := ck.Name
			if constraintName == "" {
				constraintName = sql.ConstraintName(t.Name, "ck")
			}
			tuples = append(tuples, ddlTuple{
				SQL:           sql.AlterTableAddCheck(t.Schema, t.Name, &ckCopy, false),
				IdempotentSQL: sql.AlterTableAddCheck(t.Schema, t.Name, &ckCopy, true),
				Kind:          "check",
				Name:          constraintName,
				Table:         t.Name,
				Phase:         8,
				Transactional: true,
				SourceFile:    t.SourceFile,
			})
		}
	}

	// 8b. ALTER TABLE ADD CONSTRAINT EXCLUDE (phase 8)
	for i := range tables {
		t := &tables[i]
		excls := sortedExclusions(t.Exclusions)
		for _, exc := range excls {
			excCopy := exc
			constraintName := exc.Name
			if constraintName == "" {
				constraintName = sql.ConstraintName(t.Name, "excl", exc.Elements[0].Column)
			}
			tuples = append(tuples, ddlTuple{
				SQL:           sql.AlterTableAddExclusion(t.Schema, t.Name, &excCopy, false),
				IdempotentSQL: sql.AlterTableAddExclusion(t.Schema, t.Name, &excCopy, true),
				Kind:          "exclusion",
				Name:          constraintName,
				Table:         t.Name,
				Phase:         8,
				Transactional: true,
				SourceFile:    t.SourceFile,
			})
		}
	}

	// 9. CREATE INDEX (phase 9)
	for i := range tables {
		t := &tables[i]
		idxs := sortedIndexes(t.Indexes)
		for _, idx := range idxs {
			idxCopy := idx
			idxName := idx.Name
			if idxName == "" {
				idxName = sql.ConstraintName(t.Name, "idx", idx.Columns...)
			}
			tuples = append(tuples, ddlTuple{
				SQL:           sql.CreateIndex(t.Schema, &idxCopy, t.Name, false, false),
				IdempotentSQL: sql.CreateIndex(t.Schema, &idxCopy, t.Name, true, false),
				Kind:          "index",
				Name:          idxName,
				Table:         t.Name,
				Phase:         9,
				Transactional: true,
				SourceFile:    t.SourceFile,
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
					SQL:           sql.CreateDenyMutationFunction(s),
					Kind:          "append_only_trigger",
					Name:          s + ".pgdesign_deny_mutation",
					Phase:         9,
					Transactional: true,
				})
			}
			for i := range tables {
				t := &tables[i]
				if t.AppendOnly {
					tuples = append(tuples, ddlTuple{
						SQL:           sql.CreateAppendOnlyTrigger(t.Schema, t.Name, false, 0),
						Kind:          "append_only_trigger",
						Name:          t.Name + ".deny_mutation",
						Table:         t.Name,
						Phase:         9,
						Transactional: true,
						SourceFile:    t.SourceFile,
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
				SQL:           sql.CommentOn("TABLE", qualified, t.Comment),
				Kind:          "comment",
				Name:          "table." + t.Name,
				Table:         t.Name,
				Phase:         10,
				Transactional: true,
				SourceFile:    t.SourceFile,
			})
		}
		for _, col := range t.Columns {
			if col.Comment != "" {
				qualified := sql.QualifiedName(t.Schema, t.Name) + "." + sql.QuoteIdent(col.Name)
				tuples = append(tuples, ddlTuple{
					SQL:           sql.CommentOn("COLUMN", qualified, col.Comment),
					Kind:          "comment",
					Name:          "column." + t.Name + "." + col.Name,
					Table:         t.Name,
					Phase:         10,
					Transactional: true,
					SourceFile:    t.SourceFile,
				})
			}
		}
	}
	// Sequence comments
	for _, seq := range schema.Sequences {
		if seq.Comment != "" {
			qualified := sql.QualifiedName(seq.Schema, seq.Name)
			tuples = append(tuples, ddlTuple{
				SQL:           sql.CommentOn("SEQUENCE", qualified, seq.Comment),
				Kind:          "comment",
				Name:          "sequence." + seq.Name,
				Phase:         10,
				Transactional: true,
				SourceFile:    seq.SourceFile,
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
					SQL:           fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET STATISTICS %d;", qualified, sql.QuoteIdent(col.Name), *col.Statistics),
					Kind:          "statistics",
					Name:          t.Name + "." + col.Name,
					Table:         t.Name,
					Phase:         10,
					Transactional: true,
					SourceFile:    t.SourceFile,
				})
			}
		}
	}

	// 11. ALTER TABLE OWNER (phase 11)
	for i := range tables {
		t := &tables[i]
		if t.Owner != "" {
			tuples = append(tuples, ddlTuple{
				SQL:           sql.AlterTableOwner(t.Schema, t.Name, t.Owner),
				Kind:          "owner",
				Name:          t.Name,
				Table:         t.Name,
				Phase:         11,
				Transactional: true,
				SourceFile:    t.SourceFile,
			})
		}
	}

	// 12. ENABLE RLS (phase 12)
	for i := range tables {
		t := &tables[i]
		if t.EnableRLS {
			tuples = append(tuples, ddlTuple{
				SQL:           sql.AlterTableEnableRLS(t.Schema, t.Name),
				Kind:          "rls_enable",
				Name:          t.Name,
				Table:         t.Name,
				Phase:         12,
				Transactional: true,
				SourceFile:    t.SourceFile,
			})
		}
	}

	// 12b. FORCE RLS (phase 12)
	for i := range tables {
		t := &tables[i]
		if t.ForceRLS {
			tuples = append(tuples, ddlTuple{
				SQL:           sql.AlterTableForceRLS(t.Schema, t.Name),
				Kind:          "rls_force",
				Name:          t.Name,
				Table:         t.Name,
				Phase:         12,
				Transactional: true,
				SourceFile:    t.SourceFile,
			})
		}
	}

	// 13. CREATE POLICY (phase 13)
	for i := range tables {
		t := &tables[i]
		policies := sortedPolicies(t.Policies)
		for _, p := range policies {
			tuples = append(tuples, ddlTuple{
				SQL:           sql.CreatePolicy(t.Schema, t.Name, p, false, 0),
				Kind:          "policy",
				Name:          p.Name,
				Table:         t.Name,
				Phase:         13,
				Transactional: true,
				SourceFile:    t.SourceFile,
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
				SQL:           sql.CreateView(schemaName, v, false),
				IdempotentSQL: sql.CreateView(schemaName, v, true),
				Kind:          "view",
				Name:          v.Name,
				Phase:         14,
				Transactional: true,
				SourceFile:    v.SourceFile,
			})
			if v.Comment != "" {
				tuples = append(tuples, ddlTuple{
					SQL:           sql.CommentOn("VIEW", sql.QualifiedName(schemaName, v.Name), v.Comment),
					Kind:          "comment",
					Name:          "view." + v.Name,
					Phase:         14,
					Transactional: true,
					SourceFile:    v.SourceFile,
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
				SQL:           sql.CreateMaterializedView(schemaName, mv, false),
				Kind:          "materialized_view",
				Name:          mv.Name,
				Phase:         15,
				Transactional: true,
				SourceFile:    mv.SourceFile,
			})
			if mv.Comment != "" {
				tuples = append(tuples, ddlTuple{
					SQL:           sql.CommentOn("MATERIALIZED VIEW", sql.QualifiedName(schemaName, mv.Name), mv.Comment),
					Kind:          "comment",
					Name:          "matview." + mv.Name,
					Phase:         15,
					Transactional: true,
					SourceFile:    mv.SourceFile,
				})
			}
			for j := range mv.Indexes {
				idx := &mv.Indexes[j]
				idxName := idx.Name
				if idxName == "" {
					idxName = sql.ConstraintName(mv.Name, "idx", idx.Columns...)
				}
				tuples = append(tuples, ddlTuple{
					SQL:           sql.CreateIndex(schemaName, idx, mv.Name, false, false),
					IdempotentSQL: sql.CreateIndex(schemaName, idx, mv.Name, true, false),
					Kind:          "index",
					Name:          idxName,
					Phase:         15,
					Transactional: true,
					SourceFile:    mv.SourceFile,
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
			// CREATE OR REPLACE FUNCTION is inherently idempotent.
			funcSQL := sql.CreateFunction(schemaName, *f)
			tuples = append(tuples, ddlTuple{
				SQL:           funcSQL,
				IdempotentSQL: funcSQL,
				Kind:          "function",
				Name:          f.Name,
				Phase:         16,
				Transactional: true,
				SourceFile:    f.SourceFile,
			})
			if f.Comment != "" {
				kind := "FUNCTION"
				if f.IsProc {
					kind = "PROCEDURE"
				}
				commentSQL := sql.CommentOn(kind, sql.QualifiedName(schemaName, f.Name), f.Comment)
				tuples = append(tuples, ddlTuple{
					SQL:           commentSQL,
					IdempotentSQL: commentSQL,
					Kind:          "comment",
					Name:          "func." + f.Name,
					Phase:         16,
					Transactional: true,
					SourceFile:    f.SourceFile,
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
				SQL:           sql.CreateTrigger(t.Schema, t.Name, trig, false, 0),
				Kind:          "trigger",
				Name:          trig.Name,
				Table:         t.Name,
				Phase:         17,
				Transactional: true,
				SourceFile:    t.SourceFile,
			})
		}
	}

	return tuples, tables, diags
}

// writeDDLStmtDef emits the DDLStmt namedtuple definition (import + definition).
func writeDDLStmtDef(buf *bytes.Buffer) {
	buf.WriteString("from collections import namedtuple\n\n")
	buf.WriteString("DDLStmt = namedtuple(\"DDLStmt\", [\"sql\", \"idempotent_sql\", \"kind\", \"name\", \"table\", \"phase\", \"transactional\"])\n\n")
}

// writeDDLStmt emits a single DDLStmt(...) call for a tuple.
func writeDDLStmt(buf *bytes.Buffer, t ddlTuple) {
	sqlPy := pythonEscapeStr(t.SQL)
	idempotentPy := "None"
	if t.IdempotentSQL != "" {
		idempotentPy = pythonEscapeStr(t.IdempotentSQL)
	}
	namePy := "None"
	if t.Name != "" {
		namePy = fmt.Sprintf("%q", t.Name)
	}
	tablePy := "None"
	if t.Table != "" {
		tablePy = fmt.Sprintf("%q", t.Table)
	}
	buf.WriteString(fmt.Sprintf("    DDLStmt(%s, %s, %q, %s, %s, %d, %s),\n",
		sqlPy, idempotentPy, t.Kind, namePy, tablePy, t.Phase, pythonBool(t.Transactional)))
}

// renderDDLFile renders the schema_ddl.py file content from the given tuples.
func renderDDLFile(tuples []ddlTuple, tables []model.Table) []byte {
	var buf bytes.Buffer
	buf.WriteString("# Code generated by pgdesign -- do not edit.\n\n")
	buf.WriteString("from typing import Final\n\n")
	writeDDLStmtDef(&buf)
	buf.WriteString("STATEMENTS: Final[list[DDLStmt]] = [\n")
	for _, t := range tuples {
		writeDDLStmt(&buf, t)
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

	return buf.Bytes()
}

// facetModule describes a faceted output module for executor imports.
type facetModule struct {
	fileName   string // e.g. "extensions.py"
	moduleName string // e.g. "extensions" (Python module name, no .py)
	alias      string // e.g. "_ext_stmts"
}

// ddlSection groups tuples by their DDL phase kind for the executor file.
type ddlSection struct {
	kind          string
	phase         int
	transactional bool
	tuples        []ddlTuple
}

// buildSections groups tuples into ordered sections by phase. Consecutive
// tuples with the same phase are merged into one section.
func buildSections(tuples []ddlTuple) []ddlSection {
	if len(tuples) == 0 {
		return nil
	}

	// Map phase -> section kind name. We use the phase number to derive
	// a human-readable kind for each section.
	phaseKind := map[int]string{
		1:  "schemas",
		2:  "extensions",
		3:  "types",
		4:  "tables",
		5:  "partitions",
		6:  "foreign_keys",
		7:  "unique_constraints",
		8:  "check_constraints",
		9:  "indexes",
		10: "comments",
		11: "ownership",
		12: "row_level_security",
		13: "policies",
		14: "views",
		15: "materialized_views",
		16: "functions",
		17: "triggers",
	}

	var sections []ddlSection
	var current *ddlSection

	for _, t := range tuples {
		if current == nil || current.phase != t.Phase {
			kind := phaseKind[t.Phase]
			if kind == "" {
				kind = fmt.Sprintf("phase_%d", t.Phase)
			}
			sections = append(sections, ddlSection{
				kind:          kind,
				phase:         t.Phase,
				transactional: true,
			})
			current = &sections[len(sections)-1]
		}
		current.tuples = append(current.tuples, t)
		if !t.Transactional {
			current.transactional = false
		}
	}

	return sections
}

// renderExecutorFile generates the schema_executor.py file content.
func renderExecutorFile(sections []ddlSection) []byte {
	var buf bytes.Buffer

	buf.WriteString("# Code generated by pgdesign -- do not edit.\n\n")
	buf.WriteString("from __future__ import annotations\n\n")
	buf.WriteString("from collections import namedtuple\n")
	buf.WriteString("from dataclasses import dataclass, field\n")
	buf.WriteString("from typing import Protocol, Sequence, runtime_checkable\n\n\n")

	// AsyncConnection protocol
	buf.WriteString("@runtime_checkable\n")
	buf.WriteString("class AsyncConnection(Protocol):\n")
	buf.WriteString("    async def execute(self, query: str) -> None: ...\n")
	buf.WriteString("    async def fetch(self, query: str) -> list[dict]: ...\n")
	buf.WriteString("    async def transaction(self) -> AsyncTransaction: ...\n\n\n")

	buf.WriteString("@runtime_checkable\n")
	buf.WriteString("class AsyncTransaction(Protocol):\n")
	buf.WriteString("    async def __aenter__(self) -> AsyncTransaction: ...\n")
	buf.WriteString("    async def __aexit__(self, exc_type, exc_val, exc_tb) -> None: ...\n")
	buf.WriteString("    async def execute(self, query: str) -> None: ...\n\n\n")

	// DDLOp namedtuple
	buf.WriteString("DDLOp = namedtuple(\"DDLOp\", [\"sql\", \"idempotent_sql\", \"name\"])\n\n\n")

	// Section dataclass
	buf.WriteString("@dataclass(frozen=True)\n")
	buf.WriteString("class Section:\n")
	buf.WriteString("    kind: str\n")
	buf.WriteString("    ops: list[DDLOp] = field(default_factory=list)\n")
	buf.WriteString("    transactional: bool = True\n\n")

	// Existence check queries by kind
	buf.WriteString("    async def exists(self, conn: AsyncConnection, op: DDLOp) -> bool:\n")
	buf.WriteString("        \"\"\"Check if the object described by op already exists.\"\"\"\n")
	buf.WriteString("        checkers = {\n")
	buf.WriteString("            \"schemas\": \"SELECT 1 FROM information_schema.schemata WHERE schema_name = '{}'\",\n")
	buf.WriteString("            \"extensions\": \"SELECT 1 FROM pg_extension WHERE extname = '{}'\",\n")
	buf.WriteString("            \"types\": \"SELECT 1 FROM pg_type WHERE typname = '{}'\",\n")
	buf.WriteString("            \"tables\": \"SELECT 1 FROM information_schema.tables WHERE table_name = '{}'\",\n")
	buf.WriteString("            \"indexes\": \"SELECT 1 FROM pg_indexes WHERE indexname = '{}'\",\n")
	buf.WriteString("            \"views\": \"SELECT 1 FROM information_schema.views WHERE table_name = '{}'\",\n")
	buf.WriteString("            \"materialized_views\": \"SELECT 1 FROM pg_matviews WHERE matviewname = '{}'\",\n")
	buf.WriteString("            \"functions\": \"SELECT 1 FROM pg_proc WHERE proname = '{}'\",\n")
	buf.WriteString("            \"triggers\": \"SELECT 1 FROM pg_trigger WHERE tgname = '{}'\",\n")
	buf.WriteString("            \"policies\": \"SELECT 1 FROM pg_policies WHERE policyname = '{}'\",\n")
	buf.WriteString("            \"foreign_keys\": \"SELECT 1 FROM pg_constraint WHERE conname = '{}' AND contype = 'f'\",\n")
	buf.WriteString("            \"unique_constraints\": \"SELECT 1 FROM pg_constraint WHERE conname = '{}' AND contype = 'u'\",\n")
	buf.WriteString("            \"check_constraints\": \"SELECT 1 FROM pg_constraint WHERE conname = '{}' AND contype = 'c'\",\n")
	buf.WriteString("        }\n")
	buf.WriteString("        query_template = checkers.get(self.kind)\n")
	buf.WriteString("        if query_template is None:\n")
	buf.WriteString("            return False\n")
	buf.WriteString("        escaped = op.name.replace(\"'\", \"''\")\n")
	buf.WriteString("        rows = await conn.fetch(query_template.format(escaped))\n")
	buf.WriteString("        return len(rows) > 0\n\n\n")

	// ExecutionResult
	buf.WriteString("@dataclass\n")
	buf.WriteString("class ExecutionResult:\n")
	buf.WriteString("    executed: list[tuple[str, str]] = field(default_factory=list)  # (section_kind, op_name)\n")
	buf.WriteString("    skipped: list[tuple[str, str]] = field(default_factory=list)\n")
	buf.WriteString("    errors: list[tuple[str, str, str]] = field(default_factory=list)  # (section_kind, op_name, error)\n\n\n")

	// VerifyResult
	buf.WriteString("@dataclass\n")
	buf.WriteString("class VerifyResult:\n")
	buf.WriteString("    present: list[tuple[str, str]] = field(default_factory=list)  # (section_kind, op_name)\n")
	buf.WriteString("    missing: list[tuple[str, str]] = field(default_factory=list)\n\n\n")

	// Sections list
	buf.WriteString("SECTIONS: list[Section] = [\n")
	for _, sec := range sections {
		buf.WriteString(fmt.Sprintf("    Section(\n        kind=%q,\n        transactional=%s,\n        ops=[\n",
			sec.kind, pythonBool(sec.transactional)))
		for _, t := range sec.tuples {
			sqlPy := pythonEscapeStr(t.SQL)
			idempotentPy := "None"
			if t.IdempotentSQL != "" {
				idempotentPy = pythonEscapeStr(t.IdempotentSQL)
			}
			namePy := "None"
			if t.Name != "" {
				namePy = fmt.Sprintf("%q", t.Name)
			}
			buf.WriteString(fmt.Sprintf("            DDLOp(%s, %s, %s),\n", sqlPy, idempotentPy, namePy))
		}
		buf.WriteString("        ],\n    ),\n")
	}
	buf.WriteString("]\n\n\n")

	// execute function
	buf.WriteString("async def execute(\n")
	buf.WriteString("    conn: AsyncConnection,\n")
	buf.WriteString("    sections: Sequence[str] | None = None,\n")
	buf.WriteString("    idempotent: bool = True,\n")
	buf.WriteString("    dry_run: bool = False,\n")
	buf.WriteString(") -> ExecutionResult:\n")
	buf.WriteString("    \"\"\"Execute DDL sections.\n\n")
	buf.WriteString("    Two-phase execution: transactional sections run inside a single\n")
	buf.WriteString("    transaction, non-transactional sections run outside afterward.\n")
	buf.WriteString("    \"\"\"\n")
	buf.WriteString("    result = ExecutionResult()\n")
	buf.WriteString("    selected = [s for s in SECTIONS if sections is None or s.kind in sections]\n")
	buf.WriteString("    transactional = [s for s in selected if s.transactional]\n")
	buf.WriteString("    non_transactional = [s for s in selected if not s.transactional]\n\n")
	buf.WriteString("    # Phase 1: transactional ops in a single transaction.\n")
	buf.WriteString("    if transactional and not dry_run:\n")
	buf.WriteString("        async with conn.transaction() as tx:\n")
	buf.WriteString("            for sec in transactional:\n")
	buf.WriteString("                for op in sec.ops:\n")
	buf.WriteString("                    stmt = op.idempotent_sql if idempotent and op.idempotent_sql else op.sql\n")
	buf.WriteString("                    try:\n")
	buf.WriteString("                        await tx.execute(stmt)\n")
	buf.WriteString("                        result.executed.append((sec.kind, op.name))\n")
	buf.WriteString("                    except Exception as e:\n")
	buf.WriteString("                        result.errors.append((sec.kind, op.name, str(e)))\n")
	buf.WriteString("                        raise\n")
	buf.WriteString("    elif transactional and dry_run:\n")
	buf.WriteString("        for sec in transactional:\n")
	buf.WriteString("            for op in sec.ops:\n")
	buf.WriteString("                result.executed.append((sec.kind, op.name))\n\n")
	buf.WriteString("    # Phase 2: non-transactional ops outside any transaction.\n")
	buf.WriteString("    for sec in non_transactional:\n")
	buf.WriteString("        for op in sec.ops:\n")
	buf.WriteString("            stmt = op.idempotent_sql if idempotent and op.idempotent_sql else op.sql\n")
	buf.WriteString("            if not dry_run:\n")
	buf.WriteString("                try:\n")
	buf.WriteString("                    await conn.execute(stmt)\n")
	buf.WriteString("                    result.executed.append((sec.kind, op.name))\n")
	buf.WriteString("                except Exception as e:\n")
	buf.WriteString("                    result.errors.append((sec.kind, op.name, str(e)))\n")
	buf.WriteString("            else:\n")
	buf.WriteString("                result.executed.append((sec.kind, op.name))\n\n")
	buf.WriteString("    return result\n\n\n")

	// verify function
	buf.WriteString("async def verify(conn: AsyncConnection) -> VerifyResult:\n")
	buf.WriteString("    \"\"\"Check which ops across all sections exist in the database.\"\"\"\n")
	buf.WriteString("    result = VerifyResult()\n")
	buf.WriteString("    for sec in SECTIONS:\n")
	buf.WriteString("        for op in sec.ops:\n")
	buf.WriteString("            if await sec.exists(conn, op):\n")
	buf.WriteString("                result.present.append((sec.kind, op.name))\n")
	buf.WriteString("            else:\n")
	buf.WriteString("                result.missing.append((sec.kind, op.name))\n")
	buf.WriteString("    return result\n\n\n")

	// Convenience functions
	buf.WriteString("async def create_schema(conn: AsyncConnection) -> ExecutionResult:\n")
	buf.WriteString("    \"\"\"Execute all DDL, failing on conflicts.\"\"\"\n")
	buf.WriteString("    return await execute(conn, idempotent=False)\n\n\n")

	buf.WriteString("async def ensure_schema(conn: AsyncConnection) -> ExecutionResult:\n")
	buf.WriteString("    \"\"\"Execute all DDL idempotently, safe to run on existing databases.\"\"\"\n")
	buf.WriteString("    return await execute(conn, idempotent=True)\n")

	return buf.Bytes()
}

// pythonBool returns a Python bool literal.
func pythonBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// Generate produces a Python file with all DDL statements as data tuples.
// This is the single-file Generator interface method.
func (g *PythonDDLGenerator) Generate(schema *model.Schema) ([]byte, []diagnostic.Diagnostic) {
	tuples, tables, diags := buildTuples(schema)
	return renderDDLFile(tuples, tables), diags
}

// GenerateFiles implements MultiFileGenerator, producing two files:
// schema_ddl.py (tuples) and schema_executor.py (section executor).
// When SplitMode is set, produces split output instead.
func (g *PythonDDLGenerator) GenerateFiles(schema *model.Schema) (map[string][]byte, []diagnostic.Diagnostic) {
	switch g.SplitMode {
	case SplitModeFaceted:
		return g.generateFacetedFiles(schema)
	case SplitModeSelfContained:
		return nil, []diagnostic.Diagnostic{{
			Severity: diagnostic.Error,
			Message:  "self-contained split mode not yet implemented",
		}}
	default:
		// SplitModeNone: default two-file output.
		tuples, tables, diags := buildTuples(schema)
		sections := buildSections(tuples)

		files := map[string][]byte{
			"schema_ddl.py":      renderDDLFile(tuples, tables),
			"schema_executor.py": renderExecutorFile(sections),
		}
		return files, diags
	}
}

// facetKind classifies a tuple into one of four facet categories.
const (
	facetExtensions  = "extensions"
	facetTypes       = "types"
	facetTables      = "tables"
	facetPostTables  = "post_tables"
)

// tupleFacet returns the facet category for a tuple.
func tupleFacet(t *ddlTuple) string {
	switch t.Kind {
	case "schema", "extension":
		return facetExtensions
	case "append_only_trigger":
		if t.Table == "" {
			return facetExtensions
		}
		return facetTables
	case "enum", "domain", "composite", "sequence":
		return facetTypes
	case "view", "materialized_view", "function":
		return facetPostTables
	case "comment":
		// Route comments by phase: phases 14-16 are post-table objects.
		if t.Phase >= 14 {
			return facetPostTables
		}
		// Sequence comments have no Table but belong with types.
		if t.Table == "" {
			return facetTypes
		}
		return facetTables
	case "index":
		// Materialized view indexes are at phase 15.
		if t.Phase == 15 {
			return facetPostTables
		}
		return facetTables
	default:
		// table, partition, partman, fk, unique, check, exclusion,
		// statistics, owner, rls_enable, rls_force, policy, trigger
		return facetTables
	}
}

// sourceBaseName extracts the base name from a source file path, stripping
// the .toml extension. Returns empty string for empty input.
func sourceBaseName(sourceFile string) string {
	if sourceFile == "" {
		return ""
	}
	base := filepath.Base(sourceFile)
	if strings.HasSuffix(base, ".toml") {
		base = base[:len(base)-5]
	}
	return base
}

// renderFacetFile renders a Python file for a faceted subset of tuples.
// If tables is non-empty, a TABLE_NAMES constant is emitted.
func renderFacetFile(tuples []ddlTuple, tables []model.Table) []byte {
	var buf bytes.Buffer
	buf.WriteString("# Code generated by pgdesign -- do not edit.\n\n")
	buf.WriteString("from typing import Final\n\n")
	writeDDLStmtDef(&buf)
	buf.WriteString("STATEMENTS: Final[list[DDLStmt]] = [\n")
	for _, t := range tuples {
		writeDDLStmt(&buf, t)
	}
	buf.WriteString("]\n")

	if len(tables) > 0 {
		buf.WriteString("\nTABLE_NAMES: Final[tuple[str, ...]] = (")
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
	}

	return buf.Bytes()
}

// generateFacetedFiles produces per-source-file output: extensions.py, types.py,
// tables_<source>.py (one per TOML source file), and post_tables.py.
func (g *PythonDDLGenerator) generateFacetedFiles(schema *model.Schema) (map[string][]byte, []diagnostic.Diagnostic) {
	tuples, tables, diags := buildTuples(schema)

	// Partition tuples into facets.
	var extensionTuples, typeTuples, postTableTuples []ddlTuple
	// Map from source file path to table tuples.
	tablesBySource := make(map[string][]ddlTuple)
	// Track ordering of source files.
	var sourceOrder []string
	sourceSeen := make(map[string]bool)

	for i := range tuples {
		t := &tuples[i]
		facet := tupleFacet(t)
		switch facet {
		case facetExtensions:
			extensionTuples = append(extensionTuples, *t)
		case facetTypes:
			typeTuples = append(typeTuples, *t)
		case facetPostTables:
			postTableTuples = append(postTableTuples, *t)
		case facetTables:
			src := t.SourceFile
			if !sourceSeen[src] {
				sourceSeen[src] = true
				sourceOrder = append(sourceOrder, src)
			}
			tablesBySource[src] = append(tablesBySource[src], *t)
		}
	}

	// Derive base names and check for collisions.
	baseToSource := make(map[string]string)
	for _, src := range sourceOrder {
		base := sourceBaseName(src)
		if base == "" {
			base = "unknown"
		}
		if existing, ok := baseToSource[base]; ok && existing != src {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Message:  fmt.Sprintf("faceted output: source files %q and %q produce the same base name %q", existing, src, base),
			})
			return nil, diags
		}
		baseToSource[base] = src
	}

	// Build table lists per source file for TABLE_NAMES.
	tablesBySourceFile := make(map[string][]model.Table)
	for _, tbl := range tables {
		tablesBySourceFile[tbl.SourceFile] = append(tablesBySourceFile[tbl.SourceFile], tbl)
	}

	files := make(map[string][]byte)

	// Track modules in phase order for executor imports.
	var modules []facetModule

	if len(extensionTuples) > 0 {
		files["extensions.py"] = renderFacetFile(extensionTuples, nil)
		modules = append(modules, facetModule{"extensions.py", "extensions", "_ext_stmts"})
	}
	if len(typeTuples) > 0 {
		files["types.py"] = renderFacetFile(typeTuples, nil)
		modules = append(modules, facetModule{"types.py", "types", "_types_stmts"})
	}
	for _, src := range sourceOrder {
		base := sourceBaseName(src)
		if base == "" {
			base = "unknown"
		}
		fileName := "tables_" + base + ".py"
		moduleName := "tables_" + base
		alias := "_" + moduleName + "_stmts"
		files[fileName] = renderFacetFile(tablesBySource[src], tablesBySourceFile[src])
		modules = append(modules, facetModule{fileName, moduleName, alias})
	}
	if len(postTableTuples) > 0 {
		files["post_tables.py"] = renderFacetFile(postTableTuples, nil)
		modules = append(modules, facetModule{"post_tables.py", "post_tables", "_post_stmts"})
	}

	// Generate schema_executor.py that imports and aggregates all faceted modules.
	sections := buildSections(tuples)
	files["schema_executor.py"] = renderFacetedExecutorFile(modules, sections)

	return files, diags
}

// renderFacetedExecutorFile generates a schema_executor.py that imports STATEMENTS
// from each faceted module, concatenates them, and exposes the same executor API
// as the monolithic renderExecutorFile.
func renderFacetedExecutorFile(modules []facetModule, sections []ddlSection) []byte {
	var buf bytes.Buffer

	buf.WriteString("# Code generated by pgdesign -- do not edit.\n\n")
	buf.WriteString("from __future__ import annotations\n\n")
	buf.WriteString("from collections import namedtuple\n")
	buf.WriteString("from dataclasses import dataclass, field\n")
	buf.WriteString("from typing import Protocol, Sequence, runtime_checkable\n\n")

	// Import STATEMENTS from each faceted module.
	for _, m := range modules {
		buf.WriteString(fmt.Sprintf("from .%s import STATEMENTS as %s\n", m.moduleName, m.alias))
	}
	buf.WriteString("\n")

	// Concatenate all STATEMENTS lists.
	if len(modules) > 0 {
		buf.WriteString("_ALL_STMTS = ")
		for i, m := range modules {
			if i > 0 {
				buf.WriteString(" + ")
			}
			buf.WriteString(m.alias)
		}
		buf.WriteString("\n")
	} else {
		buf.WriteString("_ALL_STMTS = []\n")
	}
	buf.WriteString("\n\n")

	// AsyncConnection protocol
	buf.WriteString("@runtime_checkable\n")
	buf.WriteString("class AsyncConnection(Protocol):\n")
	buf.WriteString("    async def execute(self, query: str) -> None: ...\n")
	buf.WriteString("    async def fetch(self, query: str) -> list[dict]: ...\n")
	buf.WriteString("    async def transaction(self) -> AsyncTransaction: ...\n\n\n")

	buf.WriteString("@runtime_checkable\n")
	buf.WriteString("class AsyncTransaction(Protocol):\n")
	buf.WriteString("    async def __aenter__(self) -> AsyncTransaction: ...\n")
	buf.WriteString("    async def __aexit__(self, exc_type, exc_val, exc_tb) -> None: ...\n")
	buf.WriteString("    async def execute(self, query: str) -> None: ...\n\n\n")

	// DDLOp namedtuple
	buf.WriteString("DDLOp = namedtuple(\"DDLOp\", [\"sql\", \"idempotent_sql\", \"name\"])\n\n\n")

	// Section dataclass
	buf.WriteString("@dataclass(frozen=True)\n")
	buf.WriteString("class Section:\n")
	buf.WriteString("    kind: str\n")
	buf.WriteString("    ops: list[DDLOp] = field(default_factory=list)\n")
	buf.WriteString("    transactional: bool = True\n\n")

	// Existence check queries by kind
	buf.WriteString("    async def exists(self, conn: AsyncConnection, op: DDLOp) -> bool:\n")
	buf.WriteString("        \"\"\"Check if the object described by op already exists.\"\"\"\n")
	buf.WriteString("        checkers = {\n")
	buf.WriteString("            \"schemas\": \"SELECT 1 FROM information_schema.schemata WHERE schema_name = '{}'\",\n")
	buf.WriteString("            \"extensions\": \"SELECT 1 FROM pg_extension WHERE extname = '{}'\",\n")
	buf.WriteString("            \"types\": \"SELECT 1 FROM pg_type WHERE typname = '{}'\",\n")
	buf.WriteString("            \"tables\": \"SELECT 1 FROM information_schema.tables WHERE table_name = '{}'\",\n")
	buf.WriteString("            \"indexes\": \"SELECT 1 FROM pg_indexes WHERE indexname = '{}'\",\n")
	buf.WriteString("            \"views\": \"SELECT 1 FROM information_schema.views WHERE table_name = '{}'\",\n")
	buf.WriteString("            \"materialized_views\": \"SELECT 1 FROM pg_matviews WHERE matviewname = '{}'\",\n")
	buf.WriteString("            \"functions\": \"SELECT 1 FROM pg_proc WHERE proname = '{}'\",\n")
	buf.WriteString("            \"triggers\": \"SELECT 1 FROM pg_trigger WHERE tgname = '{}'\",\n")
	buf.WriteString("            \"policies\": \"SELECT 1 FROM pg_policies WHERE policyname = '{}'\",\n")
	buf.WriteString("            \"foreign_keys\": \"SELECT 1 FROM pg_constraint WHERE conname = '{}' AND contype = 'f'\",\n")
	buf.WriteString("            \"unique_constraints\": \"SELECT 1 FROM pg_constraint WHERE conname = '{}' AND contype = 'u'\",\n")
	buf.WriteString("            \"check_constraints\": \"SELECT 1 FROM pg_constraint WHERE conname = '{}' AND contype = 'c'\",\n")
	buf.WriteString("        }\n")
	buf.WriteString("        query_template = checkers.get(self.kind)\n")
	buf.WriteString("        if query_template is None:\n")
	buf.WriteString("            return False\n")
	buf.WriteString("        escaped = op.name.replace(\"'\", \"''\")\n")
	buf.WriteString("        rows = await conn.fetch(query_template.format(escaped))\n")
	buf.WriteString("        return len(rows) > 0\n\n\n")

	// ExecutionResult
	buf.WriteString("@dataclass\n")
	buf.WriteString("class ExecutionResult:\n")
	buf.WriteString("    executed: list[tuple[str, str]] = field(default_factory=list)  # (section_kind, op_name)\n")
	buf.WriteString("    skipped: list[tuple[str, str]] = field(default_factory=list)\n")
	buf.WriteString("    errors: list[tuple[str, str, str]] = field(default_factory=list)  # (section_kind, op_name, error)\n\n\n")

	// VerifyResult
	buf.WriteString("@dataclass\n")
	buf.WriteString("class VerifyResult:\n")
	buf.WriteString("    present: list[tuple[str, str]] = field(default_factory=list)  # (section_kind, op_name)\n")
	buf.WriteString("    missing: list[tuple[str, str]] = field(default_factory=list)\n\n\n")

	// Build SECTIONS from _ALL_STMTS at module level.
	// We emit the sections statically (same as monolithic) since the tuples are
	// known at generation time.
	buf.WriteString("SECTIONS: list[Section] = [\n")
	for _, sec := range sections {
		buf.WriteString(fmt.Sprintf("    Section(\n        kind=%q,\n        transactional=%s,\n        ops=[\n",
			sec.kind, pythonBool(sec.transactional)))
		for _, t := range sec.tuples {
			sqlPy := pythonEscapeStr(t.SQL)
			idempotentPy := "None"
			if t.IdempotentSQL != "" {
				idempotentPy = pythonEscapeStr(t.IdempotentSQL)
			}
			namePy := "None"
			if t.Name != "" {
				namePy = fmt.Sprintf("%q", t.Name)
			}
			buf.WriteString(fmt.Sprintf("            DDLOp(%s, %s, %s),\n", sqlPy, idempotentPy, namePy))
		}
		buf.WriteString("        ],\n    ),\n")
	}
	buf.WriteString("]\n\n\n")

	// execute function
	buf.WriteString("async def execute(\n")
	buf.WriteString("    conn: AsyncConnection,\n")
	buf.WriteString("    sections: Sequence[str] | None = None,\n")
	buf.WriteString("    idempotent: bool = True,\n")
	buf.WriteString("    dry_run: bool = False,\n")
	buf.WriteString(") -> ExecutionResult:\n")
	buf.WriteString("    \"\"\"Execute DDL sections.\n\n")
	buf.WriteString("    Two-phase execution: transactional sections run inside a single\n")
	buf.WriteString("    transaction, non-transactional sections run outside afterward.\n")
	buf.WriteString("    \"\"\"\n")
	buf.WriteString("    result = ExecutionResult()\n")
	buf.WriteString("    selected = [s for s in SECTIONS if sections is None or s.kind in sections]\n")
	buf.WriteString("    transactional = [s for s in selected if s.transactional]\n")
	buf.WriteString("    non_transactional = [s for s in selected if not s.transactional]\n\n")
	buf.WriteString("    # Phase 1: transactional ops in a single transaction.\n")
	buf.WriteString("    if transactional and not dry_run:\n")
	buf.WriteString("        async with conn.transaction() as tx:\n")
	buf.WriteString("            for sec in transactional:\n")
	buf.WriteString("                for op in sec.ops:\n")
	buf.WriteString("                    stmt = op.idempotent_sql if idempotent and op.idempotent_sql else op.sql\n")
	buf.WriteString("                    try:\n")
	buf.WriteString("                        await tx.execute(stmt)\n")
	buf.WriteString("                        result.executed.append((sec.kind, op.name))\n")
	buf.WriteString("                    except Exception as e:\n")
	buf.WriteString("                        result.errors.append((sec.kind, op.name, str(e)))\n")
	buf.WriteString("                        raise\n")
	buf.WriteString("    elif transactional and dry_run:\n")
	buf.WriteString("        for sec in transactional:\n")
	buf.WriteString("            for op in sec.ops:\n")
	buf.WriteString("                result.executed.append((sec.kind, op.name))\n\n")
	buf.WriteString("    # Phase 2: non-transactional ops outside any transaction.\n")
	buf.WriteString("    for sec in non_transactional:\n")
	buf.WriteString("        for op in sec.ops:\n")
	buf.WriteString("            stmt = op.idempotent_sql if idempotent and op.idempotent_sql else op.sql\n")
	buf.WriteString("            if not dry_run:\n")
	buf.WriteString("                try:\n")
	buf.WriteString("                    await conn.execute(stmt)\n")
	buf.WriteString("                    result.executed.append((sec.kind, op.name))\n")
	buf.WriteString("                except Exception as e:\n")
	buf.WriteString("                    result.errors.append((sec.kind, op.name, str(e)))\n")
	buf.WriteString("            else:\n")
	buf.WriteString("                result.executed.append((sec.kind, op.name))\n\n")
	buf.WriteString("    return result\n\n\n")

	// verify function
	buf.WriteString("async def verify(conn: AsyncConnection) -> VerifyResult:\n")
	buf.WriteString("    \"\"\"Check which ops across all sections exist in the database.\"\"\"\n")
	buf.WriteString("    result = VerifyResult()\n")
	buf.WriteString("    for sec in SECTIONS:\n")
	buf.WriteString("        for op in sec.ops:\n")
	buf.WriteString("            if await sec.exists(conn, op):\n")
	buf.WriteString("                result.present.append((sec.kind, op.name))\n")
	buf.WriteString("            else:\n")
	buf.WriteString("                result.missing.append((sec.kind, op.name))\n")
	buf.WriteString("    return result\n\n\n")

	// Convenience functions
	buf.WriteString("async def create_schema(conn: AsyncConnection) -> ExecutionResult:\n")
	buf.WriteString("    \"\"\"Execute all DDL, failing on conflicts.\"\"\"\n")
	buf.WriteString("    return await execute(conn, idempotent=False)\n\n\n")

	buf.WriteString("async def ensure_schema(conn: AsyncConnection) -> ExecutionResult:\n")
	buf.WriteString("    \"\"\"Execute all DDL idempotently, safe to run on existing databases.\"\"\"\n")
	buf.WriteString("    return await execute(conn, idempotent=True)\n")

	return buf.Bytes()
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
func collectPartitionTuples(schemaName, parentTable, sourceFile string, children []model.PartitionSpec, tuples *[]ddlTuple) {
	for i := range children {
		child := &children[i]
		*tuples = append(*tuples, ddlTuple{
			SQL:           sql.CreatePartitionOf(schemaName, child, parentTable, false),
			IdempotentSQL: sql.CreatePartitionOf(schemaName, child, parentTable, true),
			Kind:          "partition",
			Name:          child.Name,
			Table:         child.Name,
			Phase:         5,
			Transactional: true,
			SourceFile:    sourceFile,
		})
		if len(child.Children) > 0 {
			collectPartitionTuples(schemaName, child.Name, sourceFile, child.Children, tuples)
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
