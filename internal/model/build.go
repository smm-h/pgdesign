package model

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/sqlparse"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// BuildOption customizes model construction. Options are the extension point for
// build inputs that come from OUTSIDE the schema TOML (e.g. the project's
// [imports] declarations, which live in pgdesign.toml). Existing callers pass no
// options and get the same behavior as before.
type BuildOption func(*buildOptions)

// buildOptions holds the resolved build inputs threaded through resolution.
type buildOptions struct {
	// imports maps a declared import alias to its target PostgreSQL schema
	// (the [imports.<alias>].schema config value). An `alias:table` FK ref_table
	// resolves to (imports[alias], table). A reference to an undeclared alias is
	// a hard build error.
	imports map[string]string
	// importedTables are the REFERENCE tables decoded from the vendored import
	// surface (imports/<alias>/), already stamped into their target schema. They
	// are placed on Schema.ImportedTables and folded into the derived resolution
	// structures (TablesByName, FKGraph) so imported-FK targets resolve, but are
	// never added to Schema.Tables (fail-closed — roadmap 7.3).
	importedTables []Table
}

// WithImports supplies the project's declared import aliases (alias -> target PG
// schema) so `alias:table` FK references resolve at build time (roadmap 7.1).
func WithImports(imports map[string]string) BuildOption {
	return func(o *buildOptions) { o.imports = imports }
}

// WithImportedTables supplies the REFERENCE tables decoded from the vendored
// import surface (roadmap 7.3). They populate Schema.ImportedTables and are
// unioned into TablesByName and the FKGraph so imported-FK targets resolve, but
// are kept out of Schema.Tables so every Tables-iterating consumer is fail-closed
// by omission.
func WithImportedTables(tables []Table) BuildOption {
	return func(o *buildOptions) { o.importedTables = tables }
}

func applyBuildOptions(opts []BuildOption) buildOptions {
	var o buildOptions
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// Build constructs a resolved Schema from raw parse output and a type registry.
// It returns the schema (possibly partial) and any diagnostics encountered.
func Build(raw *parse.RawSchema, reg *semtype.Registry, opts ...BuildOption) (*Schema, diagnostic.Diagnostics) {
	o := applyBuildOptions(opts)
	var diags diagnostic.Diagnostics

	schema := &Schema{
		Name:       raw.Meta.Schema,
		Extensions: raw.Meta.Extensions,
		PGVersion:  raw.Meta.Version,
	}

	// Phase 1: resolve
	tables, enums, compositeTypes, domains, resolveDiags := resolve(raw, reg, o.imports)
	diags = append(diags, resolveDiags...)
	schema.Enums = enums
	schema.CompositeTypes = compositeTypes
	schema.Domains = domains
	schema.Views = resolveViews(raw)
	schema.MaterializedViews = resolveMaterializedViews(raw)
	schema.Functions = resolveFunctions(raw)
	schema.Tables = tables
	schema.ImportedTables = o.importedTables

	// Phase 1b: resolve sequences (needs schema.Tables for owned_by validation).
	seqs, seqDiags := resolveSequences(raw, schema)
	diags = append(diags, seqDiags...)
	schema.Sequences = seqs

	// Phase 3: enrich (appends auto-FK indexes; must run before canonical sort).
	enrichDiags := enrich(schema)
	diags = append(diags, enrichDiags...)

	// Populate the first-class state-machine collection BEFORE Canonicalize so
	// it is canonicalized (name-sorted, transitions/From sorted) with everything
	// else.
	schema.StateMachines = resolveStateMachines(raw, reg)

	// Phase 4: canonicalize — ordering + derived structures (FKGraph, TablesByName).
	schema.Canonicalize()

	// Extract state machine transition maps from the registry (derived codegen
	// convenience; excluded from identity).
	schema.StateMachineTransitions = resolveStateMachineTransitions(raw, reg)

	// Validate and copy groups.
	groupDiags := resolveGroups(schema, raw.Groups)
	diags = append(diags, groupDiags...)

	// Partman maintenance requires pg_partman to be declared as an extension.
	diags = append(diags, validateMaintenanceExtension(schema)...)

	// Alias references are permitted ONLY in FK ref_table; reject them elsewhere.
	diags = append(diags, validateAliasScoping(schema, o.imports)...)

	return schema, diags
}

// validateMaintenanceExtension checks that every table declaring partman
// maintenance also declares the pg_partman extension. A silent skip when
// pg_partman is undeclared would leave the maintenance config inert.
func validateMaintenanceExtension(schema *Schema) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics
	hasPartman := false
	hasCron := false
	for _, ext := range schema.Extensions {
		switch ext {
		case "pg_partman":
			hasPartman = true
		case "pg_cron":
			hasCron = true
		}
	}
	for i := range schema.Tables {
		t := &schema.Tables[i]
		if t.Maintenance == nil {
			continue
		}
		if !hasPartman {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E010",
				Table:    t.Name,
				Message:  fmt.Sprintf("[tables.%s.maintenance] requires the pg_partman extension to be declared in [meta].extensions", t.Name),
			})
		}
		// A maintenance schedule is executed via pg_cron; declaring one without
		// pg_cron would emit a cron.schedule() call that fails at apply time.
		if t.Maintenance.Schedule != "" && !hasCron {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E010",
				Table:    t.Name,
				Message:  fmt.Sprintf("[tables.%s.maintenance].schedule requires the pg_cron extension to be declared in [meta].extensions", t.Name),
			})
		}
	}
	return diags
}

