// Package generate transforms a resolved model.Schema into PostgreSQL DDL output including tables, views, materialized views, functions, and triggers.
package generate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/sql"
)

// Options controls the DDL output behavior.
type Options struct {
	Idempotent      bool
	IncludeComments bool
	Format          string // "sql", "json", "d2", "svg", "doc", "graphql"
	TypeRegistry    *semtype.Registry      // optional: enables state machine trigger generation and D2 state diagrams
	ExtRegistry     *extregistry.Registry  // optional: resolves extension DDL names (e.g. pgvector -> vector)
}

// Generate produces DDL output for the given schema according to opts.
func Generate(schema *model.Schema, opts Options) (string, []diagnostic.Diagnostic, error) {
	switch strings.ToLower(opts.Format) {
	case "sql", "":
		out, diags := generateSQL(schema, opts)
		return out, diags, nil
	case "d2":
		return GenerateD2(schema, opts.TypeRegistry), nil, nil
	case "json":
		out, err := generateJSON(schema)
		return out, nil, err
	case "svg":
		d2Source := GenerateD2(schema, opts.TypeRegistry)
		svg, err := RenderSVG(d2Source)
		if err != nil {
			return "", nil, fmt.Errorf("svg render: %w", err)
		}
		return string(svg), nil, nil
	case "doc":
		return generateDoc(schema), nil, nil
	case "graphql":
		return generateGraphQL(schema), nil, nil
	default:
		return "", nil, fmt.Errorf("unsupported format: %s", opts.Format)
	}
}

// generateJSON produces pretty-printed JSON output of the full schema. The
// schema is already in canonical order (model.Canonicalize runs at build time),
// so it is marshalled directly.
func generateJSON(schema *model.Schema) (string, error) {
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "", fmt.Errorf("json marshal: %w", err)
	}
	return string(data), nil
}

