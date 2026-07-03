package migrate

import (
	"fmt"
	"os"
	"sort"
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
	Op                string            `toml:"op"`
	Phase             string            `toml:"phase,omitempty"`
	Table             string            `toml:"table,omitempty"`
	Column            string            `toml:"column,omitempty"`
	Type              string            `toml:"type,omitempty"`
	Collation         string            `toml:"collation,omitempty"`
	Statistics        *int              `toml:"statistics,omitempty"`
	Default           interface{}       `toml:"default,omitempty"`
	NotNull           bool              `toml:"not_null,omitempty"`
	Generated         string            `toml:"generated,omitempty"`
	Stored            bool              `toml:"stored"`
	PGVersion         int               `toml:"pg_version,omitempty"`
	Name              string            `toml:"name,omitempty"`
	Columns           []string          `toml:"columns,omitempty"`
	RefTable          string            `toml:"ref_table,omitempty"`
	RefCols           []string          `toml:"ref_cols,omitempty"`
	OnDelete          string            `toml:"on_delete,omitempty"`
	Method            string            `toml:"method,omitempty"`
	Where             string            `toml:"where,omitempty"`
	Opclass           interface{}       `toml:"opclass,omitempty"`
	IdxCollation      interface{}       `toml:"collations,omitempty"`
	Desc              []bool            `toml:"desc,omitempty"`
	Include           []string          `toml:"include,omitempty"`
	With              map[string]string `toml:"with,omitempty"`
	Comment           string            `toml:"comment,omitempty"`
	PK                []string          `toml:"pk,omitempty"`
	Values            []string          `toml:"values,omitempty"`
	Schema            string            `toml:"schema,omitempty"`
	Expr              string            `toml:"expr,omitempty"`
	Operators         []string          `toml:"operators,omitempty"`
	Deferrable        bool              `toml:"deferrable,omitempty"`
	InitiallyDeferred bool              `toml:"initially_deferred,omitempty"`
	Consolidated      []tomlDDL         `toml:"consolidated,omitempty"`
	Down              *tomlDown         `toml:"down,omitempty"`
}

type tomlDML struct {
	Op        string    `toml:"op"`
	Phase     string    `toml:"phase,omitempty"`
	SQL       string    `toml:"sql"`
	BatchSize int       `toml:"batch_size,omitempty"`
	Down      *tomlDown `toml:"down,omitempty"`
}

