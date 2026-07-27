package parse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smm-h/pgdesign/internal/diagnostic"

	tomledit "github.com/smm-h/go-toml-edit"
)

// File parses a single TOML schema file and returns a RawSchema with diagnostics.
// It continues past errors, returning partial results even on failure.
func File(path string) (*RawSchema, []diagnostic.Diagnostic) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []diagnostic.Diagnostic{{
			Severity: diagnostic.Error,
			Code:     "E001",
			File:     path,
			Message:  fmt.Sprintf("cannot read file: %v", err),
		}}
	}

	// Document-shape gate: strictspec validates well-formedness, closed records
	// (unknown keys), base types, required fields, and the custom-scalar lexemes
	// BEFORE the native walk. On failure, return diagnostics without walking.
	if gateDiags := gateDocument(data, path); len(gateDiags) > 0 {
		return nil, gateDiags
	}

	doc, err := tomledit.Parse(data)
	if err != nil {
		return nil, []diagnostic.Diagnostic{{
			Severity: diagnostic.Error,
			Code:     "E002",
			File:     path,
			Message:  fmt.Sprintf("TOML parse error: %v", err),
		}}
	}

	p := &parser{
		doc:  doc,
		file: path,
	}
	schema := p.walk()
	schema.SourceFile = filepath.Base(path)
	return schema, p.diags
}

// Bytes parses TOML bytes and returns a RawSchema with diagnostics.
// Like File but operates on in-memory bytes instead of reading from disk.
func Bytes(data []byte) (*RawSchema, []diagnostic.Diagnostic) {
	// Document-shape gate first (see gateDocument / File).
	if gateDiags := gateDocument(data, "<bytes>"); len(gateDiags) > 0 {
		return nil, gateDiags
	}

	doc, err := tomledit.Parse(data)
	if err != nil {
		return nil, []diagnostic.Diagnostic{{
			Severity: diagnostic.Error,
			Code:     "E002",
			Message:  fmt.Sprintf("TOML parse error: %v", err),
		}}
	}

	p := &parser{
		doc:  doc,
		file: "<bytes>",
	}
	schema := p.walk()
	schema.SourceFile = "<bytes>"
	return schema, p.diags
}

// Files parses multiple TOML schema files and returns all schemas with
// aggregated diagnostics.
func Files(paths []string) ([]*RawSchema, []diagnostic.Diagnostic) {
	var schemas []*RawSchema
	var allDiags []diagnostic.Diagnostic

	for _, path := range paths {
		schema, diags := File(path)
		allDiags = append(allDiags, diags...)
		if schema != nil {
			schemas = append(schemas, schema)
		}
	}

	return schemas, allDiags
}

// Dir finds all .toml schema files in a directory (excluding pgdesign.toml),
// parses each, and returns all schemas with aggregated diagnostics.
func Dir(dirPath string) ([]*RawSchema, []diagnostic.Diagnostic) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, []diagnostic.Diagnostic{{
			Severity: diagnostic.Error,
			Code:     "E001",
			File:     dirPath,
			Message:  fmt.Sprintf("cannot read directory: %v", err),
		}}
	}

	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".toml") {
			continue
		}
		if name == "pgdesign.toml" {
			continue
		}
		paths = append(paths, filepath.Join(dirPath, name))
	}

	if len(paths) == 0 {
		return nil, []diagnostic.Diagnostic{{
			Severity: diagnostic.Error,
			Code:     "E001",
			File:     dirPath,
			Message:  "no .toml schema files found in directory",
		}}
	}

	return Files(paths)
}

// parser holds state during AST walking.
type parser struct {
	doc   *tomledit.DocumentNode
	file  string
	diags []diagnostic.Diagnostic
}

func (p *parser) errorf(code, table, column, msg string, args ...any) {
	p.diags = append(p.diags, diagnostic.Diagnostic{
		Severity: diagnostic.Error,
		Code:     code,
		File:     p.file,
		Table:    table,
		Column:   column,
		Message:  fmt.Sprintf(msg, args...),
	})
}

func (p *parser) warnf(code, table, column, msg string, args ...any) {
	p.diags = append(p.diags, diagnostic.Diagnostic{
		Severity: diagnostic.Warning,
		Code:     code,
		File:     p.file,
		Table:    table,
		Column:   column,
		Message:  fmt.Sprintf(msg, args...),
	})
}

func (p *parser) walk() *RawSchema {
	schema := &RawSchema{}
	schema.Meta = p.parseMeta()
	schema.Types = p.parseTypes()
	schema.Tables = p.parseTables()
	schema.Views = p.parseViews()
	schema.MaterializedViews = p.parseMaterializedViews()
	schema.Sequences = p.parseSequences()
	schema.Functions = p.parseFunctions()
	schema.Groups = p.parseGroups()
	return schema
}

// parseMeta extracts the [meta] section.
func (p *parser) parseMeta() RawMeta {
	meta := RawMeta{}

	node := p.doc.Get("meta")
	if node == nil {
		return meta
	}

	metaTable := p.findTable("meta")
	if metaTable == nil {
		return meta
	}

	for _, child := range metaTable.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "version":
			if v, ok := nodeInt(kv.Val); ok {
				meta.Version = int(v)
			}
		case "schema":
			if v, ok := nodeString(kv.Val); ok {
				meta.Schema = v
			}
		case "extensions":
			if v, ok := nodeStringSlice(kv.Val); ok {
				meta.Extensions = v
			}
		}
	}
	return meta
}