func generateSQL(schema *model.Schema, opts Options) (string, []diagnostic.Diagnostic) {
	var sections []string
	var diags []diagnostic.Diagnostic

	// 1. CREATE SCHEMA
	// In multi-schema mode, schema.Name is empty; emit CREATE SCHEMA for each
	// distinct table schema instead.
	if schema.Name != "" {
		sections = append(sections, sql.CreateSchema(schema.Name, opts.Idempotent))
	} else {
		seen := make(map[string]bool)
		var schemaStmts []string
		for _, t := range schema.Tables {
			if t.Schema != "" && !seen[t.Schema] {
				seen[t.Schema] = true
				schemaStmts = append(schemaStmts, sql.CreateSchema(t.Schema, opts.Idempotent))
			}
		}
		for _, e := range schema.Enums {
			if e.Schema != "" && !seen[e.Schema] {
				seen[e.Schema] = true
				schemaStmts = append(schemaStmts, sql.CreateSchema(e.Schema, opts.Idempotent))
			}
		}
		for _, ct := range schema.CompositeTypes {
			if ct.Schema != "" && !seen[ct.Schema] {
				seen[ct.Schema] = true
				schemaStmts = append(schemaStmts, sql.CreateSchema(ct.Schema, opts.Idempotent))
			}
		}
		if len(schemaStmts) > 0 {
			sections = append(sections, strings.Join(schemaStmts, "\n"))
		}
	}

	// 1b. CREATE SCHEMA partman (before CREATE EXTENSION pg_partman which needs it)
	if hasExtension(schema, "pg_partman") {
		sections = append(sections, sql.CreateSchema("partman", true))
	}

	// 2. CREATE EXTENSION
	if len(schema.Extensions) > 0 {
		var extStmts []string
		for _, ext := range schema.Extensions {
			ddlName := resolveExtDDLName(opts.ExtRegistry, ext)
			if ext == "pg_partman" {
				extStmts = append(extStmts, sql.CreateExtensionInSchema(ddlName, "partman", opts.Idempotent))
			} else {
				extStmts = append(extStmts, sql.CreateExtension(ddlName, opts.Idempotent))
			}
		}
		sections = append(sections, strings.Join(extStmts, "\n"))
	}

	// 2b. CREATE SEQUENCE
	if len(schema.Sequences) > 0 {
		var seqStmts []string
		for i := range schema.Sequences {
			seqStmts = append(seqStmts, sql.CreateSequence(schema.Sequences[i].Schema, &schema.Sequences[i], opts.Idempotent))
		}
		sections = append(sections, strings.Join(seqStmts, "\n"))
	}

	// 3. CREATE TYPE ... AS ENUM
	if len(schema.Enums) > 0 {
		var enumStmts []string
		for _, e := range schema.Enums {
			enumStmts = append(enumStmts, sql.CreateEnum(e.Schema, e.Name, e.Values, opts.Idempotent))
		}
		sections = append(sections, strings.Join(enumStmts, "\n"))
	}

	// 3b. CREATE DOMAIN
	if len(schema.Domains) > 0 {
		var domainStmts []string
		for _, d := range schema.Domains {
			domainStmts = append(domainStmts, sql.CreateDomain(d.Schema, d, opts.Idempotent))
		}
		sections = append(sections, strings.Join(domainStmts, "\n"))
	}

	// 3c. CREATE TYPE AS (composite types)
	if len(schema.CompositeTypes) > 0 {
		var ctStmts []string
		for _, ct := range schema.CompositeTypes {
			ctStmts = append(ctStmts, sql.CreateCompositeType(ct.Schema, ct, opts.Idempotent))
		}
		sections = append(sections, strings.Join(ctStmts, "\n"))
	}

	tables := schema.TableOrder()

	// 4. CREATE TABLE
	if len(tables) > 0 {
		var tableStmts []string
		for i := range tables {
			tableStmts = append(tableStmts, sql.CreateTable(&tables[i], tables[i].Schema, opts.Idempotent, schema.PGVersion, schema.Enums, schema.Domains))
		}
		sections = append(sections, strings.Join(tableStmts, "\n\n"))
	}

	// 4b. ALTER TABLE ADD COLUMN IF NOT EXISTS (idempotent column guards)
	if opts.Idempotent && len(tables) > 0 {
		var colStmts []string
		for i := range tables {
			t := &tables[i]
			for _, col := range t.Columns {
				colStmts = append(colStmts, sql.AlterTableAddColumnIfNotExists(t.Name, t.Schema, col, schema.PGVersion, schema.Enums, schema.Domains))
			}
		}
		if len(colStmts) > 0 {
			sections = append(sections, strings.Join(colStmts, "\n"))
		}
	}

	// 5. CREATE TABLE ... PARTITION OF (child partitions)
	var partStmts []string
	for i := range tables {
		t := &tables[i]
		if t.Partitioning != nil && len(t.Partitioning.Children) > 0 {
			collectPartitionChildren(t.Schema, t.Name, t.Partitioning.Children, opts.Idempotent, &partStmts)
		}
	}
	if len(partStmts) > 0 {
		sections = append(sections, strings.Join(partStmts, "\n"))
	}

	// 5b. pg_partman configuration
	var partmanStmts []string
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
			partmanStmts = append(partmanStmts,
				sql.CreatePartmanParent(t.Schema, t.Name, t.Partitioning.Columns[0],
					t.Maintenance.Interval, t.Maintenance.Premake))
			if t.Maintenance.Retention != "" {
				partmanStmts = append(partmanStmts,
					sql.UpdatePartmanConfig(t.Schema, t.Name,
						t.Maintenance.Retention, t.Maintenance.RetentionKeepTable))
			}
			if t.Maintenance.Schedule != "" {
				partmanStmts = append(partmanStmts,
					sql.PartmanRunMaintenanceCron(t.Maintenance.Schedule))
			}
		}
	}
	if len(partmanStmts) > 0 {
		sections = append(sections, strings.Join(partmanStmts, "\n\n"))
	}

	// 6. ALTER TABLE ADD CONSTRAINT ... FOREIGN KEY
	var fkStmts []string
	for i := range tables {
		t := &tables[i]
		for _, fk := range t.FKs {
			fkCopy := fk
			fkStmts = append(fkStmts, sql.AlterTableAddFK(t.Schema, t, &fkCopy, opts.Idempotent))
		}
	}
	if len(fkStmts) > 0 {
		sections = append(sections, strings.Join(fkStmts, "\n"))
	}

	// 7. ALTER TABLE ADD CONSTRAINT ... UNIQUE
	var uqStmts []string
	for i := range tables {
		t := &tables[i]
		for _, uq := range t.Uniques {
			uqCopy := uq
			uqStmts = append(uqStmts, sql.AlterTableAddUnique(t.Schema, t.Name, &uqCopy, opts.Idempotent))
		}
	}
	if len(uqStmts) > 0 {
		sections = append(sections, strings.Join(uqStmts, "\n"))
	}

	// 8. ALTER TABLE ADD CONSTRAINT ... CHECK
	var ckStmts []string
	for i := range tables {
		t := &tables[i]
		for _, ck := range t.Checks {
			ckCopy := ck
			ckStmts = append(ckStmts, sql.AlterTableAddCheck(t.Schema, t.Name, &ckCopy, opts.Idempotent))
		}
	}
	if len(ckStmts) > 0 {
		sections = append(sections, strings.Join(ckStmts, "\n"))
	}

	// 8b. ALTER TABLE ADD CONSTRAINT ... EXCLUDE
	var exclStmts []string
	for i := range tables {
		t := &tables[i]
		for _, exc := range t.Exclusions {
			excCopy := exc
			exclStmts = append(exclStmts, sql.AlterTableAddExclusion(t.Schema, t.Name, &excCopy, opts.Idempotent))
		}
	}
	if len(exclStmts) > 0 {
		sections = append(sections, strings.Join(exclStmts, "\n"))
	}

	// 9. CREATE INDEX (explicit + auto-FK)
	var idxStmts []string
	for i := range tables {
		t := &tables[i]
		for _, idx := range t.Indexes {
			idxCopy := idx
			idxStmts = append(idxStmts, sql.CreateIndex(t.Schema, &idxCopy, t.Name, opts.Idempotent, false))
		}
	}
	if len(idxStmts) > 0 {
		sections = append(sections, strings.Join(idxStmts, "\n"))
	}

	// 9b. Append-only triggers (shared function + per-table triggers)
	{
		// Collect schemas that have append-only tables.
		appendOnlySchemas := make(map[string]bool)
		for i := range tables {
			if tables[i].AppendOnly {
				appendOnlySchemas[tables[i].Schema] = true
			}
		}
		if len(appendOnlySchemas) > 0 {
			var triggerStmts []string
			// Emit shared function once per schema.
			// Sort schema names for deterministic output.
			var schemaNames []string
			for s := range appendOnlySchemas {
				schemaNames = append(schemaNames, s)
			}
			sort.Strings(schemaNames)
			for _, s := range schemaNames {
				triggerStmts = append(triggerStmts, sql.CreateDenyMutationFunction(s))
			}
			// Emit per-table triggers.
			for i := range tables {
				t := &tables[i]
				if t.AppendOnly {
					triggerStmts = append(triggerStmts, sql.CreateAppendOnlyTrigger(t.Schema, t.Name, opts.Idempotent, schema.PGVersion))
				}
			}
			sections = append(sections, strings.Join(triggerStmts, "\n"))
		}
	}

	// 9c. State machine enforcement triggers
	if opts.TypeRegistry != nil {
		var smTriggerStmts []string
		for i := range tables {
			t := &tables[i]
			for _, col := range t.Columns {
				if !model.IsStateMachineColumn(col, opts.TypeRegistry) {
					continue
				}
				td, err := opts.TypeRegistry.Resolve(col.SemanticTypeName)
				if err != nil || !td.EnforceTrigger {
					continue
				}
				smTriggerStmts = append(smTriggerStmts,
					sql.CreateStateMachineTriggerFunction(t.Schema, t.Name, col.Name, td.Transitions))
				smTriggerStmts = append(smTriggerStmts,
					sql.CreateStateMachineTrigger(t.Schema, t.Name, col.Name, opts.Idempotent, schema.PGVersion))
			}
		}
		if len(smTriggerStmts) > 0 {
			sections = append(sections, strings.Join(smTriggerStmts, "\n"))
		}
	}

	// 10. COMMENT ON TABLE + COMMENT ON COLUMN
	if opts.IncludeComments {
		var commentStmts []string
		for i := range tables {
			t := &tables[i]
			if t.Comment != "" {
				qualified := sql.QualifiedName(t.Schema, t.Name)
				commentStmts = append(commentStmts, sql.CommentOn("TABLE", qualified, t.Comment))
			}
			for _, col := range t.Columns {
				if col.Comment != "" {
					qualified := sql.QualifiedName(t.Schema, t.Name) + "." + sql.QuoteIdent(col.Name)
					commentStmts = append(commentStmts, sql.CommentOn("COLUMN", qualified, col.Comment))
				}
			}
		}
		// Sequence comments
		for _, seq := range schema.Sequences {
			if seq.Comment != "" {
				qualified := sql.QualifiedName(seq.Schema, seq.Name)
				commentStmts = append(commentStmts, sql.CommentOn("SEQUENCE", qualified, seq.Comment))
			}
		}
		if len(commentStmts) > 0 {
			sections = append(sections, strings.Join(commentStmts, "\n"))
		}
	}

	// 10b. ALTER TABLE ALTER COLUMN SET STATISTICS
	var statsStmts []string
	for i := range tables {
		t := &tables[i]
		for _, col := range t.Columns {
			if col.Statistics != nil {
				qualified := sql.QualifiedName(t.Schema, t.Name)
				statsStmts = append(statsStmts, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET STATISTICS %d;",
					qualified, sql.QuoteIdent(col.Name), *col.Statistics))
			}
		}
	}
	if len(statsStmts) > 0 {
		sections = append(sections, strings.Join(statsStmts, "\n"))
	}

	// 11. ALTER TABLE OWNER TO
	var ownerStmts []string
	for i := range tables {
		t := &tables[i]
		if t.Owner != "" {
			ownerStmts = append(ownerStmts, sql.AlterTableOwner(t.Schema, t.Name, t.Owner))
		}
	}
	if len(ownerStmts) > 0 {
		sections = append(sections, strings.Join(ownerStmts, "\n"))
	}

	// 12. ALTER TABLE ENABLE ROW LEVEL SECURITY
	var enableRLSStmts []string
	for i := range tables {
		t := &tables[i]
		if t.EnableRLS {
			enableRLSStmts = append(enableRLSStmts, sql.AlterTableEnableRLS(t.Schema, t.Name))
		}
	}
	if len(enableRLSStmts) > 0 {
		sections = append(sections, strings.Join(enableRLSStmts, "\n"))
	}

	// 12b. ALTER TABLE FORCE ROW LEVEL SECURITY
	var forceRLSStmts []string
	for i := range tables {
		t := &tables[i]
		if t.ForceRLS {
			forceRLSStmts = append(forceRLSStmts, sql.AlterTableForceRLS(t.Schema, t.Name))
		}
	}
	if len(forceRLSStmts) > 0 {
		sections = append(sections, strings.Join(forceRLSStmts, "\n"))
	}

	// 13. CREATE POLICY
	var policyStmts []string
	for i := range tables {
		t := &tables[i]
		for _, p := range t.Policies {
			policyStmts = append(policyStmts, sql.CreatePolicy(t.Schema, t.Name, p, opts.Idempotent, schema.PGVersion))
		}
	}
	if len(policyStmts) > 0 {
		sections = append(sections, strings.Join(policyStmts, "\n"))
	}

	// 14. CREATE VIEW (canonical order: topologically sorted by DependsOn at build time)
	if len(schema.Views) > 0 {
		var viewStmts []string
		schemaName := schema.Name
		for i := range schema.Views {
			v := &schema.Views[i]
			if v.Schema != "" {
				schemaName = v.Schema
			}
			viewStmts = append(viewStmts, sql.CreateView(schemaName, v, opts.Idempotent))
			if v.Comment != "" && opts.IncludeComments {
				viewStmts = append(viewStmts, sql.CommentOn("VIEW", sql.QualifiedName(schemaName, v.Name), v.Comment))
			}
		}
		if len(viewStmts) > 0 {
			sections = append(sections, strings.Join(viewStmts, "\n"))
		}
	}

	// 15. CREATE MATERIALIZED VIEW (canonical order: topologically sorted by DependsOn at build time)
	if len(schema.MaterializedViews) > 0 {
		var mvStmts []string
		schemaName := schema.Name
		for i := range schema.MaterializedViews {
			mv := &schema.MaterializedViews[i]
			if mv.Schema != "" {
				schemaName = mv.Schema
			}
			mvStmts = append(mvStmts, sql.CreateMaterializedView(schemaName, mv, opts.Idempotent))
			if mv.Comment != "" && opts.IncludeComments {
				mvStmts = append(mvStmts, sql.CommentOn("MATERIALIZED VIEW", sql.QualifiedName(schemaName, mv.Name), mv.Comment))
			}
			for j := range mv.Indexes {
				idx := &mv.Indexes[j]
				mvStmts = append(mvStmts, sql.CreateIndex(schemaName, idx, mv.Name, opts.Idempotent, false))
			}
		}
		if len(mvStmts) > 0 {
			sections = append(sections, strings.Join(mvStmts, "\n"))
		}
	}

	// 16. CREATE FUNCTION / CREATE PROCEDURE (canonical order: topologically sorted by DependsOn at build time)
	if len(schema.Functions) > 0 {
		var funcStmts []string
		schemaName := schema.Name
		for i := range schema.Functions {
			f := &schema.Functions[i]
			if f.Schema != "" {
				schemaName = f.Schema
			}
			funcStmts = append(funcStmts, sql.CreateFunction(schemaName, *f))
			if f.Comment != "" && opts.IncludeComments {
				kind := "FUNCTION"
				if f.IsProc {
					kind = "PROCEDURE"
				}
				funcStmts = append(funcStmts, sql.CommentOn(kind, sql.QualifiedName(schemaName, f.Name), f.Comment))
			}
		}
		if len(funcStmts) > 0 {
			sections = append(sections, strings.Join(funcStmts, "\n"))
		}
	}

	// 17. CREATE TRIGGER (user-defined triggers, excluding SM triggers)
	var triggerStmts []string
	for i := range tables {
		t := &tables[i]
		for _, trig := range t.Triggers {
			if strings.HasPrefix(trig.Name, "_pgdesign_sm_") {
				continue
			}
			triggerStmts = append(triggerStmts, sql.CreateTrigger(t.Schema, t.Name, trig, opts.Idempotent, schema.PGVersion))
		}
	}
	if len(triggerStmts) > 0 {
		sections = append(sections, strings.Join(triggerStmts, "\n"))
	}

	return strings.Join(sections, "\n\n") + "\n", diags
}

// collectPartitionChildren recursively emits CREATE TABLE ... PARTITION OF
// statements for all children in the partition tree. For sub-partitions, the
// parent is the child itself (supporting partitions of partitions).
func collectPartitionChildren(schemaName, parentTable string, children []model.PartitionSpec, idempotent bool, out *[]string) {
	for i := range children {
		child := &children[i]
		*out = append(*out, sql.CreatePartitionOf(schemaName, child, parentTable, idempotent))
		// Recurse for sub-partitions (partitions of partitions).
		if len(child.Children) > 0 {
			collectPartitionChildren(schemaName, child.Name, child.Children, idempotent, out)
		}
	}
}

// hasExtension returns true if the schema declares the named extension.
// resolveExtDDLName returns the PostgreSQL DDL name for an extension.
// If a registry is provided, it delegates to ResolveDDLName; otherwise
// it returns the name unchanged (backward-compatible default).
func resolveExtDDLName(reg *extregistry.Registry, name string) string {
	if reg == nil {
		return name
	}
	return reg.ResolveDDLName(name)
}

func hasExtension(schema *model.Schema, name string) bool {
	for _, ext := range schema.Extensions {
		if ext == name {
			return true
		}
	}
	return false
}
