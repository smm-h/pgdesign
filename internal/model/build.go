package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
)

// Build constructs a resolved Schema from raw parse output and a type registry.
// It returns the schema (possibly partial) and any diagnostics encountered.
func Build(raw *parse.RawSchema, reg *semtype.Registry) (*Schema, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	schema := &Schema{
		Name:       raw.Meta.Schema,
		Extensions: raw.Meta.Extensions,
		PGVersion:  raw.Meta.Version,
	}

	// Phase 1: resolve
	tables, enums, resolveDiags := resolve(raw, reg)
	diags = append(diags, resolveDiags...)
	schema.Enums = enums
	schema.Views = resolveViews(raw)
	schema.MaterializedViews = resolveMaterializedViews(raw)

	// Phase 2: order
	sorted, cycles := topoSort(tables)
	schema.Tables = sorted
	schema.CycleGroups = cycles

	// Phase 1b: resolve sequences (needs schema.Tables for owned_by validation).
	seqs, seqDiags := resolveSequences(raw, schema)
	diags = append(diags, seqDiags...)
	schema.Sequences = seqs

	// Phase 3: enrich
	enrichDiags := enrich(schema)
	diags = append(diags, enrichDiags...)

	return schema, diags
}

// BuildMulti constructs a resolved Schema from multiple raw schemas and a type
// registry. Tables, enums, and extensions from all schemas are merged into one
// Schema. Each table's Schema field is set from its source RawSchema's meta.schema.
// The returned Schema.Name is empty (multi-schema has no single name).
func BuildMulti(raws []*parse.RawSchema, reg *semtype.Registry) (*Schema, diagnostic.Diagnostics) {
	if len(raws) == 1 {
		return Build(raws[0], reg)
	}

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
		tables, enums, resolveDiags := resolve(raw, reg)
		diags = append(diags, resolveDiags...)
		schema.Enums = append(schema.Enums, enums...)
		allTables = append(allTables, tables...)
		schema.Views = append(schema.Views, resolveViews(raw)...)
		schema.MaterializedViews = append(schema.MaterializedViews, resolveMaterializedViews(raw)...)
	}

	// Phase 2: order all tables together (topo sort sees cross-schema deps).
	sorted, cycles := topoSort(allTables)
	schema.Tables = sorted
	schema.CycleGroups = cycles

	// Resolve sequences across all schemas (needs merged tables for owned_by validation).
	for _, raw := range raws {
		seqs, seqDiags := resolveSequences(raw, schema)
		diags = append(diags, seqDiags...)
		schema.Sequences = append(schema.Sequences, seqs...)
	}

	// Phase 3: enrich.
	enrichDiags := enrich(schema)
	diags = append(diags, enrichDiags...)

	return schema, diags
}

// resolve expands semantic types into PG types and builds model structs.
func resolve(raw *parse.RawSchema, reg *semtype.Registry) ([]Table, []Enum, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics
	var tables []Table
	var enums []Enum

	// Build enums from raw types with kind=enum.
	for _, rt := range raw.Types {
		if strings.EqualFold(rt.Kind, "enum") {
			e := Enum{
				Schema: raw.Meta.Schema,
				Name:   rt.Name,
				Values: rt.Values,
			}
			if rt.Comment != nil {
				e.Comment = *rt.Comment
			}
			enums = append(enums, e)
		}
	}

	// Resolve tables.
	for _, rt := range raw.Tables {
		t, tableDiags := resolveTable(rt, raw.Meta.Schema, reg)
		diags = append(diags, tableDiags...)
		if t != nil {
			tables = append(tables, *t)
		}
	}

	return tables, enums, diags
}

// resolveViews converts raw views into model Views.
func resolveViews(raw *parse.RawSchema) []View {
	var views []View
	for _, rv := range raw.Views {
		v := resolveView(rv, raw.Meta.Schema)
		views = append(views, v)
	}
	return views
}