// parseTypes extracts all [types.*] sections in source order.
func (p *parser) parseTypes() []RawType {
	var types []RawType

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 2 && tbl.KeyPath[0] == "types" {
			typeName := tbl.KeyPath[1]
			rt := p.parseType(typeName, tbl)
			types = append(types, rt)
		}
	}

	// Second pass: find [types.*.fields] sub-tables and attach to the
	// corresponding RawType. Build a name->index map for lookup.
	typeIndex := make(map[string]int, len(types))
	for i, rt := range types {
		typeIndex[rt.Name] = i
	}
	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 3 && tbl.KeyPath[0] == "types" && tbl.KeyPath[2] == "fields" {
			typeName := tbl.KeyPath[1]
			idx, exists := typeIndex[typeName]
			if !exists {
				p.warnf("W001", "", "", "[types.%s.fields] has no parent [types.%s] section", typeName, typeName)
				continue
			}
			// Append in document order: field order is semantic (it becomes
			// the PostgreSQL composite field order). Duplicate field names
			// are rejected downstream by semtype (E103).
			var fields []RawCompositeField
			for _, fc := range tbl.Children {
				kv, ok := fc.(*tomledit.KeyValueNode)
				if !ok {
					continue
				}
				fieldName := kv.Key.Parts[0]
				if v, ok := nodeString(kv.Val); ok {
					fields = append(fields, RawCompositeField{Name: fieldName, Type: v})
				}
			}
			types[idx].Fields = fields
		}
		// [types.*.states.*] sub-tables (4-element keypaths)
		if len(tbl.KeyPath) == 4 && tbl.KeyPath[0] == "types" && tbl.KeyPath[2] == "states" {
			typeName := tbl.KeyPath[1]
			stateName := tbl.KeyPath[3]
			idx, exists := typeIndex[typeName]
			if !exists {
				p.warnf("W001", "", "", "[types.%s.states.%s] has no parent [types.%s] section", typeName, stateName, typeName)
				continue
			}
			state := RawSMState{Name: stateName}
			for _, fc := range tbl.Children {
				kv, ok := fc.(*tomledit.KeyValueNode)
				if !ok {
					continue
				}
				key := kv.Key.Parts[0]
				switch key {
				case "terminal":
					if v, ok := nodeBool(kv.Val); ok {
						state.Terminal = &v
					}
				case "comment":
					if v, ok := nodeString(kv.Val); ok {
						state.Comment = &v
					}
				}
			}
			// Append in document order: state order is semantic (it becomes
			// the PostgreSQL enum value order). Duplicate state names are
			// rejected downstream by semtype (E111).
			types[idx].States = append(types[idx].States, state)
		}
	}

	// Third pass: find [[types.*.transitions]] array-of-tables.
	for _, child := range p.doc.Children {
		at, ok := child.(*tomledit.ArrayTableNode)
		if !ok {
			continue
		}
		if len(at.KeyPath) == 3 && at.KeyPath[0] == "types" && at.KeyPath[2] == "transitions" {
			typeName := at.KeyPath[1]
			idx, exists := typeIndex[typeName]
			if !exists {
				p.warnf("W001", "", "", "[[types.%s.transitions]] has no parent [types.%s] section", typeName, typeName)
				continue
			}
			tr := p.parseTypeTransition(typeName, at)
			types[idx].Transitions = append(types[idx].Transitions, tr)
		}
	}

	return types
}

// parseTypeTransition parses a single [[types.<name>.transitions]] entry.
func (p *parser) parseTypeTransition(typeName string, at *tomledit.ArrayTableNode) RawSMTransition {
	tr := RawSMTransition{}

	for _, child := range at.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "name":
			if v, ok := nodeString(kv.Val); ok {
				tr.Name = v
			}
		case "from":
			if v, ok := nodeStringSlice(kv.Val); ok {
				tr.From = v
			}
		case "to":
			if v, ok := nodeString(kv.Val); ok {
				tr.To = v
			}
		case "requires":
			if m, ok := nodeStringMap(kv.Val); ok {
				tr.Requires = m
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				tr.Comment = &v
			}
		}
	}

	// name/from/to required — enforced by the strictspec shape gate.

	return tr
}

func (p *parser) parseType(name string, tbl *tomledit.TableNode) RawType {
	rt := RawType{Name: name}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "kind":
			if v, ok := nodeString(kv.Val); ok {
				rt.Kind = v
			}
		case "extends":
			if v, ok := nodeString(kv.Val); ok {
				rt.Extends = &v
			}
		case "base_type":
			if v, ok := nodeString(kv.Val); ok {
				rt.BaseType = v
			}
		case "values":
			if v, ok := nodeStringSlice(kv.Val); ok {
				rt.Values = v
			}
		case "not_null":
			if v, ok := nodeBool(kv.Val); ok {
				rt.NotNull = &v
			}
		case "default":
			if v, ok := nodeString(kv.Val); ok {
				rt.Default = &v
			}
		case "default_expr":
			if v, ok := nodeString(kv.Val); ok {
				rt.DefaultExpr = &v
			}
		case "check":
			if v, ok := nodeString(kv.Val); ok {
				rt.Check = &v
			}
		case "unique":
			if v, ok := nodeBool(kv.Val); ok {
				rt.Unique = &v
			}
		case "array":
			if v, ok := nodeBool(kv.Val); ok {
				rt.Array = &v
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				rt.Comment = &v
			}
		case "initial":
			if v, ok := nodeString(kv.Val); ok {
				rt.InitialState = &v
			}
		case "enforce":
			if v, ok := nodeBool(kv.Val); ok {
				rt.EnforceTrigger = &v
			}
		}
	}

	return rt
}

