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
	schema.SourceFile = path
	return schema, p.diags
}

// Bytes parses TOML bytes and returns a RawSchema with diagnostics.
// Like File but operates on in-memory bytes instead of reading from disk.
func Bytes(data []byte) (*RawSchema, []diagnostic.Diagnostic) {
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

	knownKeys := map[string]bool{"version": true, "schema": true, "extensions": true}

	for _, child := range metaTable.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", "", "", "unknown key in [meta]: %q", key)
			continue
		}
		switch key {
		case "version":
			if v, ok := nodeInt(kv.Val); ok {
				meta.Version = int(v)
			} else {
				p.errorf("E010", "", "", "[meta].version must be an integer")
			}
		case "schema":
			if v, ok := nodeString(kv.Val); ok {
				meta.Schema = v
			} else {
				p.errorf("E010", "", "", "[meta].schema must be a string")
			}
		case "extensions":
			if v, ok := nodeStringSlice(kv.Val); ok {
				meta.Extensions = v
			} else {
				p.errorf("E010", "", "", "[meta].extensions must be an array of strings")
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
			fields := make(map[string]string)
			for _, fc := range tbl.Children {
				kv, ok := fc.(*tomledit.KeyValueNode)
				if !ok {
					continue
				}
				fieldName := kv.Key.Parts[0]
				if v, ok := nodeString(kv.Val); ok {
					fields[fieldName] = v
				} else {
					p.errorf("E010", "", "", "[types.%s.fields].%s must be a string", typeName, fieldName)
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
			if types[idx].States == nil {
				types[idx].States = make(map[string]RawSMState)
			}
			state := RawSMState{}
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
					} else {
						p.errorf("E010", "", "", "[types.%s.states.%s].terminal must be a boolean", typeName, stateName)
					}
				case "comment":
					if v, ok := nodeString(kv.Val); ok {
						state.Comment = &v
					} else {
						p.errorf("E010", "", "", "[types.%s.states.%s].comment must be a string", typeName, stateName)
					}
				default:
					p.warnf("W001", "", "", "unknown key in [types.%s.states.%s]: %q", typeName, stateName, key)
				}
			}
			types[idx].States[stateName] = state
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

	knownKeys := map[string]bool{
		"name": true, "from": true, "to": true, "requires": true, "comment": true,
	}

	for _, child := range at.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", "", "", "unknown key in [[types.%s.transitions]]: %q", typeName, key)
			continue
		}
		switch key {
		case "name":
			if v, ok := nodeString(kv.Val); ok {
				tr.Name = v
			} else {
				p.errorf("E010", "", "", "[[types.%s.transitions]].name must be a string", typeName)
			}
		case "from":
			if v, ok := nodeStringSlice(kv.Val); ok {
				tr.From = v
			} else {
				p.errorf("E010", "", "", "[[types.%s.transitions]].from must be an array of strings", typeName)
			}
		case "to":
			if v, ok := nodeString(kv.Val); ok {
				tr.To = v
			} else {
				p.errorf("E010", "", "", "[[types.%s.transitions]].to must be a string", typeName)
			}
		case "requires":
			if m, ok := nodeStringMap(kv.Val); ok {
				tr.Requires = m
			} else {
				p.errorf("E010", "", "", "[[types.%s.transitions]].requires must be an inline table of strings", typeName)
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				tr.Comment = &v
			} else {
				p.errorf("E010", "", "", "[[types.%s.transitions]].comment must be a string", typeName)
			}
		}
	}

	// Validate required fields.
	if tr.Name == "" {
		p.errorf("E011", "", "", "[[types.%s.transitions]] is missing required field \"name\"", typeName)
	}
	if len(tr.From) == 0 {
		p.errorf("E011", "", "", "[[types.%s.transitions]] is missing required field \"from\"", typeName)
	}
	if tr.To == "" {
		p.errorf("E011", "", "", "[[types.%s.transitions]] is missing required field \"to\"", typeName)
	}

	return tr
}

