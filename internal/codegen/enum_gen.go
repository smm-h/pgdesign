package codegen

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"github.com/smm-h/pgdesign/internal/model"
)

// sanitizeEnumValue converts a PG enum value (arbitrary string) to a valid
// identifier in the target language. TS is returned unchanged since it uses
// string literal union types.
func sanitizeEnumValue(value string, lang Lang) string {
	if lang == LangTS {
		return value
	}

	// Split on non-alphanumeric characters into word parts.
	parts := splitEnumWords(value)
	if len(parts) == 0 {
		return "_"
	}

	var result string
	switch lang {
	case LangGo:
		// PascalCase: capitalize each word, join without separator.
		for _, p := range parts {
			result += strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	case LangPython, LangJava, LangKotlin:
		// UPPER_SNAKE_CASE: uppercase everything, join with underscores.
		upper := make([]string, len(parts))
		for i, p := range parts {
			upper[i] = strings.ToUpper(p)
		}
		result = strings.Join(upper, "_")
	case LangZig:
		// snake_case: lowercase everything, join with underscores.
		lower := make([]string, len(parts))
		for i, p := range parts {
			lower[i] = strings.ToLower(p)
		}
		result = strings.Join(lower, "_")
	}

	// Leading digits get an underscore prefix.
	if len(result) > 0 && unicode.IsDigit(rune(result[0])) {
		result = "_" + result
	}

	return result
}

// splitEnumWords splits a string on non-alphanumeric boundaries into word
// parts, filtering out empty segments.
func splitEnumWords(s string) []string {
	var parts []string
	var current []rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
		} else {
			if len(current) > 0 {
				parts = append(parts, string(current))
				current = current[:0]
			}
		}
	}
	if len(current) > 0 {
		parts = append(parts, string(current))
	}
	return parts
}

// GenerateEnums generates enum definitions for all enums in the given slice,
// separated by blank lines. Returns empty string if enums is empty.
func GenerateEnums(enums []model.Enum, lang Lang) string {
	if len(enums) == 0 {
		return ""
	}

	var parts []string
	for _, e := range enums {
		var s string
		switch lang {
		case LangGo:
			s = generateGoEnum(e)
		case LangTS:
			s = generateTSEnum(e)
		case LangPython:
			s = generatePythonEnum(e)
		case LangJava:
			s = generateJavaEnum(e)
		case LangKotlin:
			s = generateKotlinEnum(e)
		case LangZig:
			s = generateZigEnum(e)
		}
		parts = append(parts, s)
	}

	return strings.Join(parts, "\n")
}

// goEnumImports lists the stdlib import paths the branded Go enum block needs
// (json/driver/fmt for the validating boundary methods). Callers that emit the
// Go enum block must union these into the file's import set.
var goEnumImports = []string{"database/sql/driver", "encoding/json", "fmt"}