// parseTables extracts all [tables.*] sections in source order.
func (p *parser) parseTables() []RawTable {
	var tables []RawTable

	// Find all top-level table nodes with path [tables, <name>]
	// and collect unique table names in order of first appearance
	seen := map[string]bool{}
	var tableNames []string
	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) >= 2 && tbl.KeyPath[0] == "tables" {
			name := tbl.KeyPath[1]
			if !seen[name] {
				seen[name] = true
				tableNames = append(tableNames, name)
			}
		}
	}

	for _, name := range tableNames {
		rt := p.parseTable(name)
		tables = append(tables, rt)
	}

	return tables
}

// parseViews extracts all [views.*] sections in source order.
func (p *parser) parseViews() []RawView {
	var views []RawView

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 2 && tbl.KeyPath[0] == "views" {
			viewName := tbl.KeyPath[1]
			rv := p.parseView(viewName, tbl)
			views = append(views, rv)
		}
	}

	return views
}

func (p *parser) parseView(name string, tbl *tomledit.TableNode) RawView {
	rv := RawView{Name: name}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "query":
			if v, ok := nodeString(kv.Val); ok {
				rv.Query = v
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				rv.Comment = &v
			}
		case "depends_on":
			if v, ok := nodeStringSlice(kv.Val); ok {
				rv.DependsOn = v
			}
		}
	}

	// query required — enforced by the strictspec shape gate.

	return rv
}

// parseMaterializedViews extracts all [materialized_views.*] sections in source order.
func (p *parser) parseMaterializedViews() []RawMaterializedView {
	var matviews []RawMaterializedView

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 2 && tbl.KeyPath[0] == "materialized_views" {
			mvName := tbl.KeyPath[1]
			rmv := p.parseMaterializedView(mvName, tbl)
			matviews = append(matviews, rmv)
		}
	}

	return matviews
}

func (p *parser) parseMaterializedView(name string, tbl *tomledit.TableNode) RawMaterializedView {
	rmv := RawMaterializedView{Name: name}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "query":
			if v, ok := nodeString(kv.Val); ok {
				rmv.Query = v
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				rmv.Comment = &v
			}
		case "depends_on":
			if v, ok := nodeStringSlice(kv.Val); ok {
				rmv.DependsOn = v
			}
		case "with_data":
			if v, ok := nodeBool(kv.Val); ok {
				rmv.WithData = &v
			}
		}
	}

	// query required — enforced by the strictspec shape gate.

	// Parse indexes
	rmv.Indexes = make(map[string]RawIndex)
	for _, child := range p.doc.Children {
		tbl2, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl2.KeyPath) == 4 && tbl2.KeyPath[0] == "materialized_views" && tbl2.KeyPath[1] == name && tbl2.KeyPath[2] == "indexes" {
			idxName := tbl2.KeyPath[3]
			idx := p.parseIndex(name, idxName, tbl2)
			rmv.Indexes[idxName] = idx
		}
	}

	return rmv
}

// parseSequences extracts all [sequences.*] sections in source order.
func (p *parser) parseSequences() []RawSequence {
	var seqs []RawSequence

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 2 && tbl.KeyPath[0] == "sequences" {
			seqName := tbl.KeyPath[1]
			rs := p.parseSequence(seqName, tbl)
			seqs = append(seqs, rs)
		}
	}

	return seqs
}

func (p *parser) parseSequence(name string, tbl *tomledit.TableNode) RawSequence {
	rs := RawSequence{Name: name}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "start":
			if v, ok := nodeInt(kv.Val); ok {
				rs.Start = &v
			}
		case "increment":
			if v, ok := nodeInt(kv.Val); ok {
				rs.Increment = &v
			}
		case "min_value":
			if v, ok := nodeInt(kv.Val); ok {
				rs.MinValue = &v
			}
		case "max_value":
			if v, ok := nodeInt(kv.Val); ok {
				rs.MaxValue = &v
			}
		case "cache":
			if v, ok := nodeInt(kv.Val); ok {
				rs.Cache = &v
			}
		case "cycle":
			if v, ok := nodeBool(kv.Val); ok {
				rs.Cycle = &v
			}
		case "owned_by":
			if v, ok := nodeString(kv.Val); ok {
				rs.OwnedBy = &v
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				rs.Comment = &v
			}
		}
	}

	return rs
}

// parseFunctions extracts all [functions.*] sections in source order.
func (p *parser) parseFunctions() []RawFunction {
	var funcs []RawFunction

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 2 && tbl.KeyPath[0] == "functions" {
			funcName := tbl.KeyPath[1]
			rf := p.parseFunction(funcName, tbl)
			funcs = append(funcs, rf)
		}
	}

	return funcs
}