// resolveView converts a single raw view into a model View.
func resolveView(rv parse.RawView, schemaName string) View {
	v := View{
		Name:      rv.Name,
		Schema:    schemaName,
		Query:     rv.Query,
		DependsOn: rv.DependsOn,
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
			Name:      rmv.Name,
			Schema:    raw.Meta.Schema,
			Query:     rmv.Query,
			DependsOn: rmv.DependsOn,
			WithData:  true,
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

// resolveSequences converts raw sequences into model Sequences and validates
// owned_by references against the schema's tables.
func resolveSequences(raw *parse.RawSchema, schema *Schema) ([]Sequence, diagnostic.Diagnostics) {
	var seqs []Sequence
	var diags diagnostic.Diagnostics

	for _, rs := range raw.Sequences {
		seq := Sequence{
			Name:      rs.Name,
			Schema:    raw.Meta.Schema,
			Start:     rs.Start,
			Increment: rs.Increment,
			MinValue:  rs.MinValue,
			MaxValue:  rs.MaxValue,
			Cache:     rs.Cache,
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
func resolveTable(rt parse.RawTable, schemaName string, reg *semtype.Registry) (*Table, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	t := &Table{
		Name:   rt.Name,
		Schema: schemaName,
	}

	if rt.Comment != nil {
		t.Comment = *rt.Comment
	}

	// Resolve columns.
	type colCheck struct {
		colName   string
		checkExpr string
	}
	var semanticChecks []colCheck

	for _, rc := range rt.Columns {
		col, checkExpr, colDiags := resolveColumn(rc, rt.Name, reg)
		diags = append(diags, colDiags...)
		if col != nil {
			t.Columns = append(t.Columns, *col)
			if checkExpr != "" {
				semanticChecks = append(semanticChecks, colCheck{
					colName:   col.Name,
					checkExpr: checkExpr,
				})
			}
		}
	}

	// Resolve PK using id/pk precedence rule.
	t.PK = resolvePK(rt, t.Columns, &diags)

	// Resolve FKs.
	for name, rawFK := range rt.FKs {
		fk := resolveFK(name, rawFK, schemaName)
		t.FKs = append(t.FKs, fk)
	}

	// Resolve indexes.
	for name, rawIdx := range rt.Indexes {
		idx := resolveIndex(name, rawIdx)
		t.Indexes = append(t.Indexes, idx)
	}

	// Resolve unique constraints.
	for name, rawUniq := range rt.Uniques {
		t.Uniques = append(t.Uniques, UniqueConstraint{
			Name:    name,
			Columns: rawUniq.Columns,
		})
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

	// Generate CHECK constraints from semantic type CHECK expressions.
	for _, sc := range semanticChecks {
		// Replace VALUE placeholder with actual column name.
		expr := strings.ReplaceAll(sc.checkExpr, "VALUE", sc.colName)
		name := constraintName(rt.Name, "chk", sc.colName)
		t.Checks = append(t.Checks, CheckConstraint{
			Name: name,
			Expr: expr,
		})
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
		})
	}

	// Resolve maintenance.
	if rt.Maintenance != nil {
		mc := &MaintenanceConfig{}
		if rt.Maintenance.Premake != nil {
			mc.Premake = *rt.Maintenance.Premake
		}
		if rt.Maintenance.Retention != nil {
			mc.Retention = *rt.Maintenance.Retention
		}
		if rt.Maintenance.RetentionKeepTable != nil {
			mc.RetentionKeepTable = *rt.Maintenance.RetentionKeepTable
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

// resolveFK converts a raw FK definition to a model FK.
func resolveFK(name string, rawFK parse.RawFK, schemaName string) FK {
	fk := FK{
		Name:       name,
		Columns:    rawFK.Columns,
		RefColumns: rawFK.RefColumns,
		OnDelete:   rawFK.OnDelete,
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

	return fk
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
