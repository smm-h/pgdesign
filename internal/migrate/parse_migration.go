package migrate

import (
	"fmt"
	"os"
	"strings"

	tomledit "github.com/smm-h/go-toml-edit"
)

// tomlMigration is the TOML-level representation of a migration file.
type tomlMigration struct {
	Description string    `toml:"description"`
	DDL         []tomlDDL `toml:"ddl"`
	DML         []tomlDML `toml:"dml"`
}

type tomlDDL struct {
	Op       string      `toml:"op"`
	Table    string      `toml:"table,omitempty"`
	Column   string      `toml:"column,omitempty"`
	Type     string      `toml:"type,omitempty"`
	Default  interface{} `toml:"default,omitempty"`
	NotNull  bool        `toml:"not_null,omitempty"`
	Name     string      `toml:"name,omitempty"`
	Columns  []string    `toml:"columns,omitempty"`
	RefTable string      `toml:"ref_table,omitempty"`
	RefCols  []string    `toml:"ref_cols,omitempty"`
	OnDelete string      `toml:"on_delete,omitempty"`
	Method   string      `toml:"method,omitempty"`
	Where    string      `toml:"where,omitempty"`
	Opclass  interface{} `toml:"opclass,omitempty"`
	Include  []string    `toml:"include,omitempty"`
	Comment  string      `toml:"comment,omitempty"`
	PK       []string    `toml:"pk,omitempty"`
	Values   []string    `toml:"values,omitempty"`
	Schema   string      `toml:"schema,omitempty"`
	Expr     string      `toml:"expr,omitempty"`
	Down     *tomlDown   `toml:"down,omitempty"`
}

type tomlDML struct {
	Op   string    `toml:"op"`
	SQL  string    `toml:"sql"`
	Down *tomlDown `toml:"down,omitempty"`
}

type tomlDown struct {
	Irreversible bool      `toml:"irreversible,omitempty"`
	Op           string    `toml:"op,omitempty"`
	Table        string    `toml:"table,omitempty"`
	Column       string    `toml:"column,omitempty"`
	Name         string    `toml:"name,omitempty"`
	Columns      []string  `toml:"columns,omitempty"`
	Ops          []tomlDDL `toml:"ops,omitempty"`
}

// ParseMigrationFile reads and parses a TOML migration file.
func ParseMigrationFile(path string) (*Migration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read migration file: %w", err)
	}
	return ParseMigration(string(data))
}

// ParseMigration parses a TOML migration string.
func ParseMigration(data string) (*Migration, error) {
	var tm tomlMigration
	if err := tomledit.Unmarshal([]byte(data), &tm); err != nil {
		return nil, fmt.Errorf("parse migration TOML: %w", err)
	}

	m := &Migration{
		Description: tm.Description,
	}

	for _, td := range tm.DDL {
		op, err := convertTomlDDL(td)
		if err != nil {
			return nil, err
		}
		m.DDLOps = append(m.DDLOps, op)
	}

	for _, td := range tm.DML {
		op := DMLOp{
			Op:  td.Op,
			SQL: td.SQL,
		}
		if td.Down != nil {
			op.Down = convertTomlDown(td.Down)
		}
		m.DMLOps = append(m.DMLOps, op)
	}

	return m, nil
}

func convertTomlDDL(td tomlDDL) (DDLOp, error) {
	op := DDLOp{
		Op:       td.Op,
		Table:    td.Table,
		Column:   td.Column,
		Type:     td.Type,
		Default:  td.Default,
		NotNull:  td.NotNull,
		Name:     td.Name,
		Columns:  td.Columns,
		RefTable: td.RefTable,
		RefCols:  td.RefCols,
		OnDelete: td.OnDelete,
		Method:   td.Method,
		Where:    td.Where,
		Include:  td.Include,
		Comment:  td.Comment,
		PK:       td.PK,
		Values:   td.Values,
		Schema:   td.Schema,
		Expr:     td.Expr,
	}
	// Convert opclass: string becomes a map applied to all columns,
	// map is copied directly.
	switch v := td.Opclass.(type) {
	case string:
		if v != "" {
			op.Opclasses = make(map[string]string, len(td.Columns))
			for _, col := range td.Columns {
				op.Opclasses[col] = v
			}
		}
	case map[string]interface{}:
		op.Opclasses = make(map[string]string, len(v))
		for k, val := range v {
			if s, ok := val.(string); ok {
				op.Opclasses[k] = s
			}
		}
	}
	if td.Down != nil {
		op.Down = convertTomlDown(td.Down)
	}
	return op, nil
}

func convertTomlDown(td *tomlDown) *DownOp {
	if td == nil {
		return nil
	}
	down := &DownOp{
		Irreversible: td.Irreversible,
	}

	// Single inline down op (op/table/column/name directly on down).
	if td.Op != "" {
		singleOp := DDLOp{
			Op:      td.Op,
			Table:   td.Table,
			Column:  td.Column,
			Name:    td.Name,
			Columns: td.Columns,
		}
		down.Ops = append(down.Ops, singleOp)
	}

	// Array of down ops.
	for _, dop := range td.Ops {
		converted, _ := convertTomlDDL(dop)
		down.Ops = append(down.Ops, converted)
	}

	return down
}

// WriteMigrationFile serializes a Migration to a TOML file.
func WriteMigrationFile(path string, m *Migration) error {
	content := FormatMigration(m)
	return os.WriteFile(path, []byte(content), 0o644)
}