func (p *parser) parseFunction(name string, tbl *tomledit.TableNode) RawFunction {
	rf := RawFunction{Name: name}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "language":
			if v, ok := nodeString(kv.Val); ok {
				rf.Language = &v
			}
		case "returns":
			if v, ok := nodeString(kv.Val); ok {
				rf.Returns = &v
			}
		case "body":
			if v, ok := nodeString(kv.Val); ok {
				rf.Body = &v
			}
		case "file":
			if v, ok := nodeString(kv.Val); ok {
				rf.File = &v
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				rf.Comment = &v
			}
		case "volatility":
			if v, ok := nodeString(kv.Val); ok {
				rf.Volatility = &v
			}
		case "parallel":
			if v, ok := nodeString(kv.Val); ok {
				rf.Parallel = &v
			}
		case "security_definer":
			if v, ok := nodeBool(kv.Val); ok {
				rf.SecurityDefiner = &v
			}
		case "procedure":
			if v, ok := nodeBool(kv.Val); ok {
				rf.Procedure = &v
			}
		case "cost":
			if v, ok := nodeFloat(kv.Val); ok {
				rf.Cost = &v
			}
		case "rows":
			if v, ok := nodeFloat(kv.Val); ok {
				rf.Rows = &v
			}
		case "depends_on":
			if v, ok := nodeStringSlice(kv.Val); ok {
				rf.DependsOn = v
			}
		}
	}

	// Validate required fields. language is gate-owned; body-or-file (either),
	// both-body-and-file (exclusion), and returns (conditional on procedure)
	// remain native — the shape gate cannot express these cross-field rules.
	isProcedure := rf.Procedure != nil && *rf.Procedure

	if rf.Body == nil && rf.File == nil {
		p.errorf("E011", "", "", "function %q is missing required field \"body\" or \"file\"", name)
	} else if rf.Body != nil && rf.File != nil {
		p.errorf("E010", "", "", "function %q cannot set both \"body\" and \"file\"", name)
	}

	if !isProcedure && rf.Returns == nil {
		p.errorf("E011", "", "", "function %q is missing required field \"returns\"", name)
	}

	// File reference handling: read file content at parse time.
	if rf.File != nil && rf.Body == nil && p.file != "<bytes>" {
		schemaDir := filepath.Dir(p.file)
		filePath := filepath.Join(schemaDir, *rf.File)
		data, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				p.errorf("E012", "", "", "function %q file not found: %s", name, filePath)
			}
		} else {
			body := string(data)
			rf.Body = &body
		}
	}

	// Parse args
	rf.Args = p.parseFunctionArgs(name)

	return rf
}

// parseFunctionArgs extracts [[functions.<name>.args]] array-of-tables.
func (p *parser) parseFunctionArgs(funcName string) []RawFunctionArg {
	var args []RawFunctionArg
	target := []string{"functions", funcName, "args"}

	for _, child := range p.doc.Children {
		at, ok := child.(*tomledit.ArrayTableNode)
		if !ok {
			continue
		}
		if pathsEqual(at.KeyPath, target) {
			arg := RawFunctionArg{}

			for _, child := range at.Children {
				kv, ok := child.(*tomledit.KeyValueNode)
				if !ok {
					continue
				}
				key := kv.Key.Parts[0]
				switch key {
				case "name":
					if v, ok := nodeString(kv.Val); ok {
						arg.Name = v
					}
				case "type":
					if v, ok := nodeString(kv.Val); ok {
						arg.Type = v
					}
				case "default":
					if v, ok := nodeString(kv.Val); ok {
						arg.Default = &v
					}
				}
			}

			// name/type required — enforced by the strictspec shape gate.

			args = append(args, arg)
		}
	}

	return args
}

// parseGroups extracts the [groups] section: a flat table mapping group names
// to string arrays of table names.
func (p *parser) parseGroups() map[string][]string {
	groupsTable := p.findTable("groups")
	if groupsTable == nil {
		return nil
	}

	groups := make(map[string][]string)
	for _, child := range groupsTable.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		name := kv.Key.Parts[0]
		if tables, ok := nodeStringSlice(kv.Val); ok {
			groups[name] = tables
		}
	}

	if len(groups) == 0 {
		return nil
	}
	return groups
}

func (p *parser) parseTable(name string) RawTable {
	rt := RawTable{
		Name:       name,
		FKs:        make(map[string]RawFK),
		Indexes:    make(map[string]RawIndex),
		Uniques:    make(map[string]RawUnique),
		Checks:     make(map[string]RawCheck),
		Exclusions: make(map[string]RawExclusion),
		Policies:   make(map[string]RawPolicy),
		Triggers:   make(map[string]RawTrigger),
	}

	// Find the [tables.<name>] table node for top-level keys
	tableTbl := p.findTableByPath([]string{"tables", name})
	if tableTbl != nil {
		for _, child := range tableTbl.Children {
			kv, ok := child.(*tomledit.KeyValueNode)
			if !ok {
				continue
			}
			key := kv.Key.Parts[0]
			switch key {
			case "comment":
				if v, ok := nodeString(kv.Val); ok {
					rt.Comment = &v
				}
			case "pk":
				if v, ok := nodeStringSlice(kv.Val); ok {
					rt.PK = v
				}
			case "enable_rls":
				if v, ok := nodeBool(kv.Val); ok {
					rt.EnableRLS = v
				}
			case "force_rls":
				if v, ok := nodeBool(kv.Val); ok {
					rt.ForceRLS = v
				}
			case "append_only":
				if v, ok := nodeBool(kv.Val); ok {
					rt.AppendOnly = &v
				}
			}
		}
	}

	// Parse columns in source order
	rt.Columns = p.parseColumns(name)

	// Parse FKs
	p.parseFKs(name, &rt)

	// Parse indexes
	p.parseIndexes(name, &rt)

	// Parse unique constraints
	p.parseUniques(name, &rt)

	// Parse checks
	p.parseChecks(name, &rt)

	// Parse exclusion constraints
	p.parseExclusions(name, &rt)

	// Parse policies
	p.parsePolicies(name, &rt)

	// Parse triggers
	p.parseTriggers(name, &rt)

	// Parse partitioning
	p.parsePartitioning(name, &rt)

	// Parse dependencies
	p.parseDependencies(name, &rt)

	// Parse maintenance
	p.parseMaintenance(name, &rt)

	return rt
}

