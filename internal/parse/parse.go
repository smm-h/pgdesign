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

	return types
}

func (p *parser) parseType(name string, tbl *tomledit.TableNode) RawType {
	rt := RawType{Name: name}

	knownKeys := map[string]bool{
		"kind": true, "base_type": true, "values": true,
		"not_null": true, "default": true, "default_expr": true,
		"check": true, "unique": true, "array": true, "comment": true,
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

func (p *parser) parseTable(name string) RawTable {
	rt := RawTable{
		Name:     name,
		FKs:      make(map[string]RawFK),
		Indexes:  make(map[string]RawIndex),
		Uniques:  make(map[string]RawUnique),
		Checks:   make(map[string]RawCheck),
		Policies: make(map[string]RawPolicy),
	}

	// Find the [tables.<name>] table node for top-level keys
	tableTbl := p.findTableByPath([]string{"tables", name})
	if tableTbl != nil {
		knownKeys := map[string]bool{
			"comment": true, "pk": true, "enable_rls": true, "append_only": true,
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
					key == "unique" || key == "checks" || key == "policies" ||
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

	// Parse policies
	p.parsePolicies(name, &rt)

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
		"columns": true, "method": true, "opclass": true,
		"where": true, "include": true, "unique": true,
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
		"for": true, "to": true, "using": true,
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
		}
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

