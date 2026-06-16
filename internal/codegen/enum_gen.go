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

// generateGoEnum produces a Go type + const block for an enum.
func generateGoEnum(e model.Enum) string {
	var buf bytes.Buffer
	typeName := toPascalCase(e.Name)

	if e.Comment != "" {
		fmt.Fprintf(&buf, "// %s %s\n", typeName, e.Comment)
	}
	fmt.Fprintf(&buf, "type %s string\n\nconst (\n", typeName)
	for _, v := range e.Values {
		constName := typeName + sanitizeEnumValue(v, LangGo)
		fmt.Fprintf(&buf, "\t%s %s = %q\n", constName, typeName, v)
	}
	buf.WriteString(")\n")

	return buf.String()
}

// generateTSEnum produces a TypeScript string literal union type for an enum.
func generateTSEnum(e model.Enum) string {
	quoted := make([]string, len(e.Values))
	for i, v := range e.Values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	typeName := toPascalCase(e.Name)
	return fmt.Sprintf("export type %s = %s;\n", typeName, strings.Join(quoted, " | "))
}

// generatePythonEnum produces a Python str Enum class for an enum.
// The caller is responsible for adding "from enum import Enum" to the file.
func generatePythonEnum(e model.Enum) string {
	var buf bytes.Buffer
	className := toPascalCase(e.Name)

	fmt.Fprintf(&buf, "class %s(str, Enum):\n", className)
	if e.Comment != "" {
		fmt.Fprintf(&buf, "    \"\"\"%s\"\"\"\n", e.Comment)
	}
	for _, v := range e.Values {
		name := sanitizeEnumValue(v, LangPython)
		fmt.Fprintf(&buf, "    %s = %q\n", name, v)
	}

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
	buf.WriteString("}\n")

	return buf.String()
}

// generateZigEnum produces Zig pub const declarations for enum values.
// Zig lacks string-backed enums, so each value becomes a string constant.
func generateZigEnum(e model.Enum) string {
	var buf bytes.Buffer
	prefix := strings.ToLower(strings.ReplaceAll(e.Name, "-", "_"))

	if e.Comment != "" {
		fmt.Fprintf(&buf, "/// %s\n", e.Comment)
	}
	for _, v := range e.Values {
		name := sanitizeEnumValue(v, LangZig)
		fmt.Fprintf(&buf, "pub const %s_%s = %q;\n", prefix, name, v)
	}

	return buf.String()
}