// parseColumns extracts columns from [tables.<name>.columns.*] in source order.
func (p *parser) parseColumns(tableName string) []RawColumn {
	var columns []RawColumn

	prefix := []string{"tables", tableName, "columns"}

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		// Match [tables.<name>.columns.<colname>]
		if len(tbl.KeyPath) == 4 && pathHasPrefix(tbl.KeyPath, prefix) {
			colName := tbl.KeyPath[3]
			col := p.parseColumn(tableName, colName, tbl)
			columns = append(columns, col)
		}
	}

	return columns
}

func (p *parser) parseColumn(tableName, colName string, tbl *tomledit.TableNode) RawColumn {
	col := RawColumn{Name: colName}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "type":
			if v, ok := nodeString(kv.Val); ok {
				col.Type = v
			}
		case "nullable":
			if v, ok := nodeBool(kv.Val); ok {
				col.Nullable = &v
			}
		case "default":
			if v, ok := nodeString(kv.Val); ok {
				col.Default = &v
			}
		case "default_expr":
			if v, ok := nodeString(kv.Val); ok {
				col.DefaultExpr = &v
			}
		case "generated":
			if v, ok := nodeString(kv.Val); ok {
				col.Generated = &v
			}
		case "stored":
			if v, ok := nodeBool(kv.Val); ok {
				col.Stored = &v
			}
		case "array":
			if v, ok := nodeBool(kv.Val); ok {
				col.Array = &v
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				col.Comment = &v
			}
		case "json_schema":
			if v, ok := nodeString(kv.Val); ok {
				col.JSONSchema = &v
			}
		case "collation":
			if v, ok := nodeString(kv.Val); ok {
				col.Collation = &v
			}
		case "statistics":
			if v, ok := nodeInt(kv.Val); ok {
				iv := int(v)
				col.Statistics = &iv
			}
		}
	}

	// Validate json_schema file if specified.
	if col.JSONSchema != nil && p.file != "<bytes>" {
		schemaDir := filepath.Dir(p.file)
		schemaPath := filepath.Join(schemaDir, *col.JSONSchema)
		data, err := os.ReadFile(schemaPath)
		if err != nil {
			p.errorf("E012", tableName, colName, "json_schema file not found: %s", schemaPath)
		} else {
			var js interface{}
			if jsonErr := json.Unmarshal(data, &js); jsonErr != nil {
				p.errorf("E013", tableName, colName, "json_schema file is not valid JSON: %s", jsonErr.Error())
			} else {
				col.JSONSchemaContent = data
			}
		}
	}

	// type required — enforced by the strictspec shape gate.

	return col
}

// parseFKs extracts foreign keys from [tables.<name>.fks.*].
func (p *parser) parseFKs(tableName string, rt *RawTable) {
	prefix := []string{"tables", tableName, "fks"}

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 4 && pathHasPrefix(tbl.KeyPath, prefix) {
			fkName := tbl.KeyPath[3]
			fk := p.parseFK(tableName, fkName, tbl)
			rt.FKs[fkName] = fk
		}
	}
}

func (p *parser) parseFK(tableName, fkName string, tbl *tomledit.TableNode) RawFK {
	fk := RawFK{Name: fkName}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "columns":
			if v, ok := nodeStringSlice(kv.Val); ok {
				fk.Columns = v
			}
		case "ref_table":
			if v, ok := nodeString(kv.Val); ok {
				fk.RefTable = v
			}
		case "ref_columns":
			if v, ok := nodeStringSlice(kv.Val); ok {
				fk.RefColumns = v
			}
		case "on_delete":
			if v, ok := nodeString(kv.Val); ok {
				fk.OnDelete = v
			}
		}
	}

	return fk
}

// parseIndexes extracts indexes from [tables.<name>.indexes.*].
func (p *parser) parseIndexes(tableName string, rt *RawTable) {
	prefix := []string{"tables", tableName, "indexes"}

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 4 && pathHasPrefix(tbl.KeyPath, prefix) {
			idxName := tbl.KeyPath[3]
			idx := p.parseIndex(tableName, idxName, tbl)
			rt.Indexes[idxName] = idx
		}
	}
}

