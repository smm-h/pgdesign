package model

import (
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

	// Phase 2: order
	sorted, cycles := topoSort(tables)
	schema.Tables = sorted
	schema.CycleGroups = cycles

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
	}

	// Phase 2: order all tables together (topo sort sees cross-schema deps).
	sorted, cycles := topoSort(allTables)
	schema.Tables = sorted
	schema.CycleGroups = cycles

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
	for _, rc := range rt.Columns {
		col, colDiags := resolveColumn(rc, rt.Name, reg)
		diags = append(diags, colDiags...)
		if col != nil {
			t.Columns = append(t.Columns, *col)
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

	// Resolve partitioning.
	if rt.Partitioning != nil {
		t.Partitioning = resolvePartitioning(rt.Partitioning)
	}

	// Resolve functional dependencies.
	for _, rawDep := range rt.Dependencies {
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
func resolveColumn(rc parse.RawColumn, tableName string, reg *semtype.Registry) (*Column, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	resolved, err := reg.ResolveColumn(rc.Type, rc.Nullable, rc.Default, rc.DefaultExpr)
	if err != nil {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E101",
			Table:    tableName,
			Column:   rc.Name,
			Message:  fmt.Sprintf("cannot resolve type %q: %s", rc.Type, err.Error()),
		})
		return nil, diags
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
	}

	// Apply column-level generated override.
	if rc.Generated != nil {
		col.Generated = *rc.Generated
	}
	if rc.Stored != nil {
		col.Stored = *rc.Stored
	}

	if rc.Comment != nil {
		col.Comment = *rc.Comment
	}

	return col, diags
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
		Code:     "E100",
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
	idx := Index{
		Name:    name,
		Columns: rawIdx.Columns,
		Include: rawIdx.Include,
	}
	if rawIdx.Method != nil {
		idx.Method = *rawIdx.Method
	}
	if rawIdx.Opclass != nil {
		idx.Opclass = *rawIdx.Opclass
	}
	if rawIdx.Where != nil {
		idx.Where = *rawIdx.Where
	}
	return idx
}

// resolvePartitioning converts raw partitioning into a model PartitionSpec.
func resolvePartitioning(raw *parse.RawPartitioning) *PartitionSpec {
	ps := &PartitionSpec{
		Strategy: raw.Strategy,
		Column:   raw.Column,
	}
	for _, child := range raw.Partitions {
		childCopy := child
		resolved := resolvePartitioning(&childCopy)
		ps.Children = append(ps.Children, *resolved)
	}
	return ps
}

// enrich materializes auto-indexes for FK columns that lack index coverage.
func enrich(schema *Schema) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	for i := range schema.Tables {
		t := &schema.Tables[i]
		for _, fk := range t.FKs {
			if !t.HasIndexCovering(fk.Columns) {
				// TODO: sql.ConstraintName will replace this naming logic.
				idxName := fmt.Sprintf("idx_%s_%s", t.Name, strings.Join(fk.Columns, "_"))
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