// generateGoEnum produces a BRANDED Go enum: an opaque struct with an
// unexported value field, package-level var members (a const of struct type is
// illegal), a validating ParseXxx constructor, and Stringer/Valuer/Marshaler
// plus validating Scanner/Unmarshalers routed through ParseXxx. The zero value
// (empty value field) is detectably invalid via IsValid/ParseXxx, so a value
// that never passed a boundary cannot masquerade as a defined member.
func generateGoEnum(e model.Enum) string {
	var buf bytes.Buffer
	typeName := toPascalCase(e.Name)

	if e.Comment != "" {
		fmt.Fprintf(&buf, "// %s %s\n", typeName, e.Comment)
	}
	// Opaque struct: the value is unexported so it cannot be constructed with an
	// arbitrary string outside this package.
	fmt.Fprintf(&buf, "type %s struct{ value string }\n\n", typeName)

	// Var members (const of struct type is illegal in Go).
	buf.WriteString("var (\n")
	for _, v := range e.Values {
		memberName := typeName + sanitizeEnumValue(v, LangGo)
		fmt.Fprintf(&buf, "\t%s = %s{%q}\n", memberName, typeName, v)
	}
	buf.WriteString(")\n\n")

	// Validating constructor.
	fmt.Fprintf(&buf, "// Parse%s returns the %s for s, or an error if s is not a defined value.\n", typeName, typeName)
	fmt.Fprintf(&buf, "func Parse%s(s string) (%s, error) {\n", typeName, typeName)
	buf.WriteString("\tswitch s {\n")
	for _, v := range e.Values {
		memberName := typeName + sanitizeEnumValue(v, LangGo)
		fmt.Fprintf(&buf, "\tcase %q:\n\t\treturn %s, nil\n", v, memberName)
	}
	fmt.Fprintf(&buf, "\tdefault:\n\t\treturn %s{}, fmt.Errorf(\"invalid %s: %%q\", s)\n\t}\n}\n\n", typeName, typeName)

	// Stringer.
	fmt.Fprintf(&buf, "// String returns the underlying enum value.\n")
	fmt.Fprintf(&buf, "func (e %s) String() string { return e.value }\n\n", typeName)

	// IsValid: the zero value (value == "") is not a defined member.
	fmt.Fprintf(&buf, "// IsValid reports whether e holds a defined enum value (the zero value does not).\n")
	fmt.Fprintf(&buf, "func (e %s) IsValid() bool {\n\t_, err := Parse%s(e.value)\n\treturn err == nil\n}\n\n", typeName, typeName)

	// Valuer.
	fmt.Fprintf(&buf, "// Value implements driver.Valuer.\n")
	fmt.Fprintf(&buf, "func (e %s) Value() (driver.Value, error) { return e.value, nil }\n\n", typeName)

	// MarshalJSON.
	fmt.Fprintf(&buf, "// MarshalJSON implements json.Marshaler.\n")
	fmt.Fprintf(&buf, "func (e %s) MarshalJSON() ([]byte, error) { return json.Marshal(e.value) }\n\n", typeName)

	// UnmarshalJSON via Parse (validating).
	fmt.Fprintf(&buf, "// UnmarshalJSON implements json.Unmarshaler, validating via Parse%s.\n", typeName)
	fmt.Fprintf(&buf, "func (e *%s) UnmarshalJSON(data []byte) error {\n", typeName)
	buf.WriteString("\tvar s string\n\tif err := json.Unmarshal(data, &s); err != nil {\n\t\treturn err\n\t}\n")
	fmt.Fprintf(&buf, "\tv, err := Parse%s(s)\n\tif err != nil {\n\t\treturn err\n\t}\n\t*e = v\n\treturn nil\n}\n\n", typeName)

	// UnmarshalText via Parse (validating).
	fmt.Fprintf(&buf, "// UnmarshalText implements encoding.TextUnmarshaler, validating via Parse%s.\n", typeName)
	fmt.Fprintf(&buf, "func (e *%s) UnmarshalText(text []byte) error {\n", typeName)
	fmt.Fprintf(&buf, "\tv, err := Parse%s(string(text))\n\tif err != nil {\n\t\treturn err\n\t}\n\t*e = v\n\treturn nil\n}\n\n", typeName)

	// sql.Scanner via Parse (validating).
	fmt.Fprintf(&buf, "// Scan implements sql.Scanner, validating via Parse%s.\n", typeName)
	fmt.Fprintf(&buf, "func (e *%s) Scan(src any) error {\n", typeName)
	fmt.Fprintf(&buf, "\tif src == nil {\n\t\treturn fmt.Errorf(\"cannot scan NULL into %s\")\n\t}\n", typeName)
	buf.WriteString("\tvar s string\n\tswitch v := src.(type) {\n\tcase string:\n\t\ts = v\n\tcase []byte:\n\t\ts = string(v)\n\tdefault:\n")
	fmt.Fprintf(&buf, "\t\treturn fmt.Errorf(\"cannot scan %%T into %s\", src)\n\t}\n", typeName)
	fmt.Fprintf(&buf, "\tv, err := Parse%s(s)\n\tif err != nil {\n\t\treturn err\n\t}\n\t*e = v\n\treturn nil\n}\n", typeName)

	return buf.String()
}

// generateTSEnum produces a TypeScript string literal union type for an enum
// plus a validating parse function for boundary ingress. The union is kept (it
// preserves compile-closure and exhaustiveness narrowing); parseXxx is the
// runtime boundary that rejects values the compiler cannot see (JSON, DB rows).
func generateTSEnum(e model.Enum) string {
	quoted := make([]string, len(e.Values))
	for i, v := range e.Values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	typeName := toPascalCase(e.Name)
	valuesConst := typeName + "Values"

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "export type %s = %s;\n\n", typeName, strings.Join(quoted, " | "))
	fmt.Fprintf(&buf, "export const %s: readonly %s[] = [%s] as const;\n\n", valuesConst, typeName, strings.Join(quoted, ", "))
	fmt.Fprintf(&buf, "/** Parses s into a %s, throwing if s is not a defined value. */\n", typeName)
	fmt.Fprintf(&buf, "export function parse%s(s: string): %s {\n", typeName, typeName)
	fmt.Fprintf(&buf, "  if ((%s as readonly string[]).includes(s)) {\n", valuesConst)
	fmt.Fprintf(&buf, "    return s as %s;\n", typeName)
	buf.WriteString("  }\n")
	fmt.Fprintf(&buf, "  throw new Error(`invalid %s: ${s}`);\n", typeName)
	buf.WriteString("}\n")
	return buf.String()
}