// BuildMulti constructs a resolved Schema from multiple raw schemas and a type
// registry. Tables, enums, and extensions from all schemas are merged into one
// Schema. Each table's Schema field is set from its source RawSchema's meta.schema.
// The returned Schema.Name is empty (multi-schema has no single name).
func BuildMulti(raws []*parse.RawSchema, reg *semtype.Registry, opts ...BuildOption) (*Schema, diagnostic.Diagnostics) {
	if len(raws) == 1 {
		return Build(raws[0], reg, opts...)
	}

	o := applyBuildOptions(opts)
	var diags diagnostic.Diagnostics

	schema := &Schema{}

	// Merge extensions (deduplicate).
	extSeen := make(map[string]bool)
	for _, raw := range raws {
		for _, ext := range raw.Meta.Extensions {
			if !extSeen[ext] {
				extSeen[ext] = true
				schema.Extensions = append(schema.Extensions, ext)
			}
		}
		// Use the highest PG version across all schemas.
		if raw.Meta.Version > schema.PGVersion {
			schema.PGVersion = raw.Meta.Version
		}
	}

	// Phase 1: resolve all schemas.
	var allTables []Table
	for _, raw := range raws {
		tables, enums, compositeTypes, domains, resolveDiags := resolve(raw, reg, o.imports)
		diags = append(diags, resolveDiags...)
		schema.Enums = append(schema.Enums, enums...)
		schema.CompositeTypes = append(schema.CompositeTypes, compositeTypes...)
		schema.Domains = append(schema.Domains, domains...)
		allTables = append(allTables, tables...)
		schema.Views = append(schema.Views, resolveViews(raw)...)
		schema.MaterializedViews = append(schema.MaterializedViews, resolveMaterializedViews(raw)...)
		schema.Functions = append(schema.Functions, resolveFunctions(raw)...)
	}

	// Deduplicate enums (same SM or enum type declared in multiple files).
	schema.Enums = deduplicateEnums(schema.Enums)

	schema.Tables = allTables
	schema.ImportedTables = o.importedTables

	// Resolve sequences across all schemas (needs merged tables for owned_by validation).
	for _, raw := range raws {
		seqs, seqDiags := resolveSequences(raw, schema)
		diags = append(diags, seqDiags...)
		schema.Sequences = append(schema.Sequences, seqs...)
	}

	// Phase 3: enrich (appends auto-FK indexes; must run before canonical sort).
	enrichDiags := enrich(schema)
	diags = append(diags, enrichDiags...)

	// Populate the first-class state-machine collection from all schemas BEFORE
	// Canonicalize so it is canonicalized alongside everything else.
	for _, raw := range raws {
		schema.StateMachines = append(schema.StateMachines, resolveStateMachines(raw, reg)...)
	}
	schema.StateMachines = deduplicateStateMachines(schema.StateMachines)

	// Phase 4: canonicalize — ordering (incl. cross-schema topo) + derived structures.
	schema.Canonicalize()

	// Extract state machine transition maps from all schemas (derived codegen
	// convenience; excluded from identity).
	for _, raw := range raws {
		smts := resolveStateMachineTransitions(raw, reg)
		schema.StateMachineTransitions = append(schema.StateMachineTransitions, smts...)
	}
	schema.StateMachineTransitions = deduplicateSMTransitions(schema.StateMachineTransitions)

	// Merge and validate groups from all schemas.
	merged := mergeGroups(raws)
	groupDiags := resolveGroups(schema, merged)
	diags = append(diags, groupDiags...)

	// Partman maintenance requires pg_partman across the merged extension set.
	diags = append(diags, validateMaintenanceExtension(schema)...)

	// Alias references are permitted ONLY in FK ref_table; reject them elsewhere.
	diags = append(diags, validateAliasScoping(schema, o.imports)...)

	return schema, diags
}

// mergeGroups combines groups from multiple raw schemas. If the same group name
// appears in multiple schemas, table lists are concatenated.
func mergeGroups(raws []*parse.RawSchema) map[string][]string {
	var merged map[string][]string
	for _, raw := range raws {
		for name, tables := range raw.Groups {
			if merged == nil {
				merged = make(map[string][]string)
			}
			merged[name] = append(merged[name], tables...)
		}
	}
	return merged
}

// buildTablesByName populates the TablesByName lookup map from the owned Tables
// slice AND the ImportedTables reference slice (roadmap 7.3, union site 1). This
// is the resolution funnel for FK validation (E204), migrate FK qualification,
// and the coverage check C104 — all of which look up FK targets by
// TableByName. Without the imported entries an `alias:table` FK would resolve to
// nil and trip a spurious E204 (and C104 would silently skip). Imported entries
// are added only to this lookup map, never to Tables, so DDL/audit/codegen stay
// fail-closed.
func (s *Schema) buildTablesByName() {
	s.TablesByName = make(map[string]*Table, len(s.Tables)+len(s.ImportedTables))
	for i := range s.Tables {
		key := TableKey(s.Tables[i].Schema, s.Tables[i].Name)
		s.TablesByName[key] = &s.Tables[i]
	}
	for i := range s.ImportedTables {
		key := TableKey(s.ImportedTables[i].Schema, s.ImportedTables[i].Name)
		// Owned tables win on collision: a same-keyed local table shadows an
		// imported reference (the local object is the one this project generates).
		if _, exists := s.TablesByName[key]; !exists {
			s.TablesByName[key] = &s.ImportedTables[i]
		}
	}
}

// resolveGroups validates that every table name in groups refers to a table that
// exists in the schema, and copies the validated groups onto the schema.
func resolveGroups(schema *Schema, groups map[string][]string) diagnostic.Diagnostics {
	if len(groups) == 0 {
		return nil
	}

	// Build a set of known table names (bare, not schema-qualified).
	known := make(map[string]bool, len(schema.Tables))
	for i := range schema.Tables {
		known[schema.Tables[i].Name] = true
	}

	var diags diagnostic.Diagnostics
	for groupName, tables := range groups {
		for _, tbl := range tables {
			if !known[tbl] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E227",
					Message:  fmt.Sprintf("[groups].%s references unknown table %q", groupName, tbl),
				})
			}
		}
	}

	if !diags.HasErrors() {
		schema.Groups = groups
	}
	return diags
}

