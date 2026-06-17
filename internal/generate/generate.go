// Package generate transforms a resolved model.Schema into PostgreSQL DDL output.
package generate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/graph"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sql"
)

// Options controls the DDL output behavior.
type Options struct {
	Idempotent      bool
	IncludeComments bool
	Format          string // "sql", "json", "d2", "svg", "doc"
	PGVersion       int
}

// Generate produces DDL output for the given schema according to opts.
func Generate(schema *model.Schema, opts Options) (string, []diagnostic.Diagnostic, error) {
	switch strings.ToLower(opts.Format) {
	case "sql", "":
		out, diags := generateSQL(schema, opts)
		return out, diags, nil
	case "d2":
		return GenerateD2(schema), nil, nil
	case "json":
		out, err := generateJSON(schema)
		return out, nil, err
	case "svg":
		d2Source := GenerateD2(schema)
		svg, err := RenderSVG(d2Source)
		if err != nil {
			return "", nil, fmt.Errorf("svg render: %w", err)
		}
		return string(svg), nil, nil
	case "doc":
		return generateDoc(schema), nil, nil
	default:
		return "", nil, fmt.Errorf("unsupported format: %s", opts.Format)
	}
}

// generateJSON produces pretty-printed JSON output of the full schema.
func generateJSON(schema *model.Schema) (string, error) {
	// Deep-enough copy so sorting doesn't mutate the original.
	s := *schema
	s.Enums = make([]model.Enum, len(schema.Enums))
	copy(s.Enums, schema.Enums)
	sort.Slice(s.Enums, func(i, j int) bool {
		return s.Enums[i].Name < s.Enums[j].Name
	})
	s.Domains = make([]model.Domain, len(schema.Domains))
	copy(s.Domains, schema.Domains)
	sort.Slice(s.Domains, func(i, j int) bool {
		return s.Domains[i].Name < s.Domains[j].Name
	})
	s.CompositeTypes = make([]model.CompositeType, len(schema.CompositeTypes))
	copy(s.CompositeTypes, schema.CompositeTypes)
	sort.Slice(s.CompositeTypes, func(i, j int) bool {
		return s.CompositeTypes[i].Name < s.CompositeTypes[j].Name
	})
	s.Sequences = make([]model.Sequence, len(schema.Sequences))
	copy(s.Sequences, schema.Sequences)
	sort.Slice(s.Sequences, func(i, j int) bool {
		return s.Sequences[i].Name < s.Sequences[j].Name
	})
	s.Tables = make([]model.Table, len(schema.Tables))
	copy(s.Tables, schema.Tables)
	for i := range s.Tables {
		s.Tables[i].FKs = sortedFKs(s.Tables[i].FKs)
		s.Tables[i].Indexes = sortedIndexes(s.Tables[i].Indexes)
		s.Tables[i].Uniques = sortedUniques(s.Tables[i].Uniques)
		s.Tables[i].Checks = sortedChecks(s.Tables[i].Checks)
		s.Tables[i].Exclusions = sortedExclusions(s.Tables[i].Exclusions)
		s.Tables[i].Policies = sortedPolicies(s.Tables[i].Policies)
	}
	s.Functions = make([]model.Function, len(schema.Functions))
	copy(s.Functions, schema.Functions)
	sort.Slice(s.Functions, func(i, j int) bool {
		return s.Functions[i].Name < s.Functions[j].Name
	})
	data, err := json.MarshalIndent(&s, "", "  ")
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

	// 2. CREATE EXTENSION
	if len(schema.Extensions) > 0 {
		var extStmts []string
		for _, ext := range schema.Extensions {
			extStmts = append(extStmts, sql.CreateExtension(ext, opts.Idempotent))
		}
		sections = append(sections, strings.Join(extStmts, "\n"))
	}

	// 2b. CREATE SEQUENCE
	if len(schema.Sequences) > 0 {
		var seqStmts []string
		for i := range schema.Sequences {
			seqStmts = append(seqStmts, sql.CreateSequence(schema.Sequences[i].Schema, &schema.Sequences[i]))
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
			domainStmts = append(domainStmts, sql.CreateDomain(d.Schema, d))
		}
		sections = append(sections, strings.Join(domainStmts, "\n"))
	}

	// 3c. CREATE TYPE AS (composite types)
	if len(schema.CompositeTypes) > 0 {
		var ctStmts []string
		for _, ct := range schema.CompositeTypes {
			ctStmts = append(ctStmts, sql.CreateCompositeType(ct.Schema, ct))
		}
		sections = append(sections, strings.Join(ctStmts, "\n"))
	}

	tables := schema.TableOrder()

	// 4. CREATE TABLE
	if len(tables) > 0 {
		var tableStmts []string
		for i := range tables {
			tableStmts = append(tableStmts, sql.CreateTable(&tables[i], tables[i].Schema, opts.Idempotent, opts.PGVersion, schema.Enums))
		}
		sections = append(sections, strings.Join(tableStmts, "\n\n"))
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
					t.Maintenance.Retention, t.Maintenance.Premake))
			if t.Maintenance.Retention != "" {
				partmanStmts = append(partmanStmts,
					sql.UpdatePartmanConfig(t.Schema, t.Name,
						t.Maintenance.Retention, t.Maintenance.RetentionKeepTable))
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
		fks := sortedFKs(t.FKs)
		for _, fk := range fks {
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
		uqs := sortedUniques(t.Uniques)
		for _, uq := range uqs {
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
		cks := sortedChecks(t.Checks)
		for _, ck := range cks {
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
		excls := sortedExclusions(t.Exclusions)
		for _, exc := range excls {
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
		idxs := sortedIndexes(t.Indexes)
		for _, idx := range idxs {
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
					triggerStmts = append(triggerStmts, sql.CreateAppendOnlyTrigger(t.Schema, t.Name))
				}
			}
			sections = append(sections, strings.Join(triggerStmts, "\n"))
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

	// 13. CREATE POLICY
	var policyStmts []string
	for i := range tables {
		t := &tables[i]
		policies := sortedPolicies(t.Policies)
		for _, p := range policies {
			policyStmts = append(policyStmts, sql.CreatePolicy(t.Schema, t.Name, p))
		}
	}
	if len(policyStmts) > 0 {
		sections = append(sections, strings.Join(policyStmts, "\n"))
	}

	// 14. CREATE VIEW (topologically sorted by DependsOn)
	if len(schema.Views) > 0 {
		sorted, err := topoSortViews(schema.Views)
		if err != nil {
			// Cycle in view dependencies -- emit in original order with a warning.
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
		var viewStmts []string
		if err != nil {
			viewStmts = append(viewStmts, "-- WARNING: dependency cycle detected; emitted in declaration order")
		}
		schemaName := schema.Name
		for i := range sorted {
			v := &sorted[i]
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

	// 15. CREATE MATERIALIZED VIEW (topologically sorted by DependsOn)
	if len(schema.MaterializedViews) > 0 {
		sorted, err := topoSortMaterializedViews(schema.MaterializedViews)
		if err != nil {
			// Cycle in materialized view dependencies -- emit in original order with a warning.
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
		var mvStmts []string
		if err != nil {
			mvStmts = append(mvStmts, "-- WARNING: dependency cycle detected; emitted in declaration order")
		}
		schemaName := schema.Name
		for i := range sorted {
			mv := &sorted[i]
			if mv.Schema != "" {
				schemaName = mv.Schema
			}
			mvStmts = append(mvStmts, sql.CreateMaterializedView(schemaName, mv))
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

	// 16. CREATE FUNCTION / CREATE PROCEDURE (topologically sorted by DependsOn)
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
		var funcStmts []string
		if err != nil {
			funcStmts = append(funcStmts, "-- WARNING: dependency cycle detected; emitted in declaration order")
		}
		schemaName := schema.Name
		for i := range sorted {
			f := &sorted[i]
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

	return strings.Join(sections, "\n\n") + "\n", diags
}

// sortedFKs returns FKs sorted alphabetically by name.
func sortedFKs(fks []model.FK) []model.FK {
	result := make([]model.FK, len(fks))
	copy(result, fks)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// sortedUniques returns unique constraints sorted alphabetically by name.
func sortedUniques(uqs []model.UniqueConstraint) []model.UniqueConstraint {
	result := make([]model.UniqueConstraint, len(uqs))
	copy(result, uqs)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// sortedChecks returns check constraints sorted alphabetically by name.
func sortedChecks(cks []model.CheckConstraint) []model.CheckConstraint {
	result := make([]model.CheckConstraint, len(cks))
	copy(result, cks)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// sortedExclusions returns exclusion constraints sorted alphabetically by name.
func sortedExclusions(excls []model.ExclusionConstraint) []model.ExclusionConstraint {
	sorted := make([]model.ExclusionConstraint, len(excls))
	copy(sorted, excls)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

// sortedIndexes returns indexes sorted alphabetically by name.
func sortedIndexes(idxs []model.Index) []model.Index {
	result := make([]model.Index, len(idxs))
	copy(result, idxs)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// sortedPolicies returns policies sorted alphabetically by name.
func sortedPolicies(pols []model.Policy) []model.Policy {
	result := make([]model.Policy, len(pols))
	copy(result, pols)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
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
func hasExtension(schema *model.Schema, name string) bool {
	for _, ext := range schema.Extensions {
		if ext == name {
			return true
		}
	}
	return false
}

// topoSortViews sorts views by DependsOn using Kahn's algorithm.
// Views that depend on other views come after their dependencies.
// Returns an error if a cycle is detected.
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

// topoSortMaterializedViews sorts materialized views by DependsOn using Kahn's algorithm.
// Materialized views that depend on other materialized views come after their dependencies.
// Returns an error if a cycle is detected.
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

// topoSortFunctions sorts functions by DependsOn using Kahn's algorithm.
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