// generatePythonEnum produces a Python StrEnum class for an enum plus an
// ergonomic typed parse() classmethod. StrEnum.__call__ already validates
// (Role("bad") raises ValueError), so parse() is a typed alias, not
// construction-closing machinery; it names the boundary for readable ingress.
// The caller adds "from enum import StrEnum" to the file.
func generatePythonEnum(e model.Enum) string {
	var buf bytes.Buffer
	className := toPascalCase(e.Name)

	fmt.Fprintf(&buf, "class %s(StrEnum):\n", className)
	if e.Comment != "" {
		fmt.Fprintf(&buf, "    \"\"\"%s\"\"\"\n", e.Comment)
	}
	for _, v := range e.Values {
		name := sanitizeEnumValue(v, LangPython)
		fmt.Fprintf(&buf, "    %s = %q\n", name, v)
	}
	buf.WriteString("\n    @classmethod\n")
	fmt.Fprintf(&buf, "    def parse(cls, value: str) -> \"%s\":\n", className)
	buf.WriteString("        \"\"\"Return the member for value, raising ValueError if undefined.\"\"\"\n")
	buf.WriteString("        return cls(value)\n")

	return buf.String()
}

// generateJavaEnum produces a Java enum with a String value field.
func generateJavaEnum(e model.Enum) string {
	var buf bytes.Buffer
	typeName := toPascalCase(e.Name)

	fmt.Fprintf(&buf, "public enum %s {\n", typeName)
	for i, v := range e.Values {
		name := sanitizeEnumValue(v, LangJava)
		if i < len(e.Values)-1 {
			fmt.Fprintf(&buf, "    %s(%q),\n", name, v)
		} else {
			fmt.Fprintf(&buf, "    %s(%q);\n", name, v)
		}
	}
	buf.WriteString("\n    private final String value;\n\n")
	fmt.Fprintf(&buf, "    %s(String value) {\n", typeName)
	buf.WriteString("        this.value = value;\n")
	buf.WriteString("    }\n\n")
	buf.WriteString("    public String getValue() {\n")
	buf.WriteString("        return this.value;\n")
	buf.WriteString("    }\n\n")
	// fromValue is the net-new value-based parse: it maps a raw DB/JSON value
	// back to the member, rejecting undefined values (the JPA converter uses it).
	fmt.Fprintf(&buf, "    public static %s fromValue(String value) {\n", typeName)
	fmt.Fprintf(&buf, "        for (%s e : values()) {\n", typeName)
	buf.WriteString("            if (e.value.equals(value)) {\n")
	buf.WriteString("                return e;\n")
	buf.WriteString("            }\n")
	buf.WriteString("        }\n")
	fmt.Fprintf(&buf, "        throw new IllegalArgumentException(\"invalid %s: \" + value);\n", typeName)
	buf.WriteString("    }\n")
	buf.WriteString("}\n")

	return buf.String()
}

// generateKotlinEnum produces a Kotlin enum class with a String value property.
func generateKotlinEnum(e model.Enum) string {
	var buf bytes.Buffer
	typeName := toPascalCase(e.Name)

	fmt.Fprintf(&buf, "enum class %s(val value: String) {\n", typeName)
	for i, v := range e.Values {
		name := sanitizeEnumValue(v, LangKotlin)
		if i < len(e.Values)-1 {
			fmt.Fprintf(&buf, "    %s(%q),\n", name, v)
		} else {
			fmt.Fprintf(&buf, "    %s(%q);\n", name, v)
		}
	}
	// fromValue is the net-new value-based parse, symmetric with Java's.
	buf.WriteString("\n    companion object {\n")
	fmt.Fprintf(&buf, "        fun fromValue(value: String): %s =\n", typeName)
	buf.WriteString("            values().firstOrNull { it.value == value }\n")
	fmt.Fprintf(&buf, "                ?: throw IllegalArgumentException(\"invalid %s: $value\")\n", typeName)
	buf.WriteString("    }\n")
	buf.WriteString("}\n")

	return buf.String()
}

// generateZigEnum produces a BRANDED Zig wrapper struct: a struct with a
// []const u8 value, named member constants, and a validating parse() that
// rejects undefined values. The wrapper is the validating boundary (Zig lacks
// string-backed enums). Callers must import std for parse's std.mem.eql.
func generateZigEnum(e model.Enum) string {
	var buf bytes.Buffer
	typeName := toPascalCase(e.Name)

	if e.Comment != "" {
		fmt.Fprintf(&buf, "/// %s\n", e.Comment)
	}
	fmt.Fprintf(&buf, "pub const %s = struct {\n", typeName)
	buf.WriteString("    value: []const u8,\n\n")
	for _, v := range e.Values {
		name := sanitizeEnumValue(v, LangZig)
		fmt.Fprintf(&buf, "    pub const %s = %s{ .value = %q };\n", name, typeName, v)
	}
	fmt.Fprintf(&buf, "\n    pub fn parse(s: []const u8) !%s {\n", typeName)
	for _, v := range e.Values {
		name := sanitizeEnumValue(v, LangZig)
		fmt.Fprintf(&buf, "        if (std.mem.eql(u8, s, %q)) return %s;\n", v, name)
	}
	fmt.Fprintf(&buf, "        return error.Invalid%s;\n", typeName)
	buf.WriteString("    }\n")
	buf.WriteString("};\n")

	return buf.String()
}