// FormatMigration serializes a Migration to a TOML string.
func FormatMigration(m *Migration) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("description = %q\n", m.Description))

	for _, op := range m.DDLOps {
		b.WriteString("\n[[ddl]]\n")
		writeDDLOp(&b, &op)
	}

	for _, op := range m.DMLOps {
		b.WriteString("\n[[dml]]\n")
		writeDMLOp(&b, &op)
	}

	return b.String()
}

func writeDDLOp(b *strings.Builder, op *DDLOp) {
	b.WriteString(fmt.Sprintf("op = %q\n", op.Op))
	if op.Table != "" {
		b.WriteString(fmt.Sprintf("table = %q\n", op.Table))
	}
	if op.Column != "" {
		b.WriteString(fmt.Sprintf("column = %q\n", op.Column))
	}
	if op.Type != "" {
		b.WriteString(fmt.Sprintf("type = %q\n", op.Type))
	}
	if op.Default != nil {
		writeDefault(b, op.Default)
	}
	if op.NotNull {
		b.WriteString("not_null = true\n")
	}
	if op.Name != "" {
		b.WriteString(fmt.Sprintf("name = %q\n", op.Name))
	}
	if len(op.Columns) > 0 {
		b.WriteString(fmt.Sprintf("columns = %s\n", formatStringSlice(op.Columns)))
	}
	if op.RefTable != "" {
		b.WriteString(fmt.Sprintf("ref_table = %q\n", op.RefTable))
	}
	if len(op.RefCols) > 0 {
		b.WriteString(fmt.Sprintf("ref_cols = %s\n", formatStringSlice(op.RefCols)))
	}
	if op.OnDelete != "" {
		b.WriteString(fmt.Sprintf("on_delete = %q\n", op.OnDelete))
	}
	if op.Method != "" {
		b.WriteString(fmt.Sprintf("method = %q\n", op.Method))
	}
	if op.Where != "" {
		b.WriteString(fmt.Sprintf("where = %q\n", op.Where))
	}
	if len(op.Opclasses) > 0 {
		// Check if all values are the same -- use compact string form.
		allSame := true
		var singleVal string
		for _, v := range op.Opclasses {
			if singleVal == "" {
				singleVal = v
			} else if v != singleVal {
				allSame = false
				break
			}
		}
		if allSame && singleVal != "" {
			b.WriteString(fmt.Sprintf("opclass = %q\n", singleVal))
		} else {
			b.WriteString("opclass = { ")
			first := true
			for _, col := range op.Columns {
				if oc, ok := op.Opclasses[col]; ok {
					if !first {
						b.WriteString(", ")
					}
					b.WriteString(fmt.Sprintf("%s = %q", col, oc))
					first = false
				}
			}
			b.WriteString(" }\n")
		}
	}
	if len(op.Include) > 0 {
		b.WriteString(fmt.Sprintf("include = %s\n", formatStringSlice(op.Include)))
	}
	if op.Comment != "" {
		b.WriteString(fmt.Sprintf("comment = %q\n", op.Comment))
	}
	if len(op.PK) > 0 {
		b.WriteString(fmt.Sprintf("pk = %s\n", formatStringSlice(op.PK)))
	}
	if len(op.Values) > 0 {
		b.WriteString(fmt.Sprintf("values = %s\n", formatStringSlice(op.Values)))
	}
	if op.Schema != "" {
		b.WriteString(fmt.Sprintf("schema = %q\n", op.Schema))
	}
	if op.Expr != "" {
		b.WriteString(fmt.Sprintf("expr = %q\n", op.Expr))
	}

	if op.Down != nil {
		writeDownOp(b, op.Down)
	}
}

func writeDMLOp(b *strings.Builder, op *DMLOp) {
	b.WriteString(fmt.Sprintf("op = %q\n", op.Op))
	b.WriteString(fmt.Sprintf("sql = %q\n", op.SQL))
	if op.Down != nil {
		writeDownOp(b, op.Down)
	}
}

func writeDownOp(b *strings.Builder, down *DownOp) {
	if down.Irreversible {
		b.WriteString("down = { irreversible = true }\n")
		return
	}

	if len(down.Ops) == 1 {
		// Inline single down op.
		op := &down.Ops[0]
		parts := []string{fmt.Sprintf("op = %q", op.Op)}
		if op.Table != "" {
			parts = append(parts, fmt.Sprintf("table = %q", op.Table))
		}
		if op.Column != "" {
			parts = append(parts, fmt.Sprintf("column = %q", op.Column))
		}
		if op.Name != "" {
			parts = append(parts, fmt.Sprintf("name = %q", op.Name))
		}
		b.WriteString(fmt.Sprintf("down = { %s }\n", strings.Join(parts, ", ")))
		return
	}

	// Multiple down ops use [[down.ops]].
	b.WriteString("[down]\n")
	for _, op := range down.Ops {
		b.WriteString("[[down.ops]]\n")
		writeDDLOp(b, &op)
	}
}

func writeDefault(b *strings.Builder, val interface{}) {
	switch v := val.(type) {
	case int64:
		b.WriteString(fmt.Sprintf("default = %d\n", v))
	case float64:
		b.WriteString(fmt.Sprintf("default = %v\n", v))
	case bool:
		b.WriteString(fmt.Sprintf("default = %v\n", v))
	case string:
		b.WriteString(fmt.Sprintf("default = %q\n", v))
	default:
		b.WriteString(fmt.Sprintf("default = %q\n", fmt.Sprintf("%v", v)))
	}
}

func formatStringSlice(s []string) string {
	quoted := make([]string, len(s))
	for i, v := range s {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