func (p *parser) parseType(name string, tbl *tomledit.TableNode) RawType {
	rt := RawType{Name: name}

	knownKeys := map[string]bool{
		"kind": true, "extends": true, "base_type": true, "values": true,
		"not_null": true, "default": true, "default_expr": true,
		"check": true, "unique": true, "array": true, "comment": true,
		"initial": true, "enforce": true,
	}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", "", "", "unknown key in [types.%s]: %q", name, key)
			continue
		}
		switch key {
		case "kind":
			if v, ok := nodeString(kv.Val); ok {
				rt.Kind = v
			} else {
				p.errorf("E010", "", "", "[types.%s].kind must be a string", name)
			}
		case "extends":
			if v, ok := nodeString(kv.Val); ok {
				rt.Extends = &v
			} else {
				p.errorf("E010", "", "", "[types.%s].extends must be a string", name)
			}
		case "base_type":
			if v, ok := nodeString(kv.Val); ok {
				rt.BaseType = v
			} else {
				p.errorf("E010", "", "", "[types.%s].base_type must be a string", name)
			}
		case "values":
			if v, ok := nodeStringSlice(kv.Val); ok {
				rt.Values = v
			} else {
				p.errorf("E010", "", "", "[types.%s].values must be an array of strings", name)
			}
		case "not_null":
			if v, ok := nodeBool(kv.Val); ok {
				rt.NotNull = &v
			} else {
				p.errorf("E010", "", "", "[types.%s].not_null must be a boolean", name)
			}
		case "default":
			if v, ok := nodeString(kv.Val); ok {
				rt.Default = &v
			} else {
				p.errorf("E010", "", "", "[types.%s].default must be a string", name)
			}
		case "default_expr":
			if v, ok := nodeString(kv.Val); ok {
				rt.DefaultExpr = &v
			} else {
				p.errorf("E010", "", "", "[types.%s].default_expr must be a string", name)
			}
		case "check":
			if v, ok := nodeString(kv.Val); ok {
				rt.Check = &v
			} else {
				p.errorf("E010", "", "", "[types.%s].check must be a string", name)
			}
		case "unique":
			if v, ok := nodeBool(kv.Val); ok {
				rt.Unique = &v
			} else {
				p.errorf("E010", "", "", "[types.%s].unique must be a boolean", name)
			}
		case "array":
			if v, ok := nodeBool(kv.Val); ok {
				rt.Array = &v
			} else {
				p.errorf("E010", "", "", "[types.%s].array must be a boolean", name)
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				rt.Comment = &v
			} else {
				p.errorf("E010", "", "", "[types.%s].comment must be a string", name)
			}
		case "initial":
			if v, ok := nodeString(kv.Val); ok {
				rt.InitialState = &v
			} else {
				p.errorf("E010", "", "", "[types.%s].initial must be a string", name)
			}
		case "enforce":
			if v, ok := nodeBool(kv.Val); ok {
				rt.EnforceTrigger = &v
			} else {
				p.errorf("E010", "", "", "[types.%s].enforce must be a boolean", name)
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

	knownKeys := map[string]bool{
		"query": true, "comment": true, "depends_on": true,
	}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", "", "", "unknown key in [views.%s]: %q", name, key)
			continue
		}
		switch key {
		case "query":
			if v, ok := nodeString(kv.Val); ok {
				rv.Query = v
			} else {
				p.errorf("E010", "", "", "[views.%s].query must be a string", name)
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				rv.Comment = &v
			} else {
				p.errorf("E010", "", "", "[views.%s].comment must be a string", name)
			}
		case "depends_on":
			if v, ok := nodeStringSlice(kv.Val); ok {
				rv.DependsOn = v
			} else {
				p.errorf("E010", "", "", "[views.%s].depends_on must be an array of strings", name)
			}
		}
	}

	// query is required
	if rv.Query == "" {
		p.errorf("E011", "", "", "view %q is missing required field \"query\"", name)
	}

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

	knownKeys := map[string]bool{
		"query": true, "comment": true, "depends_on": true, "with_data": true,
	}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", "", "", "unknown key in [materialized_views.%s]: %q", name, key)
			continue
		}
		switch key {
		case "query":
			if v, ok := nodeString(kv.Val); ok {
				rmv.Query = v
			} else {
				p.errorf("E010", "", "", "[materialized_views.%s].query must be a string", name)
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				rmv.Comment = &v
			} else {
				p.errorf("E010", "", "", "[materialized_views.%s].comment must be a string", name)
			}
		case "depends_on":
			if v, ok := nodeStringSlice(kv.Val); ok {
				rmv.DependsOn = v
			} else {
				p.errorf("E010", "", "", "[materialized_views.%s].depends_on must be an array of strings", name)
			}
		case "with_data":
			if v, ok := nodeBool(kv.Val); ok {
				rmv.WithData = &v
			} else {
				p.errorf("E010", "", "", "[materialized_views.%s].with_data must be a boolean", name)
			}
		}
	}

	// query is required
	if rmv.Query == "" {
		p.errorf("E011", "", "", "materialized view %q is missing required field \"query\"", name)
	}

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

	knownKeys := map[string]bool{
		"start": true, "increment": true, "min_value": true, "max_value": true,
		"cache": true, "cycle": true, "owned_by": true, "comment": true,
	}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", "", "", "unknown key in [sequences.%s]: %q", name, key)
			continue
		}
		switch key {
		case "start":
			if v, ok := nodeInt(kv.Val); ok {
				rs.Start = &v
			} else {
				p.errorf("E010", "", "", "[sequences.%s].start must be an integer", name)
			}
		case "increment":
			if v, ok := nodeInt(kv.Val); ok {
				rs.Increment = &v
			} else {
				p.errorf("E010", "", "", "[sequences.%s].increment must be an integer", name)
			}
		case "min_value":
			if v, ok := nodeInt(kv.Val); ok {
				rs.MinValue = &v
			} else {
				p.errorf("E010", "", "", "[sequences.%s].min_value must be an integer", name)
			}
		case "max_value":
			if v, ok := nodeInt(kv.Val); ok {
				rs.MaxValue = &v
			} else {
				p.errorf("E010", "", "", "[sequences.%s].max_value must be an integer", name)
			}
		case "cache":
			if v, ok := nodeInt(kv.Val); ok {
				rs.Cache = &v
			} else {
				p.errorf("E010", "", "", "[sequences.%s].cache must be an integer", name)
			}
		case "cycle":
			if v, ok := nodeBool(kv.Val); ok {
				rs.Cycle = &v
			} else {
				p.errorf("E010", "", "", "[sequences.%s].cycle must be a boolean", name)
			}
		case "owned_by":
			if v, ok := nodeString(kv.Val); ok {
				rs.OwnedBy = &v
			} else {
				p.errorf("E010", "", "", "[sequences.%s].owned_by must be a string", name)
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				rs.Comment = &v
			} else {
				p.errorf("E010", "", "", "[sequences.%s].comment must be a string", name)
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

	knownKeys := map[string]bool{
		"language": true, "returns": true, "body": true, "file": true,
		"comment": true, "volatility": true, "parallel": true,
		"security_definer": true, "procedure": true, "cost": true,
		"rows": true, "depends_on": true,
	}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", "", "", "unknown key in [functions.%s]: %q", name, key)
			continue
		}
		switch key {
		case "language":
			if v, ok := nodeString(kv.Val); ok {
				rf.Language = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].language must be a string", name)
			}
		case "returns":
			if v, ok := nodeString(kv.Val); ok {
				rf.Returns = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].returns must be a string", name)
			}
		case "body":
			if v, ok := nodeString(kv.Val); ok {
				rf.Body = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].body must be a string", name)
			}
		case "file":
			if v, ok := nodeString(kv.Val); ok {
				rf.File = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].file must be a string", name)
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				rf.Comment = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].comment must be a string", name)
			}
		case "volatility":
			if v, ok := nodeString(kv.Val); ok {
				rf.Volatility = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].volatility must be a string", name)
			}
		case "parallel":
			if v, ok := nodeString(kv.Val); ok {
				rf.Parallel = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].parallel must be a string", name)
			}
		case "security_definer":
			if v, ok := nodeBool(kv.Val); ok {
				rf.SecurityDefiner = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].security_definer must be a boolean", name)
			}
		case "procedure":
			if v, ok := nodeBool(kv.Val); ok {
				rf.Procedure = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].procedure must be a boolean", name)
			}
		case "cost":
			if v, ok := nodeFloat(kv.Val); ok {
				rf.Cost = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].cost must be a number", name)
			}
		case "rows":
			if v, ok := nodeFloat(kv.Val); ok {
				rf.Rows = &v
			} else {
				p.errorf("E010", "", "", "[functions.%s].rows must be a number", name)
			}
		case "depends_on":
			if v, ok := nodeStringSlice(kv.Val); ok {
				rf.DependsOn = v
			} else {
				p.errorf("E010", "", "", "[functions.%s].depends_on must be an array of strings", name)
			}
		}
	}

	// Validate required fields.
	isProcedure := rf.Procedure != nil && *rf.Procedure

	if rf.Language == nil {
		p.errorf("E011", "", "", "function %q is missing required field \"language\"", name)
	}

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
			} else {
				p.errorf("E010", "", "", "function %q cannot read file %s: %v", name, filePath, err)
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

			knownKeys := map[string]bool{
				"name": true, "type": true, "default": true,
			}

			for _, child := range at.Children {
				kv, ok := child.(*tomledit.KeyValueNode)
				if !ok {
					continue
				}
				key := kv.Key.Parts[0]
				if !knownKeys[key] {
					p.warnf("W001", "", "", "unknown key in [[functions.%s.args]]: %q", funcName, key)
					continue
				}
				switch key {
				case "name":
					if v, ok := nodeString(kv.Val); ok {
						arg.Name = v
					} else {
						p.errorf("E010", "", "", "[[functions.%s.args]].name must be a string", funcName)
					}
				case "type":
					if v, ok := nodeString(kv.Val); ok {
						arg.Type = v
					} else {
						p.errorf("E010", "", "", "[[functions.%s.args]].type must be a string", funcName)
					}
				case "default":
					if v, ok := nodeString(kv.Val); ok {
						arg.Default = &v
					} else {
						p.errorf("E010", "", "", "[[functions.%s.args]].default must be a string", funcName)
					}
				}
			}

			if arg.Name == "" {
				p.errorf("E011", "", "", "[[functions.%s.args]] is missing required field \"name\"", funcName)
			}
			if arg.Type == "" {
				p.errorf("E011", "", "", "[[functions.%s.args]] is missing required field \"type\"", funcName)
			}

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
		} else {
			p.errorf("E010", "", "", "[groups].%s must be an array of strings", name)
		}
	}

	if len(groups) == 0 {
		return nil
	}
	return groups
}