func (p *parser) parseIndex(tableName, idxName string, tbl *tomledit.TableNode) RawIndex {
	idx := RawIndex{Name: idxName}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "columns":
			if v, ok := nodeStringSlice(kv.Val); ok {
				idx.Columns = v
			}
		case "method":
			if v, ok := nodeString(kv.Val); ok {
				idx.Method = &v
			}
		case "opclass":
			if v, ok := nodeString(kv.Val); ok {
				idx.Opclass = &v
			} else if m, ok := nodeStringMap(kv.Val); ok {
				idx.OpclassMap = m
			}
		case "collation":
			if v, ok := nodeString(kv.Val); ok {
				idx.Collation = &v
			} else if m, ok := nodeStringMap(kv.Val); ok {
				idx.CollationMap = m
			}
		case "where":
			if v, ok := nodeString(kv.Val); ok {
				idx.Where = &v
			}
		case "include":
			if v, ok := nodeStringSlice(kv.Val); ok {
				idx.Include = v
			}
		case "unique":
			if v, ok := nodeBool(kv.Val); ok {
				idx.Unique = &v
			}
		case "with":
			if m, ok := nodeStringMap(kv.Val); ok {
				idx.With = m
			}
		}
	}

	return idx
}

// parseUniques extracts unique constraints from [tables.<name>.unique.*].
func (p *parser) parseUniques(tableName string, rt *RawTable) {
	prefix := []string{"tables", tableName, "unique"}

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 4 && pathHasPrefix(tbl.KeyPath, prefix) {
			uqName := tbl.KeyPath[3]
			uq := p.parseUnique(tableName, uqName, tbl)
			rt.Uniques[uqName] = uq
		}
	}
}

func (p *parser) parseUnique(tableName, uqName string, tbl *tomledit.TableNode) RawUnique {
	uq := RawUnique{Name: uqName}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "columns":
			if v, ok := nodeStringSlice(kv.Val); ok {
				uq.Columns = v
			}
		case "deferrable":
			if v, ok := nodeBool(kv.Val); ok {
				uq.Deferrable = &v
			}
		case "initially_deferred":
			if v, ok := nodeBool(kv.Val); ok {
				uq.InitiallyDeferred = &v
			}
		}
	}

	return uq
}

// parseChecks extracts check constraints from [tables.<name>.checks.*].
func (p *parser) parseChecks(tableName string, rt *RawTable) {
	prefix := []string{"tables", tableName, "checks"}

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 4 && pathHasPrefix(tbl.KeyPath, prefix) {
			chkName := tbl.KeyPath[3]
			chk := p.parseCheck(tableName, chkName, tbl)
			rt.Checks[chkName] = chk
		}
	}
}

func (p *parser) parseCheck(tableName, chkName string, tbl *tomledit.TableNode) RawCheck {
	chk := RawCheck{Name: chkName}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "expr":
			if v, ok := nodeString(kv.Val); ok {
				chk.Expr = v
			}
		}
	}

	return chk
}

// parseExclusions extracts exclusion constraints from [tables.<name>.exclusions.*].
func (p *parser) parseExclusions(tableName string, rt *RawTable) {
	prefix := []string{"tables", tableName, "exclusions"}

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 4 && pathHasPrefix(tbl.KeyPath, prefix) {
			excName := tbl.KeyPath[3]
			exc := p.parseExclusion(tableName, excName, tbl)
			rt.Exclusions[excName] = exc
		}
	}
}

func (p *parser) parseExclusion(tableName, excName string, tbl *tomledit.TableNode) RawExclusion {
	exc := RawExclusion{Name: excName}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "columns":
			if v, ok := nodeStringSlice(kv.Val); ok {
				exc.Columns = v
			}
		case "operators":
			if v, ok := nodeStringSlice(kv.Val); ok {
				exc.Operators = v
			}
		case "method":
			if v, ok := nodeString(kv.Val); ok {
				exc.Method = &v
			}
		case "where":
			if v, ok := nodeString(kv.Val); ok {
				exc.Where = &v
			}
		case "deferrable":
			if v, ok := nodeBool(kv.Val); ok {
				exc.Deferrable = &v
			}
		case "initially_deferred":
			if v, ok := nodeBool(kv.Val); ok {
				exc.InitiallyDeferred = &v
			}
		}
	}

	// Validate: columns and operators must have the same length and at least one element.
	if len(exc.Columns) > 0 && len(exc.Operators) > 0 && len(exc.Columns) != len(exc.Operators) {
		p.errorf("E010", tableName, "", "[tables.%s.exclusions.%s]: columns and operators must have the same length (got %d columns, %d operators)", tableName, excName, len(exc.Columns), len(exc.Operators))
	}

	return exc
}

// parsePolicies extracts RLS policies from [tables.<name>.policies.*].
func (p *parser) parsePolicies(tableName string, rt *RawTable) {
	prefix := []string{"tables", tableName, "policies"}

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 4 && pathHasPrefix(tbl.KeyPath, prefix) {
			polName := tbl.KeyPath[3]
			pol := p.parsePolicy(tableName, polName, tbl)
			rt.Policies[polName] = pol
		}
	}
}