// resolve expands semantic types into PG types and builds model structs.
func resolve(raw *parse.RawSchema, reg *semtype.Registry, imports map[string]string) ([]Table, []Enum, []CompositeType, []Domain, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics
	var tables []Table
	var enums []Enum
	var compositeTypes []CompositeType

	// Build enums and composite types from raw types.
	// Extended types (with Extends set) are resolved from the registry to get
	// merged data; non-extended types are read directly from the raw TOML.
	for _, rt := range raw.Types {
		// Determine the effective kind: for extended types, resolve from registry.
		if rt.Extends != nil {
			td, err := reg.Resolve(rt.Name)
			if err != nil {
				continue
			}
			switch td.Kind {
			case semtype.KindEnum:
				enums = append(enums, Enum{
					Schema:     raw.Meta.Schema,
					Name:       td.Name,
					Values:     td.EnumValues,
					Comment:    td.Comment,
					SourceFile: raw.SourceFile,
				})
			case semtype.KindComposite:
				ct := CompositeType{
					Schema:     raw.Meta.Schema,
					Name:       td.Name,
					Comment:    td.Comment,
					SourceFile: raw.SourceFile,
				}
				for _, f := range td.Fields {
					ct.Fields = append(ct.Fields, CompositeField{
						Name:   f.Name,
						PGType: typeinfo.Parse(f.PGType),
					})
				}
				compositeTypes = append(compositeTypes, ct)
			case semtype.KindStateMachine:
				enums = append(enums, Enum{
					Schema:     raw.Meta.Schema,
					Name:       td.Name,
					Values:     td.EnumValues,
					Comment:    td.Comment,
					SourceFile: raw.SourceFile,
				})
			}
			continue
		}

		switch {
		case strings.EqualFold(rt.Kind, "enum"):
			e := Enum{
				Schema:     raw.Meta.Schema,
				Name:       rt.Name,
				Values:     rt.Values,
				SourceFile: raw.SourceFile,
			}
			if rt.Comment != nil {
				e.Comment = *rt.Comment
			}
			enums = append(enums, e)

		case strings.EqualFold(rt.Kind, "composite"):
			ct := CompositeType{
				Schema:     raw.Meta.Schema,
				Name:       rt.Name,
				SourceFile: raw.SourceFile,
			}
			if rt.Comment != nil {
				ct.Comment = *rt.Comment
			}
			// Fields come from the parsed RawType.Fields slice, in TOML
			// declaration order (order is semantic: it becomes the
			// PostgreSQL composite field order).
			for _, f := range rt.Fields {
				ct.Fields = append(ct.Fields, CompositeField{
					Name:   f.Name,
					PGType: typeinfo.Parse(f.Type),
				})
			}
			compositeTypes = append(compositeTypes, ct)

		case strings.EqualFold(rt.Kind, "state_machine"):
			td, err := reg.Resolve(rt.Name)
			if err != nil {
				continue
			}
			enums = append(enums, Enum{
				Schema:     raw.Meta.Schema,
				Name:       td.Name,
				Values:     td.EnumValues,
				Comment:    td.Comment,
				SourceFile: raw.SourceFile,
			})
		}
	}

	// Resolve tables.
	for _, rt := range raw.Tables {
		t, tableDiags := resolveTable(rt, raw.Meta.Schema, reg, raw.SourceFile, imports)
		diags = append(diags, tableDiags...)
		if t != nil {
			tables = append(tables, *t)
		}
	}

	// Build domains from scalar types with CHECK expressions.
	// Build a typeName -> sourceFile map for domain provenance tracking.
	// Domains are derived from scalar types in the registry, not directly from
	// RawType declarations, so we map type names to their source file here.
	typeSourceFile := make(map[string]string, len(raw.Types))
	for _, rt := range raw.Types {
		typeSourceFile[rt.Name] = raw.SourceFile
	}
	var domains []Domain
	seen := make(map[string]bool)
	for _, t := range tables {
		for _, col := range t.Columns {
			if col.SemanticTypeName == "" || seen[col.SemanticTypeName] {
				continue
			}
			td, err := reg.Resolve(col.SemanticTypeName)
			if err != nil || td.Kind != semtype.KindScalar || td.Check == "" {
				continue
			}
			seen[col.SemanticTypeName] = true
			domains = append(domains, Domain{
				Name:       td.Name,
				Schema:     raw.Meta.Schema,
				BaseType:   td.BaseType,
				Check:      td.Check,
				Comment:    td.Comment,
				SourceFile: typeSourceFile[td.Name],
			})
		}
	}

	return tables, enums, compositeTypes, domains, diags
}

// resolveViews converts raw views into model Views.
func resolveViews(raw *parse.RawSchema) []View {
	var views []View
	for _, rv := range raw.Views {
		v := resolveView(rv, raw.Meta.Schema, raw.SourceFile)
		views = append(views, v)
	}
	return views
}

// resolveView converts a single raw view into a model View.
func resolveView(rv parse.RawView, schemaName string, sourceFile string) View {
	v := View{
		Name:       rv.Name,
		Schema:     schemaName,
		SourceFile: sourceFile,
		Query:      rv.Query,
		DependsOn:  rv.DependsOn,
	}
	if rv.Comment != nil {
		v.Comment = *rv.Comment
	}
	return v
}

// resolveMaterializedViews converts raw materialized views into model MaterializedViews.
func resolveMaterializedViews(raw *parse.RawSchema) []MaterializedView {
	var mvs []MaterializedView
	for _, rmv := range raw.MaterializedViews {
		mv := MaterializedView{
			Name:       rmv.Name,
			Schema:     raw.Meta.Schema,
			SourceFile: raw.SourceFile,
			Query:      rmv.Query,
			DependsOn:  rmv.DependsOn,
			WithData:   true,
		}
		if rmv.Comment != nil {
			mv.Comment = *rmv.Comment
		}
		if rmv.WithData != nil {
			mv.WithData = *rmv.WithData
		}
		for name, rawIdx := range rmv.Indexes {
			idx := resolveIndex(name, rawIdx)
			mv.Indexes = append(mv.Indexes, idx)
		}
		mvs = append(mvs, mv)
	}
	return mvs
}

// resolveFunctions converts raw functions into model Functions.
func resolveFunctions(raw *parse.RawSchema) []Function {
	var funcs []Function
	for _, rf := range raw.Functions {
		f := resolveFunction(rf, raw.Meta.Schema, raw.SourceFile)
		// Auto-populate DependsOn for LANGUAGE sql functions.
		if len(f.DependsOn) == 0 && strings.EqualFold(f.Language, "sql") && f.Body != "" {
			if refs, err := sqlparse.ExtractTableRefs(f.Body); err == nil && len(refs) > 0 {
				f.DependsOn = refs
			}
		}
		funcs = append(funcs, f)
	}
	return funcs
}

// resolveFunction converts a single raw function into a model Function.
func resolveFunction(rf parse.RawFunction, schemaName string, sourceFile string) Function {
	f := Function{
		Name:       rf.Name,
		Schema:     schemaName,
		SourceFile: sourceFile,
		DependsOn:  rf.DependsOn,
	}
	if rf.Language != nil {
		f.Language = *rf.Language
	}
	if rf.Returns != nil {
		f.ReturnType = *rf.Returns
	}
	if rf.Body != nil {
		f.Body = *rf.Body
	}
	if rf.Comment != nil {
		f.Comment = *rf.Comment
	}
	if rf.Volatility != nil {
		f.Volatility = strings.ToUpper(*rf.Volatility)
	}
	if rf.Parallel != nil {
		f.Parallel = strings.ToUpper(*rf.Parallel)
	}
	if rf.SecurityDefiner != nil {
		f.SecurityDefiner = *rf.SecurityDefiner
	}
	if rf.Procedure != nil {
		f.IsProc = *rf.Procedure
	}
	f.Cost = rf.Cost
	f.Rows = rf.Rows
	// Convert args
	for _, ra := range rf.Args {
		arg := FunctionArg{
			Name: ra.Name,
			Type: typeinfo.Parse(ra.Type),
		}
		if ra.Default != nil {
			arg.Default = *ra.Default
		}
		f.Args = append(f.Args, arg)
	}
	return f
}

