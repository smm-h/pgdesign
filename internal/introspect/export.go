package introspect

import (
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
)

// Export serializes a model.Schema to pgdesign TOML format.
// It uses simple string building (not go-toml-edit) since we're generating
// fresh output, not editing existing TOML.
func Export(schema *model.Schema) ([]byte, error) {
	var b strings.Builder

	// [meta]
	b.WriteString("[meta]\n")
	b.WriteString("version = 1\n")
	if schema.Name != "" {
		b.WriteString(fmt.Sprintf("schema = %s\n", quoteTOML(schema.Name)))
	}
	if len(schema.Extensions) > 0 {
		b.WriteString(fmt.Sprintf("extensions = %s\n", tomlStringArray(schema.Extensions)))
	}

	// [types.*] for enums
	for _, e := range schema.Enums {
		b.WriteByte('\n')
		b.WriteString(fmt.Sprintf("[types.%s]\n", e.Name))
		b.WriteString("kind = \"enum\"\n")
		b.WriteString(fmt.Sprintf("values = %s\n", tomlStringArray(e.Values)))
		if e.Comment != "" {
			b.WriteString(fmt.Sprintf("comment = %s\n", quoteTOML(e.Comment)))
		}
	}

	// [tables.*]
	for _, t := range schema.Tables {
		b.WriteByte('\n')
		b.WriteString(fmt.Sprintf("[tables.%s]\n", t.Name))
		if t.Comment != "" {
			b.WriteString(fmt.Sprintf("comment = %s\n", quoteTOML(t.Comment)))
		}
		if len(t.PK) > 0 {
			b.WriteString(fmt.Sprintf("pk = %s\n", tomlStringArray(t.PK)))
		}

		// Columns
		for _, c := range t.Columns {
			b.WriteByte('\n')
			b.WriteString(fmt.Sprintf("[tables.%s.columns.%s]\n", t.Name, c.Name))
			b.WriteString(fmt.Sprintf("type = %s\n", quoteTOML(c.PGType)))
			if !c.NotNull {
				b.WriteString("nullable = true\n")
			}
			if c.Default != "" {
				b.WriteString(fmt.Sprintf("default = %s\n", quoteTOML(c.Default)))
			}
			if c.DefaultExpr != "" {
				b.WriteString(fmt.Sprintf("default_expr = %s\n", quoteTOML(c.DefaultExpr)))
			}
			if c.Identity != "" {
				b.WriteString(fmt.Sprintf("identity = %s\n", quoteTOML(c.Identity)))
			}
			if c.Generated != "" {
				b.WriteString(fmt.Sprintf("generated = %s\n", quoteTOML(c.Generated)))
				if c.Stored {
					b.WriteString("stored = true\n")
				}
			}
			if c.Comment != "" {
				b.WriteString(fmt.Sprintf("comment = %s\n", quoteTOML(c.Comment)))
			}
		}

		// Foreign keys
		for _, fk := range t.FKs {
			b.WriteByte('\n')
			b.WriteString(fmt.Sprintf("[tables.%s.fks.%s]\n", t.Name, fk.Name))
			b.WriteString(fmt.Sprintf("columns = %s\n", tomlStringArray(fk.Columns)))
			b.WriteString(fmt.Sprintf("ref_table = %s\n", quoteTOML(fk.RefTable)))
			b.WriteString(fmt.Sprintf("ref_columns = %s\n", tomlStringArray(fk.RefColumns)))
			if fk.OnDelete != "" {
				b.WriteString(fmt.Sprintf("on_delete = %s\n", quoteTOML(fk.OnDelete)))
			}
		}

		// Indexes
		for _, idx := range t.Indexes {
			b.WriteByte('\n')
			b.WriteString(fmt.Sprintf("[tables.%s.indexes.%s]\n", t.Name, idx.Name))
			b.WriteString(fmt.Sprintf("columns = %s\n", tomlStringArray(idx.Columns)))
			if idx.Method != "" && idx.Method != "btree" {
				b.WriteString(fmt.Sprintf("method = %s\n", quoteTOML(idx.Method)))
			}
			if idx.Opclass != "" {
				b.WriteString(fmt.Sprintf("opclass = %s\n", quoteTOML(idx.Opclass)))
			}
			if idx.Where != "" {
				b.WriteString(fmt.Sprintf("where = %s\n", quoteTOML(idx.Where)))
			}
			if len(idx.Include) > 0 {
				b.WriteString(fmt.Sprintf("include = %s\n", tomlStringArray(idx.Include)))
			}
		}

		// Unique constraints
		for _, uq := range t.Uniques {
			b.WriteByte('\n')
			b.WriteString(fmt.Sprintf("[tables.%s.unique.%s]\n", t.Name, uq.Name))
			b.WriteString(fmt.Sprintf("columns = %s\n", tomlStringArray(uq.Columns)))
		}

		// Check constraints
		for _, ck := range t.Checks {
			b.WriteByte('\n')
			b.WriteString(fmt.Sprintf("[tables.%s.checks.%s]\n", t.Name, ck.Name))
			b.WriteString(fmt.Sprintf("expr = %s\n", quoteTOML(ck.Expr)))
		}
	}

	return []byte(b.String()), nil
}

// quoteTOML returns a TOML-quoted string with proper escaping.
func quoteTOML(s string) string {
	// Use basic string. Escape backslashes, quotes, and control chars.
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlStringArray formats a []string as a TOML inline array.
func tomlStringArray(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = quoteTOML(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