func (p *parser) parseTable(name string) RawTable {
	rt := RawTable{
		Name:     name,
		FKs:      make(map[string]RawFK),
		Indexes:  make(map[string]RawIndex),
		Uniques:  make(map[string]RawUnique),
		Checks:     make(map[string]RawCheck),
		Exclusions: make(map[string]RawExclusion),
		Policies:   make(map[string]RawPolicy),
		Triggers:   make(map[string]RawTrigger),
	}

	// Find the [tables.<name>] table node for top-level keys
	tableTbl := p.findTableByPath([]string{"tables", name})
	if tableTbl != nil {
		knownKeys := map[string]bool{
			"comment": true, "pk": true, "enable_rls": true, "force_rls": true, "append_only": true,
		}
		for _, child := range tableTbl.Children {
			kv, ok := child.(*tomledit.KeyValueNode)
			if !ok {
				continue
			}
			key := kv.Key.Parts[0]
			if !knownKeys[key] {
				// Could be a dotted key for sub-sections; skip known sub-section prefixes
				if key == "columns" || key == "fks" || key == "indexes" ||
					key == "unique" || key == "checks" || key == "exclusions" || key == "policies" || key == "triggers" ||
					key == "partitioning" || key == "dependencies" || key == "maintenance" {
					continue
				}
				p.warnf("W001", name, "", "unknown key in [tables.%s]: %q", name, key)
				continue
			}
			switch key {
			case "comment":
				if v, ok := nodeString(kv.Val); ok {
					rt.Comment = &v
				} else {
					p.errorf("E010", name, "", "[tables.%s].comment must be a string", name)
				}
			case "pk":
				if v, ok := nodeStringSlice(kv.Val); ok {
					rt.PK = v
				} else {
					p.errorf("E010", name, "", "[tables.%s].pk must be an array of strings", name)
				}
			case "enable_rls":
				if v, ok := nodeBool(kv.Val); ok {
					rt.EnableRLS = v
				} else {
					p.errorf("E010", name, "", "[tables.%s].enable_rls must be a boolean", name)
				}
			case "force_rls":
				if v, ok := nodeBool(kv.Val); ok {
					rt.ForceRLS = v
				} else {
					p.errorf("E010", name, "", "[tables.%s].force_rls must be a boolean", name)
				}
			case "append_only":
				if v, ok := nodeBool(kv.Val); ok {
					rt.AppendOnly = &v
				} else {
					p.errorf("E010", name, "", "[tables.%s].append_only must be a boolean", name)
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

	knownKeys := map[string]bool{
		"type": true, "nullable": true, "default": true,
		"default_expr": true, "generated": true, "stored": true,
		"array": true, "comment": true, "json_schema": true,
		"collation": true, "statistics": true,
	}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", tableName, colName, "unknown key in [tables.%s.columns.%s]: %q", tableName, colName, key)
			continue
		}
		switch key {
		case "type":
			if v, ok := nodeString(kv.Val); ok {
				col.Type = v
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].type must be a string", tableName, colName)
			}
		case "nullable":
			if v, ok := nodeBool(kv.Val); ok {
				col.Nullable = &v
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].nullable must be a boolean", tableName, colName)
			}
		case "default":
			if v, ok := nodeString(kv.Val); ok {
				col.Default = &v
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].default must be a string", tableName, colName)
			}
		case "default_expr":
			if v, ok := nodeString(kv.Val); ok {
				col.DefaultExpr = &v
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].default_expr must be a string", tableName, colName)
			}
		case "generated":
			if v, ok := nodeString(kv.Val); ok {
				col.Generated = &v
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].generated must be a string", tableName, colName)
			}
		case "stored":
			if v, ok := nodeBool(kv.Val); ok {
				col.Stored = &v
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].stored must be a boolean", tableName, colName)
			}
		case "array":
			if v, ok := nodeBool(kv.Val); ok {
				col.Array = &v
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].array must be a boolean", tableName, colName)
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				col.Comment = &v
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].comment must be a string", tableName, colName)
			}
		case "json_schema":
			if v, ok := nodeString(kv.Val); ok {
				col.JSONSchema = &v
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].json_schema must be a string", tableName, colName)
			}
		case "collation":
			if v, ok := nodeString(kv.Val); ok {
				col.Collation = &v
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].collation must be a string", tableName, colName)
			}
		case "statistics":
			if v, ok := nodeInt(kv.Val); ok {
				iv := int(v)
				col.Statistics = &iv
			} else {
				p.errorf("E010", tableName, colName, "[tables.%s.columns.%s].statistics must be an integer", tableName, colName)
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

	// Missing type is an error but we continue with partial data
	if col.Type == "" {
		p.errorf("E011", tableName, colName, "column %q in table %q is missing required field \"type\"", colName, tableName)
	}

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

	knownKeys := map[string]bool{
		"columns": true, "ref_table": true, "ref_columns": true, "on_delete": true,
	}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", tableName, "", "unknown key in [tables.%s.fks.%s]: %q", tableName, fkName, key)
			continue
		}
		switch key {
		case "columns":
			if v, ok := nodeStringSlice(kv.Val); ok {
				fk.Columns = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.fks.%s].columns must be an array of strings", tableName, fkName)
			}
		case "ref_table":
			if v, ok := nodeString(kv.Val); ok {
				fk.RefTable = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.fks.%s].ref_table must be a string", tableName, fkName)
			}
		case "ref_columns":
			if v, ok := nodeStringSlice(kv.Val); ok {
				fk.RefColumns = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.fks.%s].ref_columns must be an array of strings", tableName, fkName)
			}
		case "on_delete":
			if v, ok := nodeString(kv.Val); ok {
				fk.OnDelete = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.fks.%s].on_delete must be a string", tableName, fkName)
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

	knownKeys := map[string]bool{
		"columns": true, "method": true, "opclass": true, "collation": true,
		"where": true, "include": true, "unique": true, "with": true,
	}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", tableName, "", "unknown key in [tables.%s.indexes.%s]: %q", tableName, idxName, key)
			continue
		}
		switch key {
		case "columns":
			if v, ok := nodeStringSlice(kv.Val); ok {
				idx.Columns = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.indexes.%s].columns must be an array of strings", tableName, idxName)
			}
		case "method":
			if v, ok := nodeString(kv.Val); ok {
				idx.Method = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.indexes.%s].method must be a string", tableName, idxName)
			}
		case "opclass":
			if v, ok := nodeString(kv.Val); ok {
				idx.Opclass = &v
			} else if m, ok := nodeStringMap(kv.Val); ok {
				idx.OpclassMap = m
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.indexes.%s].opclass must be a string or inline table of strings", tableName, idxName)
			}
		case "collation":
			if v, ok := nodeString(kv.Val); ok {
				idx.Collation = &v
			} else if m, ok := nodeStringMap(kv.Val); ok {
				idx.CollationMap = m
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.indexes.%s].collation must be a string or inline table of strings", tableName, idxName)
			}
		case "where":
			if v, ok := nodeString(kv.Val); ok {
				idx.Where = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.indexes.%s].where must be a string", tableName, idxName)
			}
		case "include":
			if v, ok := nodeStringSlice(kv.Val); ok {
				idx.Include = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.indexes.%s].include must be an array of strings", tableName, idxName)
			}
		case "unique":
			if v, ok := nodeBool(kv.Val); ok {
				idx.Unique = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.indexes.%s].unique must be a boolean", tableName, idxName)
			}
		case "with":
			if m, ok := nodeStringMap(kv.Val); ok {
				idx.With = m
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.indexes.%s].with must be an inline table of strings", tableName, idxName)
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
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.unique.%s].columns must be an array of strings", tableName, uqName)
			}
		case "deferrable":
			if v, ok := nodeBool(kv.Val); ok {
				uq.Deferrable = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.unique.%s].deferrable must be a boolean", tableName, uqName)
			}
		case "initially_deferred":
			if v, ok := nodeBool(kv.Val); ok {
				uq.InitiallyDeferred = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.unique.%s].initially_deferred must be a boolean", tableName, uqName)
			}
		default:
			p.warnf("W001", tableName, "", "unknown key in [tables.%s.unique.%s]: %q", tableName, uqName, key)
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
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.checks.%s].expr must be a string", tableName, chkName)
			}
		default:
			p.warnf("W001", tableName, "", "unknown key in [tables.%s.checks.%s]: %q", tableName, chkName, key)
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
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.exclusions.%s].columns must be an array of strings", tableName, excName)
			}
		case "operators":
			if v, ok := nodeStringSlice(kv.Val); ok {
				exc.Operators = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.exclusions.%s].operators must be an array of strings", tableName, excName)
			}
		case "method":
			if v, ok := nodeString(kv.Val); ok {
				exc.Method = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.exclusions.%s].method must be a string", tableName, excName)
			}
		case "where":
			if v, ok := nodeString(kv.Val); ok {
				exc.Where = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.exclusions.%s].where must be a string", tableName, excName)
			}
		case "deferrable":
			if v, ok := nodeBool(kv.Val); ok {
				exc.Deferrable = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.exclusions.%s].deferrable must be a boolean", tableName, excName)
			}
		case "initially_deferred":
			if v, ok := nodeBool(kv.Val); ok {
				exc.InitiallyDeferred = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.exclusions.%s].initially_deferred must be a boolean", tableName, excName)
			}
		default:
			p.warnf("W001", tableName, "", "unknown key in [tables.%s.exclusions.%s]: %q", tableName, excName, key)
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

	knownKeys := map[string]bool{
		"type": true, "for": true, "to": true, "using": true,
		"with_check": true, "error_code": true, "error_message": true,
	}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", tableName, "", "unknown key in [tables.%s.policies.%s]: %q", tableName, polName, key)
			continue
		}
		switch key {
		case "type":
			if v, ok := nodeString(kv.Val); ok {
				pol.Type = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.policies.%s].type must be a string", tableName, polName)
			}
		case "for":
			if v, ok := nodeString(kv.Val); ok {
				pol.For = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.policies.%s].for must be a string", tableName, polName)
			}
		case "to":
			if v, ok := nodeString(kv.Val); ok {
				pol.To = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.policies.%s].to must be a string", tableName, polName)
			}
		case "using":
			if v, ok := nodeString(kv.Val); ok {
				pol.Using = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.policies.%s].using must be a string", tableName, polName)
			}
		case "with_check":
			if v, ok := nodeString(kv.Val); ok {
				pol.WithCheck = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.policies.%s].with_check must be a string", tableName, polName)
			}
		case "error_code":
			if v, ok := nodeString(kv.Val); ok {
				pol.ErrorCode = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.policies.%s].error_code must be a string", tableName, polName)
			}
		case "error_message":
			if v, ok := nodeString(kv.Val); ok {
				pol.ErrorMessage = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.policies.%s].error_message must be a string", tableName, polName)
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

	knownKeys := map[string]bool{
		"function": true, "events": true, "timing": true, "for_each": true,
		"when": true, "constraint": true, "deferrable": true, "initially_deferred": true,
		"referencing_old": true, "referencing_new": true, "comment": true,
	}

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			continue
		}
		key := kv.Key.Parts[0]
		if !knownKeys[key] {
			p.warnf("W001", tableName, "", "unknown key in [tables.%s.triggers.%s]: %q", tableName, trigName, key)
			continue
		}
		switch key {
		case "function":
			if v, ok := nodeString(kv.Val); ok {
				trig.Function = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].function must be a string", tableName, trigName)
			}
		case "events":
			if arr, ok := nodeStringSlice(kv.Val); ok {
				trig.Events = arr
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].events must be an array of strings", tableName, trigName)
			}
		case "timing":
			if v, ok := nodeString(kv.Val); ok {
				trig.Timing = v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].timing must be a string", tableName, trigName)
			}
		case "for_each":
			if v, ok := nodeString(kv.Val); ok {
				trig.ForEach = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].for_each must be a string", tableName, trigName)
			}
		case "when":
			if v, ok := nodeString(kv.Val); ok {
				trig.When = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].when must be a string", tableName, trigName)
			}
		case "constraint":
			if v, ok := nodeBool(kv.Val); ok {
				trig.Constraint = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].constraint must be a boolean", tableName, trigName)
			}
		case "deferrable":
			if v, ok := nodeBool(kv.Val); ok {
				trig.Deferrable = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].deferrable must be a boolean", tableName, trigName)
			}
		case "initially_deferred":
			if v, ok := nodeBool(kv.Val); ok {
				trig.InitiallyDeferred = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].initially_deferred must be a boolean", tableName, trigName)
			}
		case "referencing_old":
			if v, ok := nodeString(kv.Val); ok {
				trig.ReferencingOld = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].referencing_old must be a string", tableName, trigName)
			}
		case "referencing_new":
			if v, ok := nodeString(kv.Val); ok {
				trig.ReferencingNew = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].referencing_new must be a string", tableName, trigName)
			}
		case "comment":
			if v, ok := nodeString(kv.Val); ok {
				trig.Comment = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.triggers.%s].comment must be a string", tableName, trigName)
			}
		}
	}

	// Validate required fields.
	if trig.Function == "" {
		p.errorf("E010", tableName, "", "[tables.%s.triggers.%s] missing required field \"function\"", tableName, trigName)
	}
	if len(trig.Events) == 0 {
		p.errorf("E010", tableName, "", "[tables.%s.triggers.%s] missing required field \"events\"", tableName, trigName)
	}
	if trig.Timing == "" {
		p.errorf("E010", tableName, "", "[tables.%s.triggers.%s] missing required field \"timing\"", tableName, trigName)
	}

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
		case "premake":
			if v, ok := nodeInt(kv.Val); ok {
				iv := int(v)
				maint.Premake = &iv
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.maintenance].premake must be an integer", tableName)
			}
		case "retention":
			if v, ok := nodeString(kv.Val); ok {
				maint.Retention = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.maintenance].retention must be a string", tableName)
			}
		case "retention_keep_table":
			if v, ok := nodeBool(kv.Val); ok {
				maint.RetentionKeepTable = &v
			} else {
				p.errorf("E010", tableName, "", "[tables.%s.maintenance].retention_keep_table must be a boolean", tableName)
			}
		default:
			p.warnf("W001", tableName, "", "unknown key in [tables.%s.maintenance]: %q", tableName, key)
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