// resolveSequences converts raw sequences into model Sequences and validates
// owned_by references against the schema's tables.
func resolveSequences(raw *parse.RawSchema, schema *Schema) ([]Sequence, diagnostic.Diagnostics) {
	var seqs []Sequence
	var diags diagnostic.Diagnostics

	for _, rs := range raw.Sequences {
		seq := Sequence{
			Name:       rs.Name,
			Schema:     raw.Meta.Schema,
			SourceFile: raw.SourceFile,
			Start:      rs.Start,
			Increment:  rs.Increment,
			MinValue:   rs.MinValue,
			MaxValue:   rs.MaxValue,
			Cache:      rs.Cache,
		}
		if rs.Cycle != nil {
			seq.Cycle = *rs.Cycle
		}
		if rs.OwnedBy != nil {
			seq.OwnedBy = *rs.OwnedBy
		}
		if rs.Comment != nil {
			seq.Comment = *rs.Comment
		}

		// Validate owned_by reference.
		if seq.OwnedBy != "" {
			parts := strings.SplitN(seq.OwnedBy, ".", 2)
			if len(parts) != 2 {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E124",
					Message:  fmt.Sprintf("sequence %q: owned_by must be in \"table.column\" format, got %q", rs.Name, seq.OwnedBy),
				})
			} else {
				tableName, colName := parts[0], parts[1]
				table := schema.TableByName(raw.Meta.Schema, tableName)
				if table == nil {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Error,
						Code:     "E124",
						Message:  fmt.Sprintf("sequence %q: owned_by references unknown table %q", rs.Name, tableName),
					})
				} else {
					found := false
					for _, col := range table.Columns {
						if col.Name == colName {
							found = true
							if col.Identity != "" {
								diags = append(diags, diagnostic.Diagnostic{
									Severity: diagnostic.Error,
									Code:     "E124",
									Message:  fmt.Sprintf("sequence %q: owned_by cannot target identity column %q.%q", rs.Name, tableName, colName),
								})
							}
							break
						}
					}
					if !found {
						diags = append(diags, diagnostic.Diagnostic{
							Severity: diagnostic.Error,
							Code:     "E124",
							Message:  fmt.Sprintf("sequence %q: owned_by references unknown column %q.%q", rs.Name, tableName, colName),
						})
					}
				}
			}
		}

		seqs = append(seqs, seq)
	}

	return seqs, diags
}

// resolveTable resolves a single raw table into a model Table.
func resolveTable(rt parse.RawTable, schemaName string, reg *semtype.Registry, sourceFile string, imports map[string]string) (*Table, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	t := &Table{
		Name:       rt.Name,
		Schema:     schemaName,
		SourceFile: sourceFile,
	}

	if rt.Comment != nil {
		t.Comment = *rt.Comment
	}

	// Resolve columns.
	for _, rc := range rt.Columns {
		col, _, colDiags := resolveColumn(rc, rt.Name, reg)
		diags = append(diags, colDiags...)
		if col != nil {
			t.Columns = append(t.Columns, *col)
		}
	}

	// Resolve PK using id/pk precedence rule.
	t.PK = resolvePK(rt, t.Columns, &diags)

	// Resolve FKs.
	for name, rawFK := range rt.FKs {
		fk, fkDiags := resolveFK(name, rawFK, schemaName, rt.Name, imports)
		diags = append(diags, fkDiags...)
		t.FKs = append(t.FKs, fk)
	}

	// Resolve indexes.
	for name, rawIdx := range rt.Indexes {
		idx := resolveIndex(name, rawIdx)
		t.Indexes = append(t.Indexes, idx)
	}

	// Resolve unique constraints.
	for name, rawUniq := range rt.Uniques {
		uq := UniqueConstraint{
			Name:    name,
			Columns: rawUniq.Columns,
		}
		if rawUniq.Deferrable != nil {
			uq.Deferrable = *rawUniq.Deferrable
		}
		if rawUniq.InitiallyDeferred != nil {
			uq.InitiallyDeferred = *rawUniq.InitiallyDeferred
		}
		t.Uniques = append(t.Uniques, uq)
	}

	// Resolve check constraints.
	for name, rawCheck := range rt.Checks {
		t.Checks = append(t.Checks, CheckConstraint{
			Name: name,
			Expr: rawCheck.Expr,
		})
	}

	// Generate CHECK constraints from json_schema column attributes.
	for _, col := range t.Columns {
		if col.JSONSchema == "" {
			continue
		}
		var content []byte
		for _, rc := range rt.Columns {
			if rc.Name == col.Name && rc.JSONSchemaContent != nil {
				content = rc.JSONSchemaContent
				break
			}
		}
		if content == nil {
			continue
		}
		checks := jsonSchemaToChecks(col.Name, content)
		t.Checks = append(t.Checks, checks...)
	}

	// Resolve exclusion constraints.
	for name, rawExc := range rt.Exclusions {
		exc := resolveExclusion(name, rawExc)
		t.Exclusions = append(t.Exclusions, exc)
	}

	// Resolve policies.
	for name, rawPol := range rt.Policies {
		pol, polDiags := resolvePolicy(name, rawPol, rt.Name)
		diags = append(diags, polDiags...)
		t.Policies = append(t.Policies, pol)
	}
	// If any policies exist, enable RLS on the table.
	if len(t.Policies) > 0 {
		t.EnableRLS = true
	}
	// Explicit enable_rls from TOML takes precedence (allows RLS without policies).
	if rt.EnableRLS {
		t.EnableRLS = true
	}
	// Explicit force_rls from TOML.
	if rt.ForceRLS {
		t.ForceRLS = true
		// force_rls implies enable_rls.
		t.EnableRLS = true
	}

	// Resolve triggers.
	for name, rawTrig := range rt.Triggers {
		trig, trigDiags := resolveTrigger(name, rawTrig, rt.Name)
		diags = append(diags, trigDiags...)
		t.Triggers = append(t.Triggers, trig)
	}

	// Resolve append-only.
	if rt.AppendOnly != nil && *rt.AppendOnly {
		t.AppendOnly = true
	}

	// Resolve partitioning.
	if rt.Partitioning != nil {
		t.Partitioning = resolvePartitioning(rt.Partitioning)
	}

	// Resolve functional dependencies.
	colSet := make(map[string]bool, len(t.Columns))
	for _, col := range t.Columns {
		colSet[col.Name] = true
	}
	for _, rawDep := range rt.Dependencies {
		for _, name := range rawDep.Determinant {
			if !colSet[name] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E221",
					Table:    rt.Name,
					Column:   name,
					Message:  fmt.Sprintf("functional dependency references unknown column %q", name),
				})
			}
		}
		for _, name := range rawDep.Dependent {
			if !colSet[name] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E221",
					Table:    rt.Name,
					Column:   name,
					Message:  fmt.Sprintf("functional dependency references unknown column %q", name),
				})
			}
		}
		t.Dependencies = append(t.Dependencies, fd.FuncDep{
			Determinant: rawDep.Determinant,
			Dependent:   rawDep.Dependent,
			Source:      "declared",
		})
	}

	// Resolve maintenance.
	if rt.Maintenance != nil {
		mc := &MaintenanceConfig{}
		if rt.Maintenance.Interval != nil {
			mc.Interval = *rt.Maintenance.Interval
		}
		if rt.Maintenance.Premake != nil {
			mc.Premake = *rt.Maintenance.Premake
		}
		if rt.Maintenance.Retention != nil {
			mc.Retention = *rt.Maintenance.Retention
		}
		if rt.Maintenance.RetentionKeepTable != nil {
			mc.RetentionKeepTable = *rt.Maintenance.RetentionKeepTable
		}
		if rt.Maintenance.Schedule != nil {
			mc.Schedule = *rt.Maintenance.Schedule
		}
		// interval is required for any partman-managed table.
		if mc.Interval == "" {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E010",
				Table:    rt.Name,
				Message:  fmt.Sprintf("[tables.%s.maintenance] requires \"interval\" key for partman-managed tables", rt.Name),
			})
		}
		// premake is required: a silent zero disables partman premaking, so the
		// operator must state it explicitly rather than have it default to 0.
		if rt.Maintenance.Premake == nil {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E010",
				Table:    rt.Name,
				Message:  fmt.Sprintf("[tables.%s.maintenance] requires \"premake\" key for partman-managed tables (a missing value would silently disable partition premaking)", rt.Name),
			})
		}
		// pg_partman only manages RANGE-partitioned parents. Maintenance without
		// RANGE partitioning would emit contradictory or inert DDL.
		if rt.Partitioning == nil {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E010",
				Table:    rt.Name,
				Message:  fmt.Sprintf("[tables.%s.maintenance] requires RANGE partitioning; table %q has no partitioning", rt.Name, rt.Name),
			})
		} else if !strings.EqualFold(rt.Partitioning.Strategy, "RANGE") {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E010",
				Table:    rt.Name,
				Message:  fmt.Sprintf("[tables.%s.maintenance] requires RANGE partitioning; strategy is %q", rt.Name, rt.Partitioning.Strategy),
			})
		}
		// Manual partition children and partman maintenance are mutually
		// exclusive: partman creates and drops children automatically, so
		// declaring them by hand produces contradictory DDL.
		if rt.Partitioning != nil && len(rt.Partitioning.Partitions) > 0 {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E010",
				Table:    rt.Name,
				Message:  fmt.Sprintf("[tables.%s.maintenance] cannot be combined with manual partition children; partman manages children automatically", rt.Name),
			})
		}
		t.Maintenance = mc
	}

	return t, diags
}