func (p *parser) parsePolicy(tableName, polName string, tbl *tomledit.TableNode) RawPolicy {
	pol := RawPolicy{Name: polName}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "type":
			if v, ok := nodeString(kv.Val); ok {
				pol.Type = v
			}
		case "for":
			if v, ok := nodeString(kv.Val); ok {
				pol.For = v
			}
		case "to":
			if v, ok := nodeString(kv.Val); ok {
				pol.To = v
			}
		case "using":
			if v, ok := nodeString(kv.Val); ok {
				pol.Using = v
			}
		case "with_check":
			if v, ok := nodeString(kv.Val); ok {
				pol.WithCheck = v
			}
		case "error_code":
			if v, ok := nodeString(kv.Val); ok {
				pol.ErrorCode = v
			}
		case "error_message":
			if v, ok := nodeString(kv.Val); ok {
				pol.ErrorMessage = v
			}
		}
	}

	return pol
}

// parseTriggers extracts triggers from [tables.<name>.triggers.*].
func (p *parser) parseTriggers(tableName string, rt *RawTable) {
	prefix := []string{"tables", tableName, "triggers"}

	for _, child := range p.doc.Children {
		tbl, ok := child.(*tomledit.TableNode)
		if !ok {
			continue
		}
		if len(tbl.KeyPath) == 4 && pathHasPrefix(tbl.KeyPath, prefix) {
			trigName := tbl.KeyPath[3]
			trig := p.parseTrigger(tableName, trigName, tbl)
			rt.Triggers[trigName] = trig
		}
	}
}

func (p *parser) parseTrigger(tableName, trigName string, tbl *tomledit.TableNode) RawTrigger {
	trig := RawTrigger{Name: trigName}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "function":
			if v, ok := nodeString(kv.Val); ok {
				trig.Function = v
			}
		case "events":
			if arr, ok := nodeStringSlice(kv.Val); ok {
				trig.Events = arr
			}
		case "timing":
			if v, ok := nodeString(kv.Val); ok {
				trig.Timing = v
			}
		case "for_each":
			if v, ok := nodeString(kv.Val); ok {
				trig.ForEach = &v
			}
		case "when":
			if v, ok := nodeString(kv.Val); ok {
				trig.When = &v
			}
		case "constraint":
			if v, ok := nodeBool(kv.Val); ok {
				trig.Constraint = &v
			}
		case "deferrable":
			if v, ok := nodeBool(kv.Val); ok {
				trig.Deferrable = &v
			}
		case "initially_deferred":
			if v, ok := nodeBool(kv.Val); ok {
				trig.InitiallyDeferred = &v
			}
		case "referencing_old":
			if v, ok := nodeString(kv.Val); ok {
				trig.ReferencingOld = &v
			}
		case "referencing_new":
			if v, ok := nodeString(kv.Val); ok {
				trig.ReferencingNew = &v
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				trig.Comment = &v
			}
		}
	}

	// function/events/timing required — enforced by the strictspec shape gate.

	return trig
}

// parsePartitioning extracts partitioning from [tables.<name>.partitioning].
func (p *parser) parsePartitioning(tableName string, rt *RawTable) {
	partTbl := p.findTableByPath([]string{"tables", tableName, "partitioning"})
	if partTbl == nil {
		return
	}

	part := p.parsePartitioningNode(tableName, partTbl)
	rt.Partitioning = &part
}

func (p *parser) parsePartitioningNode(tableName string, tbl *tomledit.TableNode) RawPartitioning {
	part := RawPartitioning{}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "strategy":
			if v, ok := nodeString(kv.Val); ok {
				part.Strategy = v
			}
		case "column":
			if v, ok := nodeString(kv.Val); ok {
				part.Column = v
			}
		case "columns":
			if v, ok := nodeStringSlice(kv.Val); ok {
				part.Columns = v
			}
		}
	}

	// Validate column/columns mutual exclusivity.
	if part.Column != "" && len(part.Columns) > 0 {
		p.errorf("E010", tableName, "", "[tables.%s.partitioning] cannot set both column and columns", tableName)
	}
	// Parent-level partitioning must specify a column.
	if part.Column == "" && len(part.Columns) == 0 {
		p.errorf("E010", tableName, "", "[tables.%s.partitioning] requires column or columns", tableName)
	}

	// Look for [[tables.<name>.partitioning.partitions]] array-of-tables
	prefix := append(tbl.KeyPath, "partitions")
	for _, child := range p.doc.Children {
		at, ok := child.(*tomledit.ArrayTableNode)
		if !ok {
			continue
		}
		if pathsEqual(at.KeyPath, prefix) {
			sub := p.parsePartitioningFromArrayTable(tableName, at)
			part.Partitions = append(part.Partitions, sub)
		}
	}

	return part
}

func (p *parser) parsePartitioningFromArrayTable(tableName string, at *tomledit.ArrayTableNode) RawPartitioning {
	part := RawPartitioning{}

	for _, child := range at.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "strategy":
			if v, ok := nodeString(kv.Val); ok {
				part.Strategy = v
			}
		case "column":
			if v, ok := nodeString(kv.Val); ok {
				part.Column = v
			}
		case "columns":
			if v, ok := nodeStringSlice(kv.Val); ok {
				part.Columns = v
			}
		case "name":
			if v, ok := nodeString(kv.Val); ok {
				part.Name = v
			}
		case "bound":
			if v, ok := nodeString(kv.Val); ok {
				part.Bound = v
			}
		}
	}

	return part
}