type tomlDown struct {
	Irreversible      bool      `toml:"irreversible,omitempty"`
	Op                string    `toml:"op,omitempty"`
	Table             string    `toml:"table,omitempty"`
	Column            string    `toml:"column,omitempty"`
	Name              string    `toml:"name,omitempty"`
	Columns           []string  `toml:"columns,omitempty"`
	Operators         []string  `toml:"operators,omitempty"`
	Deferrable        bool      `toml:"deferrable,omitempty"`
	InitiallyDeferred bool      `toml:"initially_deferred,omitempty"`
	Ops               []tomlDDL `toml:"ops,omitempty"`
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
			Op:        td.Op,
			Phase:     td.Phase,
			SQL:       td.SQL,
			BatchSize: td.BatchSize,
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
		Op:                td.Op,
		Phase:             td.Phase,
		Table:             td.Table,
		Column:            td.Column,
		Type:              td.Type,
		Collation:         td.Collation,
		Statistics:        td.Statistics,
		Default:           td.Default,
		NotNull:           td.NotNull,
		Generated:         td.Generated,
		Stored:            td.Stored,
		PGVersion:         td.PGVersion,
		Name:              td.Name,
		Columns:           td.Columns,
		RefTable:          td.RefTable,
		RefCols:           td.RefCols,
		OnDelete:          td.OnDelete,
		Method:            td.Method,
		Where:             td.Where,
		Desc:              td.Desc,
		Include:           td.Include,
		With:              td.With,
		Comment:           td.Comment,
		PK:                td.PK,
		Values:            td.Values,
		Schema:            td.Schema,
		Expr:              td.Expr,
		Operators:         td.Operators,
		Deferrable:        td.Deferrable,
		InitiallyDeferred: td.InitiallyDeferred,
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
	// Convert index collations: same pattern as opclass.
	switch v := td.IdxCollation.(type) {
	case string:
		if v != "" {
			op.Collations = make(map[string]string, len(td.Columns))
			for _, col := range td.Columns {
				op.Collations[col] = v
			}
		}
	case map[string]interface{}:
		op.Collations = make(map[string]string, len(v))
		for k, val := range v {
			if s, ok := val.(string); ok {
				op.Collations[k] = s
			}
		}
	}
	for _, ctd := range td.Consolidated {
		cop, err := convertTomlDDL(ctd)
		if err != nil {
			return DDLOp{}, fmt.Errorf("consolidated op: %w", err)
		}
		op.ConsolidatedOps = append(op.ConsolidatedOps, cop)
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
			Op:                td.Op,
			Table:             td.Table,
			Column:            td.Column,
			Name:              td.Name,
			Columns:           td.Columns,
			Operators:         td.Operators,
			Deferrable:        td.Deferrable,
			InitiallyDeferred: td.InitiallyDeferred,
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
	if op.Phase != "" {
		b.WriteString(fmt.Sprintf("phase = %q\n", op.Phase))
	}
	if op.Table != "" {
		b.WriteString(fmt.Sprintf("table = %q\n", op.Table))
	}
	if op.Column != "" {
		b.WriteString(fmt.Sprintf("column = %q\n", op.Column))
	}
	if op.Type != "" {
		b.WriteString(fmt.Sprintf("type = %q\n", op.Type))
	}
	if op.Collation != "" {
		b.WriteString(fmt.Sprintf("collation = %q\n", op.Collation))
	}
	if op.Statistics != nil {
		b.WriteString(fmt.Sprintf("statistics = %d\n", *op.Statistics))
	}
	if op.Default != nil {
		writeDefault(b, op.Default)
	}
	if op.NotNull {
		b.WriteString("not_null = true\n")
	}
	if op.Generated != "" {
		b.WriteString(fmt.Sprintf("generated = %q\n", op.Generated))
	}
	if op.Generated != "" {
		b.WriteString(fmt.Sprintf("stored = %v\n", op.Stored))
	}
	if op.PGVersion > 0 {
		b.WriteString(fmt.Sprintf("pg_version = %d\n", op.PGVersion))
	}
	if op.Name != "" {
		b.WriteString(fmt.Sprintf("name = %q\n", op.Name))
	}
	if len(op.Columns) > 0 {
		b.WriteString(fmt.Sprintf("columns = %s\n", formatStringSlice(op.Columns)))
	}
	if len(op.Desc) > 0 {
		b.WriteString(fmt.Sprintf("desc = %s\n", formatBoolSlice(op.Desc)))
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
	if len(op.Collations) > 0 {
		// Same pattern as opclasses: compact string if all same, inline table otherwise.
		allSame := true
		var singleVal string
		for _, v := range op.Collations {
			if singleVal == "" {
				singleVal = v
			} else if v != singleVal {
				allSame = false
				break
			}
		}
		if allSame && singleVal != "" {
			b.WriteString(fmt.Sprintf("collations = %q\n", singleVal))
		} else {
			b.WriteString("collations = { ")
			first := true
			for _, col := range op.Columns {
				if coll, ok := op.Collations[col]; ok {
					if !first {
						b.WriteString(", ")
					}
					b.WriteString(fmt.Sprintf("%s = %q", col, coll))
					first = false
				}
			}
			b.WriteString(" }\n")
		}
	}
	if len(op.Include) > 0 {
		b.WriteString(fmt.Sprintf("include = %s\n", formatStringSlice(op.Include)))
	}
	if len(op.With) > 0 {
		b.WriteString("with = { ")
		keys := make([]string, 0, len(op.With))
		for k := range op.With {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		first := true
		for _, k := range keys {
			if !first {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("%s = %q", k, op.With[k]))
			first = false
		}
		b.WriteString(" }\n")
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
	if len(op.Operators) > 0 {
		b.WriteString(fmt.Sprintf("operators = %s\n", formatStringSlice(op.Operators)))
	}
	if op.Deferrable {
		b.WriteString("deferrable = true\n")
	}
	if op.InitiallyDeferred {
		b.WriteString("initially_deferred = true\n")
	}

	// Down must be written before consolidated: [[ddl.consolidated]] changes
	// the TOML scope so subsequent keys would be under the last consolidated
	// entry, not the parent [[ddl]].
	if op.Down != nil {
		writeDownOp(b, op.Down)
	}

	if len(op.ConsolidatedOps) > 0 {
		for i := range op.ConsolidatedOps {
			b.WriteString("\n[[ddl.consolidated]]\n")
			writeDDLOp(b, &op.ConsolidatedOps[i])
		}
	}
}

func writeDMLOp(b *strings.Builder, op *DMLOp) {
	b.WriteString(fmt.Sprintf("op = %q\n", op.Op))
	if op.Phase != "" {
		b.WriteString(fmt.Sprintf("phase = %q\n", op.Phase))
	}
	b.WriteString(fmt.Sprintf("sql = %q\n", op.SQL))
	if op.BatchSize > 0 {
		b.WriteString(fmt.Sprintf("batch_size = %d\n", op.BatchSize))
	}
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
		if len(op.Columns) > 0 {
			parts = append(parts, fmt.Sprintf("columns = %s", formatStringSlice(op.Columns)))
		}
		if len(op.Operators) > 0 {
			parts = append(parts, fmt.Sprintf("operators = %s", formatStringSlice(op.Operators)))
		}
		if op.Deferrable {
			parts = append(parts, "deferrable = true")
		}
		if op.InitiallyDeferred {
			parts = append(parts, "initially_deferred = true")
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

func formatBoolSlice(s []bool) string {
	parts := make([]string, len(s))
	for i, v := range s {
		parts[i] = fmt.Sprintf("%v", v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