// resolveColumn resolves a single raw column into a model Column.
func resolveColumn(rc parse.RawColumn, tableName string, reg *semtype.Registry) (*Column, string, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	resolved, err := reg.ResolveColumn(rc.Type, rc.Nullable, rc.Default, rc.DefaultExpr, rc.Array)
	if err != nil {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E121",
			Table:    tableName,
			Column:   rc.Name,
			Message:  fmt.Sprintf("cannot resolve type %q: %s", rc.Type, err.Error()),
		})
		return nil, "", diags
	}

	col := &Column{
		Name:             rc.Name,
		PGType:           resolved.PGType,
		NotNull:          resolved.NotNull,
		Default:          resolved.Default,
		DefaultExpr:      resolved.DefaultExpr,
		Generated:        resolved.Generated,
		Stored:           resolved.Stored,
		Identity:         resolved.Identity,
		SemanticTypeName: rc.Type,
		Array:            resolved.Array,
		TypeKind:         resolved.Kind,
	}

	// If the semantic type has a CHECK expression, the column uses a domain.
	// Set DomainName so DDL output uses the domain name instead of the base PG type.
	if resolved.Check != "" {
		col.PGType.DomainName = rc.Type
	}

	// Apply column-level generated override.
	if rc.Generated != nil {
		col.Generated = *rc.Generated
	}
	if rc.Stored != nil {
		col.Stored = *rc.Stored
	}

	// Default: generated columns are STORED unless explicitly set otherwise.
	// PostgreSQL < 18 only supports STORED; PG 18+ defaults to VIRTUAL when
	// the storage keyword is omitted. We make the model explicit so downstream
	// code never has to guess.
	if col.Generated != "" && rc.Stored == nil {
		col.Stored = true
	}

	if rc.Comment != nil {
		col.Comment = *rc.Comment
	}

	if rc.JSONSchema != nil {
		col.JSONSchema = *rc.JSONSchema
	}

	if rc.Collation != nil {
		col.Collation = *rc.Collation
	}
	if rc.Statistics != nil {
		col.Statistics = rc.Statistics
	}

	return col, resolved.Check, diags
}

// resolvePK applies the id/pk precedence rule.
func resolvePK(rt parse.RawTable, columns []Column, diags *diagnostic.Diagnostics) []string {
	// Rule 1: explicit PK from raw.
	if len(rt.PK) > 0 {
		return rt.PK
	}

	// Rule 2: exactly one column with semantic type "id" or "auto_id".
	var idColumns []string
	for _, col := range columns {
		if col.SemanticTypeName == "id" || col.SemanticTypeName == "auto_id" {
			idColumns = append(idColumns, col.Name)
		}
	}
	if len(idColumns) == 1 {
		return idColumns
	}

	// Rule 3: no PK found.
	*diags = append(*diags, diagnostic.Diagnostic{
		Severity: diagnostic.Error,
		Code:     "E120",
		Table:    rt.Name,
		Message:  "table missing primary key",
	})
	return nil
}

// resolveFK converts a raw FK definition to a model FK, resolving the ref_table
// reference. Resolution order (roadmap 7.1):
//
//  1. If ref_table contains ':' it is an import alias reference `alias:table`.
//     The alias is resolved BEFORE any dot-split: it maps to the import's target
//     PG schema, and the remainder is the (unqualified) table name. An undeclared
//     alias is a hard error (E230); a qualified remainder is malformed (E232).
//  2. Otherwise a '.' splits schema.table; a bare name inherits schemaName.
func resolveFK(name string, rawFK parse.RawFK, schemaName, tableName string, imports map[string]string) (FK, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics
	fk := FK{
		Name:       name,
		Columns:    rawFK.Columns,
		RefColumns: rawFK.RefColumns,
		OnDelete:   rawFK.OnDelete,
	}

	// Alias resolution happens BEFORE dot-split.
	if idx := strings.IndexByte(rawFK.RefTable, ':'); idx >= 0 {
		alias := rawFK.RefTable[:idx]
		target := rawFK.RefTable[idx+1:]
		if alias == "" || target == "" {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error, Code: "E232", Table: tableName,
				Message: fmt.Sprintf("foreign key %q ref_table %q is malformed: expected alias:table", name, rawFK.RefTable),
			})
			return fk, diags
		}
		if strings.Contains(target, ".") {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error, Code: "E232", Table: tableName,
				Message: fmt.Sprintf("foreign key %q ref_table %q is malformed: the target of an alias reference must be an unqualified table name (the alias already selects the schema)", name, rawFK.RefTable),
			})
			return fk, diags
		}
		targetSchema, ok := imports[alias]
		if !ok {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error, Code: "E230", Table: tableName,
				Message:    fmt.Sprintf("foreign key %q references unknown import alias %q; declare it under [imports] in pgdesign.toml", name, alias),
				Suggestion: fmt.Sprintf("[imports.%s]\ngit = \"...\"\nref = \"...\"\nschema = \"...\"", alias),
			})
			return fk, diags
		}
		fk.RefSchema = targetSchema
		fk.RefTable = target
		fk.RefAlias = alias
		return fk, diags
	}

	// Parse qualified ref table name (bare = same schema).
	if strings.Contains(rawFK.RefTable, ".") {
		parts := strings.SplitN(rawFK.RefTable, ".", 2)
		fk.RefSchema = parts[0]
		fk.RefTable = parts[1]
	} else {
		fk.RefSchema = schemaName
		fk.RefTable = rawFK.RefTable
	}

	return fk, diags
}