// parseDependencies extracts [[tables.<name>.dependencies]] array-of-tables.
func (p *parser) parseDependencies(tableName string, rt *RawTable) {
	target := []string{"tables", tableName, "dependencies"}

	for _, child := range p.doc.Children {
		at, ok := child.(*tomledit.ArrayTableNode)
		if !ok {
			continue
		}
		if pathsEqual(at.KeyPath, target) {
			dep := p.parseDependency(tableName, at)
			rt.Dependencies = append(rt.Dependencies, dep)
		}
	}
}

func (p *parser) parseDependency(tableName string, at *tomledit.ArrayTableNode) RawDependency {
	dep := RawDependency{}

	for _, child := range at.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "determinant":
			if v, ok := nodeStringSlice(kv.Val); ok {
				dep.Determinant = v
			}
		case "dependent":
			if v, ok := nodeStringSlice(kv.Val); ok {
				dep.Dependent = v
			}
		}
	}

	return dep
}

// parseMaintenance extracts [tables.<name>.maintenance].
func (p *parser) parseMaintenance(tableName string, rt *RawTable) {
	maintTbl := p.findTableByPath([]string{"tables", tableName, "maintenance"})
	if maintTbl == nil {
		return
	}

	maint := RawMaintenance{}

	for _, child := range maintTbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		switch key {
		case "interval":
			if v, ok := nodeString(kv.Val); ok {
				maint.Interval = &v
			}
		case "premake":
			if v, ok := nodeInt(kv.Val); ok {
				iv := int(v)
				maint.Premake = &iv
			}
		case "retention":
			if v, ok := nodeString(kv.Val); ok {
				maint.Retention = &v
			}
		case "retention_keep_table":
			if v, ok := nodeBool(kv.Val); ok {
				maint.RetentionKeepTable = &v
			}
		case "schedule":
			if v, ok := nodeString(kv.Val); ok {
				maint.Schedule = &v
			}
		}
	}

	rt.Maintenance = &maint
}

// --- Helpers ---

// findTable finds the first TableNode with a single-element KeyPath matching name.
func (p *parser) findTable(name string) *tomledit.TableNode {
	for _, child := range p.doc.Children {
		if tbl, ok := child.(*tomledit.TableNode); ok {
			if len(tbl.KeyPath) == 1 && tbl.KeyPath[0] == name {
				return tbl
			}
		}
	}
	return nil
}

// findTableByPath finds the first TableNode with a KeyPath matching path exactly.
func (p *parser) findTableByPath(path []string) *tomledit.TableNode {
	for _, child := range p.doc.Children {
		if tbl, ok := child.(*tomledit.TableNode); ok {
			if pathsEqual(tbl.KeyPath, path) {
				return tbl
			}
		}
	}
	return nil
}

// pathHasPrefix returns true if path starts with prefix.
func pathHasPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

// pathsEqual returns true if two paths are identical.
func pathsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// nodeString extracts a string value from a Node.
func nodeString(n tomledit.Node) (string, bool) {
	if n == nil {
		return "", false
	}
	if s, ok := n.(*tomledit.StringNode); ok {
		return s.Val, true
	}
	return "", false
}

// nodeInt extracts an integer value from a Node.
func nodeInt(n tomledit.Node) (int64, bool) {
	if n == nil {
		return 0, false
	}
	if i, ok := n.(*tomledit.IntegerNode); ok {
		return i.Val, true
	}
	return 0, false
}

// nodeBool extracts a boolean value from a Node.
func nodeBool(n tomledit.Node) (bool, bool) {
	if n == nil {
		return false, false
	}
	if b, ok := n.(*tomledit.BooleanNode); ok {
		return b.Val, true
	}
	return false, false
}

// nodeFloat extracts a float64 value from a Node.
// Accepts both FloatNode and IntegerNode (integers are valid floats).
func nodeFloat(n tomledit.Node) (float64, bool) {
	if n == nil {
		return 0, false
	}
	if f, ok := n.(*tomledit.FloatNode); ok {
		return f.Val, true
	}
	// Accept integer values as floats (cost = 100 is valid TOML).
	if i, ok := n.(*tomledit.IntegerNode); ok {
		return float64(i.Val), true
	}
	return 0, false
}

// nodeStringSlice extracts a []string from an ArrayNode.
func nodeStringSlice(n tomledit.Node) ([]string, bool) {
	if n == nil {
		return nil, false
	}
	arr, ok := n.(*tomledit.ArrayNode)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(arr.Elements))
	for _, elem := range arr.Elements {
		s, ok := elem.(*tomledit.StringNode)
		if !ok {
			return nil, false
		}
		result = append(result, s.Val)
	}
	return result, true
}

// nodeStringMap extracts a map[string]string from an InlineTableNode
// where all values are strings.
func nodeStringMap(n tomledit.Node) (map[string]string, bool) {
	if n == nil {
		return nil, false
	}
	it, ok := n.(*tomledit.InlineTableNode)
	if !ok {
		return nil, false
	}
	result := make(map[string]string, len(it.Children))
	for _, child := range it.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			return nil, false
		}
		if len(kv.Key.Parts) != 1 {
			return nil, false
		}
		v, ok := nodeString(kv.Val)
		if !ok {
			return nil, false
		}
		result[kv.Key.Parts[0]] = v
	}
	return result, true
}