// validateAliasScoping enforces that import alias references (`alias:...`) appear
// ONLY in FK ref_table. Any declared alias used as a prefix in a depends_on
// entry or a view/matview query body is a hard error (E231) naming the
// unsupported site — a typo'd alias in those positions must not silently become
// a phantom dependency. With no declared imports there is nothing to police.
func validateAliasScoping(schema *Schema, imports map[string]string) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics
	if len(imports) == 0 {
		return diags
	}
	aliases := make([]string, 0, len(imports))
	for a := range imports {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)

	reject := func(where, detail string) {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error, Code: "E231",
			Message: fmt.Sprintf("import alias reference is only supported in foreign key ref_table, not in %s (%s)", where, detail),
		})
	}
	// containsAliasRef reports the first declared alias whose `alias:` prefix
	// appears in s, or "" if none.
	containsAliasRef := func(s string) string {
		for _, a := range aliases {
			if strings.Contains(s, a+":") {
				return a
			}
		}
		return ""
	}

	for _, v := range schema.Views {
		for _, dep := range v.DependsOn {
			if a := containsAliasRef(dep); a != "" {
				reject("view depends_on", fmt.Sprintf("view %q references alias %q", v.Name, a))
			}
		}
		if a := containsAliasRef(v.Query); a != "" {
			reject("view query", fmt.Sprintf("view %q query references alias %q", v.Name, a))
		}
	}
	for _, mv := range schema.MaterializedViews {
		for _, dep := range mv.DependsOn {
			if a := containsAliasRef(dep); a != "" {
				reject("materialized view depends_on", fmt.Sprintf("materialized view %q references alias %q", mv.Name, a))
			}
		}
		if a := containsAliasRef(mv.Query); a != "" {
			reject("materialized view query", fmt.Sprintf("materialized view %q query references alias %q", mv.Name, a))
		}
	}
	for _, fn := range schema.Functions {
		for _, dep := range fn.DependsOn {
			if a := containsAliasRef(dep); a != "" {
				reject("function depends_on", fmt.Sprintf("function %q references alias %q", fn.Name, a))
			}
		}
	}
	return diags
}

// resolveIndex converts a raw index definition to a model Index.
func resolveIndex(name string, rawIdx parse.RawIndex) Index {
	// Parse column names and sort direction from raw strings.
	// Format: "column_name" (ASC, default) or "column_name DESC" or "column_name ASC".
	columns, desc := parseIndexColumns(rawIdx.Columns)

	idx := Index{
		Name:    name,
		Columns: columns,
		Desc:    desc,
		Include: rawIdx.Include,
	}
	if rawIdx.Method != nil {
		idx.Method = *rawIdx.Method
	}
	if rawIdx.OpclassMap != nil {
		// Per-column opclass map: copy directly.
		idx.Opclasses = make(map[string]string, len(rawIdx.OpclassMap))
		for k, v := range rawIdx.OpclassMap {
			idx.Opclasses[k] = v
		}
	} else if rawIdx.Opclass != nil {
		// Single opclass: expand to all columns.
		idx.Opclasses = make(map[string]string, len(columns))
		for _, col := range columns {
			idx.Opclasses[col] = *rawIdx.Opclass
		}
	}
	if rawIdx.CollationMap != nil {
		idx.Collations = make(map[string]string, len(rawIdx.CollationMap))
		for k, v := range rawIdx.CollationMap {
			idx.Collations[k] = v
		}
	} else if rawIdx.Collation != nil {
		idx.Collations = make(map[string]string, len(columns))
		for _, col := range columns {
			idx.Collations[col] = *rawIdx.Collation
		}
	}
	if rawIdx.With != nil {
		idx.With = make(map[string]string, len(rawIdx.With))
		for k, v := range rawIdx.With {
			idx.With[k] = v
		}
	}
	if rawIdx.Where != nil {
		idx.Where = *rawIdx.Where
	}
	if rawIdx.Unique != nil {
		idx.Unique = *rawIdx.Unique
	}
	return idx
}

// resolvePolicy converts a raw policy definition to a model Policy with validation.
func resolvePolicy(name string, rawPol parse.RawPolicy, tableName string) (Policy, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	pol := Policy{
		Name:         name,
		Type:         strings.ToUpper(rawPol.Type),
		Operation:    strings.ToUpper(rawPol.For),
		Role:         rawPol.To,
		Using:        rawPol.Using,
		WithCheck:    rawPol.WithCheck,
		ErrorCode:    rawPol.ErrorCode,
		ErrorMessage: rawPol.ErrorMessage,
	}

	// Validate operation.
	validOps := map[string]bool{
		"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "ALL": true,
	}
	if pol.Operation == "" {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E122",
			Table:    tableName,
			Message:  fmt.Sprintf("policy %q missing required field \"for\"", name),
		})
	} else if !validOps[pol.Operation] {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E122",
			Table:    tableName,
			Message:  fmt.Sprintf("policy %q has invalid operation %q; must be SELECT, INSERT, UPDATE, DELETE, or ALL", name, pol.Operation),
		})
	}

	// Validate policy type. Default to PERMISSIVE if empty.
	if pol.Type == "" {
		pol.Type = "PERMISSIVE"
	}
	validTypes := map[string]bool{"PERMISSIVE": true, "RESTRICTIVE": true}
	if !validTypes[pol.Type] {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E124",
			Table:    tableName,
			Message:  fmt.Sprintf("policy %q has invalid type %q; must be PERMISSIVE or RESTRICTIVE", name, pol.Type),
		})
	}

	// At least one of using or with_check must be set.
	if pol.Using == "" && pol.WithCheck == "" {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E123",
			Table:    tableName,
			Message:  fmt.Sprintf("policy %q must have at least one of \"using\" or \"with_check\"", name),
		})
	}

	return pol, diags
}

// resolveTrigger converts a raw trigger definition to a model Trigger with validation.
func resolveTrigger(name string, raw parse.RawTrigger, tableName string) (Trigger, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	t := Trigger{
		Name:     name,
		Function: raw.Function,
		Timing:   strings.ToUpper(raw.Timing),
		ForEach:  "ROW", // default
	}

	// Copy events, uppercased.
	for _, ev := range raw.Events {
		t.Events = append(t.Events, strings.ToUpper(ev))
	}

	if raw.ForEach != nil {
		t.ForEach = strings.ToUpper(*raw.ForEach)
	}
	if raw.When != nil {
		t.When = *raw.When
	}
	if raw.Constraint != nil {
		t.Constraint = *raw.Constraint
	}
	if raw.Deferrable != nil {
		t.Deferrable = *raw.Deferrable
	}
	if raw.InitiallyDeferred != nil {
		t.InitiallyDeferred = *raw.InitiallyDeferred
	}
	if raw.ReferencingOld != nil {
		t.ReferencingOld = *raw.ReferencingOld
	}
	if raw.ReferencingNew != nil {
		t.ReferencingNew = *raw.ReferencingNew
	}
	if raw.Comment != nil {
		t.Comment = *raw.Comment
	}

	// Validate timing.
	validTimings := map[string]bool{
		"BEFORE": true, "AFTER": true, "INSTEAD OF": true,
	}
	if t.Timing == "" {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E125",
			Table:    tableName,
			Message:  fmt.Sprintf("trigger %q missing required field \"timing\"", name),
		})
	} else if !validTimings[t.Timing] {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E125",
			Table:    tableName,
			Message:  fmt.Sprintf("trigger %q has invalid timing %q; must be BEFORE, AFTER, or INSTEAD OF", name, t.Timing),
		})
	}

	// Validate events.
	validEvents := map[string]bool{
		"INSERT": true, "UPDATE": true, "DELETE": true, "TRUNCATE": true,
	}
	if len(t.Events) == 0 {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E125",
			Table:    tableName,
			Message:  fmt.Sprintf("trigger %q missing required field \"events\"", name),
		})
	}
	for _, ev := range t.Events {
		if !validEvents[ev] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E125",
				Table:    tableName,
				Message:  fmt.Sprintf("trigger %q has invalid event %q; must be INSERT, UPDATE, DELETE, or TRUNCATE", name, ev),
			})
		}
	}

	// Validate for_each.
	validForEach := map[string]bool{"ROW": true, "STATEMENT": true}
	if !validForEach[t.ForEach] {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E125",
			Table:    tableName,
			Message:  fmt.Sprintf("trigger %q has invalid for_each %q; must be ROW or STATEMENT", name, t.ForEach),
		})
	}

	// Constraint triggers must be AFTER.
	if t.Constraint && t.Timing != "AFTER" {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E126",
			Table:    tableName,
			Message:  fmt.Sprintf("constraint trigger %q must use timing AFTER, got %q", name, t.Timing),
		})
	}

	// REFERENCING requires AFTER + ROW (PostgreSQL restriction).
	if (t.ReferencingOld != "" || t.ReferencingNew != "") && (t.Timing != "AFTER" || t.ForEach != "ROW") {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E127",
			Table:    tableName,
			Message:  fmt.Sprintf("trigger %q uses REFERENCING but timing must be AFTER and for_each must be ROW", name),
		})
	}

	// Function is required.
	if t.Function == "" {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E125",
			Table:    tableName,
			Message:  fmt.Sprintf("trigger %q missing required field \"function\"", name),
		})
	}

	return t, diags
}

func resolveExclusion(name string, raw parse.RawExclusion) ExclusionConstraint {
	exc := ExclusionConstraint{
		Name:   name,
		Method: "gist", // default
	}
	if raw.Method != nil {
		exc.Method = *raw.Method
	}
	if raw.Where != nil {
		exc.Where = *raw.Where
	}
	if raw.Deferrable != nil {
		exc.Deferrable = *raw.Deferrable
	}
	if raw.InitiallyDeferred != nil {
		exc.InitiallyDeferred = *raw.InitiallyDeferred
	}

	// Pair up columns[i] with operators[i] into ExclusionElements.
	for i := range raw.Columns {
		elem := ExclusionElement{
			Column: raw.Columns[i],
		}
		if i < len(raw.Operators) {
			elem.Operator = raw.Operators[i]
		}
		exc.Elements = append(exc.Elements, elem)
	}

	return exc
}

// parseIndexColumns splits raw column strings like "col DESC" into separate
// column names and a parallel desc slice. A plain "col" or "col ASC" is ASC
// (desc=false). "col DESC" is desc=true. The comparison is case-insensitive.
func parseIndexColumns(raw []string) ([]string, []bool) {
	columns := make([]string, len(raw))
	desc := make([]bool, len(raw))
	anyDesc := false
	for i, s := range raw {
		s = strings.TrimSpace(s)
		if last := strings.LastIndexByte(s, ' '); last >= 0 {
			suffix := strings.ToUpper(s[last+1:])
			if suffix == "DESC" {
				columns[i] = strings.TrimSpace(s[:last])
				desc[i] = true
				anyDesc = true
				continue
			}
			if suffix == "ASC" {
				columns[i] = strings.TrimSpace(s[:last])
				desc[i] = false
				continue
			}
		}
		columns[i] = s
		desc[i] = false
	}
	// Omit desc slice if all columns are ASC (backward compatibility).
	if !anyDesc {
		return columns, nil
	}
	return columns, desc
}

// resolvePartitioning converts raw partitioning into a model PartitionSpec.
func resolvePartitioning(raw *parse.RawPartitioning) *PartitionSpec {
	ps := &PartitionSpec{
		Strategy: raw.Strategy,
		Name:     raw.Name,
		Bound:    raw.Bound,
	}
	// Resolve columns: single Column wraps to slice, Columns used directly.
	if len(raw.Columns) > 0 {
		ps.Columns = raw.Columns
	} else if raw.Column != "" {
		ps.Columns = []string{raw.Column}
	}
	for _, child := range raw.Partitions {
		childCopy := child
		resolved := resolvePartitioning(&childCopy)
		ps.Children = append(ps.Children, *resolved)
	}
	return ps
}

// constraintName generates a constraint name following the same convention as
// sql.ConstraintName: kind_table_refs joined by underscores. Duplicated here
// because internal/sql imports internal/model, so the reverse import would
// create a cycle.
func constraintName(table, kind string, refs ...string) string {
	parts := []string{kind, table}
	parts = append(parts, refs...)
	return strings.Join(parts, "_")
}

// IsStateMachineColumn returns true if the column's semantic type is a state machine.
func IsStateMachineColumn(col Column, reg *semtype.Registry) bool {
	if col.SemanticTypeName == "" {
		return false
	}
	td, err := reg.Resolve(col.SemanticTypeName)
	if err != nil {
		return false
	}
	return td.Kind == semtype.KindStateMachine
}

// resolveStateMachineTransitions extracts transition maps from state machine
// types declared in the raw schema.
func resolveStateMachineTransitions(raw *parse.RawSchema, reg *semtype.Registry) []SMTransitionMap {
	var result []SMTransitionMap
	for _, rt := range raw.Types {
		if !strings.EqualFold(rt.Kind, "state_machine") {
			continue
		}
		td, err := reg.Resolve(rt.Name)
		if err != nil {
			continue
		}

		// Build from-state -> []to-state map.
		transMap := make(map[string][]string)
		for _, tr := range td.Transitions {
			for _, from := range tr.From {
				transMap[from] = append(transMap[from], tr.To)
			}
		}

		// Deduplicate and sort target states for deterministic output.
		for from, tos := range transMap {
			seen := make(map[string]bool, len(tos))
			var deduped []string
			for _, to := range tos {
				if !seen[to] {
					seen[to] = true
					deduped = append(deduped, to)
				}
			}
			sort.Strings(deduped)
			transMap[from] = deduped
		}

		// Build named transitions for codegen.
		var namedTrans []NamedTransition
		for _, tr := range td.Transitions {
			nt := NamedTransition{
				Name: tr.Name,
				From: tr.From,
				To:   tr.To,
			}
			if len(tr.Requires) > 0 {
				nt.Requires = make(map[string]string, len(tr.Requires))
				for k, v := range tr.Requires {
					nt.Requires[k] = v
				}
			}
			namedTrans = append(namedTrans, nt)
		}
		sort.Slice(namedTrans, func(i, j int) bool {
			return namedTrans[i].Name < namedTrans[j].Name
		})

		result = append(result, SMTransitionMap{
			TypeName:         td.Name,
			Transitions:      transMap,
			States:           td.EnumValues,
			NamedTransitions: namedTrans,
			EnforceTrigger:   td.EnforceTrigger,
		})
	}
	return result
}

// resolveStateMachines builds the first-class, identity-bearing StateMachine
// collection from the registry for every state-machine type declared in the raw
// schema. Unlike resolveStateMachineTransitions (a derived from->to adjacency
// for codegen), this preserves the FULL transition graph with per-state and
// per-transition comments — the identity content the derived duplicate drops.
func resolveStateMachines(raw *parse.RawSchema, reg *semtype.Registry) []StateMachine {
	var result []StateMachine
	for _, rt := range raw.Types {
		if !strings.EqualFold(rt.Kind, "state_machine") {
			continue
		}
		td, err := reg.Resolve(rt.Name)
		if err != nil {
			continue
		}
		sm := StateMachine{
			Name:           td.Name,
			Schema:         raw.Meta.Schema,
			InitialState:   td.InitialState,
			EnforceTrigger: td.EnforceTrigger,
			Comment:        td.Comment,
		}
		for _, s := range td.States {
			sm.States = append(sm.States, SMState{Name: s.Name, Terminal: s.Terminal, Comment: s.Comment})
		}
		for _, tr := range td.Transitions {
			t := SMTransition{
				Name:    tr.Name,
				From:    append([]string(nil), tr.From...),
				To:      tr.To,
				Comment: tr.Comment,
			}
			if len(tr.Requires) > 0 {
				t.Requires = make(map[string]string, len(tr.Requires))
				for k, v := range tr.Requires {
					t.Requires[k] = v
				}
			}
			sm.Transitions = append(sm.Transitions, t)
		}
		result = append(result, sm)
	}
	return result
}

// deduplicateStateMachines removes duplicate StateMachine definitions by name
// (a type may be referenced from multiple raw schemas in a multi-file build).
func deduplicateStateMachines(sms []StateMachine) []StateMachine {
	seen := make(map[string]bool, len(sms))
	var result []StateMachine
	for _, sm := range sms {
		if seen[sm.Name] {
			continue
		}
		seen[sm.Name] = true
		result = append(result, sm)
	}
	return result
}

// deduplicateSMTransitions removes duplicate SM transition maps by type name.
func deduplicateSMTransitions(smts []SMTransitionMap) []SMTransitionMap {
	seen := make(map[string]bool, len(smts))
	var result []SMTransitionMap
	for _, smt := range smts {
		if seen[smt.TypeName] {
			continue
		}
		seen[smt.TypeName] = true
		result = append(result, smt)
	}
	return result
}

// deduplicateEnums removes duplicate enums by schema+name key, keeping the first occurrence.
func deduplicateEnums(enums []Enum) []Enum {
	seen := make(map[string]bool, len(enums))
	var result []Enum
	for _, e := range enums {
		key := e.Schema + "." + e.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, e)
	}
	return result
}

// enrich materializes auto-indexes for FK columns that lack index coverage.
func enrich(schema *Schema) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	for i := range schema.Tables {
		t := &schema.Tables[i]
		for _, fk := range t.FKs {
			if !t.HasIndexCovering(fk.Columns) {
				idxName := constraintName(t.Name, "idx", fk.Columns...)
				t.Indexes = append(t.Indexes, Index{
					Name:     idxName,
					Columns:  fk.Columns,
					Method:   "btree",
					IsAutoFK: true,
				})
			}
		}
	}

	return diags
}

// jsonSchemaToChecks generates CHECK constraints from a JSON Schema definition.
// It supports a limited subset: top-level "required" and "properties" with "type" declarations.
// For each required property with a declared type, it generates a CHECK that verifies
// the key exists and has the correct jsonb_typeof value.
//
// JSON Schema type mapping to PostgreSQL jsonb_typeof:
//   - "string"  -> "string"
//   - "number"  -> "number"
//   - "integer" -> "number" (PostgreSQL doesn't distinguish)
//   - "boolean" -> "boolean"
//   - "object"  -> "object"
//   - "array"   -> "array"
func jsonSchemaToChecks(colName string, content []byte) []CheckConstraint {
	var schema struct {
		Required   []string                          `json:"required"`
		Properties map[string]map[string]interface{} `json:"properties"`
	}
	if err := json.Unmarshal(content, &schema); err != nil {
		return nil
	}

	typeMap := map[string]string{
		"string":  "string",
		"number":  "number",
		"integer": "number",
		"boolean": "boolean",
		"object":  "object",
		"array":   "array",
	}

	var checks []CheckConstraint

	for _, propName := range schema.Required {
		propDef, ok := schema.Properties[propName]
		if !ok {
			checks = append(checks, CheckConstraint{
				Name: fmt.Sprintf("ck_%s_%s_exists", colName, propName),
				Expr: fmt.Sprintf("%s ? '%s'", colName, propName),
			})
			continue
		}

		typeVal, ok := propDef["type"]
		if !ok {
			checks = append(checks, CheckConstraint{
				Name: fmt.Sprintf("ck_%s_%s_exists", colName, propName),
				Expr: fmt.Sprintf("%s ? '%s'", colName, propName),
			})
			continue
		}

		typeStr, ok := typeVal.(string)
		if !ok {
			continue
		}

		pgType, ok := typeMap[typeStr]
		if !ok {
			continue
		}

		checks = append(checks, CheckConstraint{
			Name: fmt.Sprintf("ck_%s_%s_type", colName, propName),
			Expr: fmt.Sprintf("%s ? '%s' AND jsonb_typeof(%s->'%s') = '%s'", colName, propName, colName, propName, pgType),
		})
	}

	return checks
}
