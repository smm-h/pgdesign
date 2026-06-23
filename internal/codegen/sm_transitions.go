package codegen

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
)

// GenerateTransitionMaps generates transition map declarations for all state
// machine types in the schema, formatted for the given language.
func GenerateTransitionMaps(smts []model.SMTransitionMap, lang Lang) string {
	if len(smts) == 0 {
		return ""
	}

	var parts []string
	for _, smt := range smts {
		var s string
		switch lang {
		case LangGo:
			s = generateGoTransitionMap(smt)
		case LangTS:
			s = generateTSTransitionMap(smt)
		case LangPython:
			s = generatePythonTransitionMap(smt)
		case LangJava:
			s = generateJavaTransitionMap(smt)
		case LangKotlin:
			s = generateKotlinTransitionMap(smt)
		case LangZig:
			s = generateZigTransitionMap(smt)
		}
		if s != "" {
			parts = append(parts, s)
		}
	}

	return strings.Join(parts, "\n")
}

// sortedFromStates returns the from-states of a transition map in deterministic order.
func sortedFromStates(smt model.SMTransitionMap) []string {
	froms := make([]string, 0, len(smt.Transitions))
	for from := range smt.Transitions {
		froms = append(froms, from)
	}
	sort.Strings(froms)
	return froms
}

func generateGoTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer
	typeName := toPascalCase(smt.TypeName)

	fmt.Fprintf(&buf, "// %sTransitions maps each %s state to its allowed target states.\n", typeName, typeName)
	fmt.Fprintf(&buf, "var %sTransitions = map[%s][]%s{\n", typeName, typeName, typeName)

	for _, from := range sortedFromStates(smt) {
		tos := smt.Transitions[from]
		constFrom := typeName + sanitizeEnumValue(from, LangGo)
		toConsts := make([]string, len(tos))
		for i, to := range tos {
			toConsts[i] = typeName + sanitizeEnumValue(to, LangGo)
		}
		fmt.Fprintf(&buf, "\t%s: {%s},\n", constFrom, strings.Join(toConsts, ", "))
	}
	buf.WriteString("}\n")

	return buf.String()
}

func generateTSTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer
	typeName := toPascalCase(smt.TypeName)

	fmt.Fprintf(&buf, "/** Maps each %s state to its allowed target states. */\n", typeName)
	fmt.Fprintf(&buf, "export const %sTransitions: Record<%s, %s[]> = {\n", toCamelCase(smt.TypeName), typeName, typeName)

	for _, from := range sortedFromStates(smt) {
		tos := smt.Transitions[from]
		quotedTos := make([]string, len(tos))
		for i, to := range tos {
			quotedTos[i] = fmt.Sprintf("%q", to)
		}
		fmt.Fprintf(&buf, "  %q: [%s],\n", from, strings.Join(quotedTos, ", "))
	}
	buf.WriteString("};\n")

	return buf.String()
}

func generatePythonTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer
	className := toPascalCase(smt.TypeName)
	varName := strings.ToUpper(smt.TypeName) + "_TRANSITIONS"

	fmt.Fprintf(&buf, "# Maps each %s state to its allowed target states.\n", className)
	fmt.Fprintf(&buf, "%s: dict[%s, list[%s]] = {\n", varName, className, className)

	for _, from := range sortedFromStates(smt) {
		tos := smt.Transitions[from]
		constFrom := className + "." + sanitizeEnumValue(from, LangPython)
		toConsts := make([]string, len(tos))
		for i, to := range tos {
			toConsts[i] = className + "." + sanitizeEnumValue(to, LangPython)
		}
		fmt.Fprintf(&buf, "    %s: [%s],\n", constFrom, strings.Join(toConsts, ", "))
	}
	buf.WriteString("}\n")

	return buf.String()
}

func generateJavaTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer
	typeName := toPascalCase(smt.TypeName)
	froms := sortedFromStates(smt)

	fmt.Fprintf(&buf, "/** Maps each %s state to its allowed target states. */\n", typeName)
	fmt.Fprintf(&buf, "public static final Map<%s, Set<%s>> %s_TRANSITIONS = Map.ofEntries(\n",
		typeName, typeName, strings.ToUpper(smt.TypeName))

	for i, from := range froms {
		tos := smt.Transitions[from]
		constFrom := typeName + "." + sanitizeEnumValue(from, LangJava)
		toConsts := make([]string, len(tos))
		for j, to := range tos {
			toConsts[j] = typeName + "." + sanitizeEnumValue(to, LangJava)
		}
		sep := ","
		if i == len(froms)-1 {
			sep = ""
		}
		fmt.Fprintf(&buf, "    Map.entry(%s, Set.of(%s))%s\n", constFrom, strings.Join(toConsts, ", "), sep)
	}
	buf.WriteString(");\n")

	return buf.String()
}

func generateKotlinTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer
	typeName := toPascalCase(smt.TypeName)
	varName := strings.ToUpper(smt.TypeName) + "_TRANSITIONS"

	fmt.Fprintf(&buf, "/** Maps each %s state to its allowed target states. */\n", typeName)
	fmt.Fprintf(&buf, "val %s: Map<%s, Set<%s>> = mapOf(\n", varName, typeName, typeName)

	froms := sortedFromStates(smt)
	for i, from := range froms {
		tos := smt.Transitions[from]
		constFrom := typeName + "." + sanitizeEnumValue(from, LangKotlin)
		toConsts := make([]string, len(tos))
		for j, to := range tos {
			toConsts[j] = typeName + "." + sanitizeEnumValue(to, LangKotlin)
		}
		sep := ","
		if i == len(froms)-1 {
			sep = ""
		}
		fmt.Fprintf(&buf, "    %s to setOf(%s)%s\n", constFrom, strings.Join(toConsts, ", "), sep)
	}
	buf.WriteString(")\n")

	return buf.String()
}

// GenerateConstantsTransitionMaps generates transition map declarations using
// string constants (not type-safe enum references) for the constants output mode.
func GenerateConstantsTransitionMaps(smts []model.SMTransitionMap, lang ConstantsLang) string {
	if len(smts) == 0 {
		return ""
	}

	var parts []string
	for _, smt := range smts {
		var s string
		switch lang.Lang {
		case LangGo:
			s = generateGoConstTransitionMap(smt)
		case LangTS:
			s = generateTSConstTransitionMap(smt)
		case LangPython:
			s = generatePythonConstTransitionMap(smt)
		case LangJava:
			s = generateJavaConstTransitionMap(smt)
		case LangKotlin:
			s = generateKotlinConstTransitionMap(smt)
		case LangZig:
			s = generateZigConstTransitionMap(smt)
		}
		if s != "" {
			parts = append(parts, s)
		}
	}

	return strings.Join(parts, "\n")
}

func generateGoConstTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer
	name := toPascalCase(smt.TypeName)

	fmt.Fprintf(&buf, "// %sTransitions maps each %s state to its allowed target states.\n", name, smt.TypeName)
	fmt.Fprintf(&buf, "var %sTransitions = map[string][]string{\n", name)

	for _, from := range sortedFromStates(smt) {
		tos := smt.Transitions[from]
		quotedTos := make([]string, len(tos))
		for i, to := range tos {
			quotedTos[i] = fmt.Sprintf("%q", to)
		}
		fmt.Fprintf(&buf, "\t%q: {%s},\n", from, strings.Join(quotedTos, ", "))
	}
	buf.WriteString("}\n")

	return buf.String()
}

func generateTSConstTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "// Transitions for %s: maps each state to its allowed target states.\n", smt.TypeName)
	fmt.Fprintf(&buf, "export const TRANSITIONS_%s: Record<string, string[]> = {\n", strings.ToUpper(smt.TypeName))

	for _, from := range sortedFromStates(smt) {
		tos := smt.Transitions[from]
		quotedTos := make([]string, len(tos))
		for i, to := range tos {
			quotedTos[i] = fmt.Sprintf("%q", to)
		}
		fmt.Fprintf(&buf, "  %q: [%s],\n", from, strings.Join(quotedTos, ", "))
	}
	buf.WriteString("}\n")

	return buf.String()
}

func generatePythonConstTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "# Transitions for %s: maps each state to its allowed target states.\n", smt.TypeName)
	fmt.Fprintf(&buf, "TRANSITIONS_%s: dict[str, list[str]] = {\n", strings.ToUpper(smt.TypeName))

	for _, from := range sortedFromStates(smt) {
		tos := smt.Transitions[from]
		quotedTos := make([]string, len(tos))
		for i, to := range tos {
			quotedTos[i] = fmt.Sprintf("%q", to)
		}
		fmt.Fprintf(&buf, "    %q: [%s],\n", from, strings.Join(quotedTos, ", "))
	}
	buf.WriteString("}\n")

	return buf.String()
}

func generateJavaConstTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer
	froms := sortedFromStates(smt)

	fmt.Fprintf(&buf, "// Transitions for %s: maps each state to its allowed target states.\n", smt.TypeName)
	fmt.Fprintf(&buf, "public static final Map<String, Set<String>> TRANSITIONS_%s = Map.ofEntries(\n", strings.ToUpper(smt.TypeName))

	for i, from := range froms {
		tos := smt.Transitions[from]
		quotedTos := make([]string, len(tos))
		for j, to := range tos {
			quotedTos[j] = fmt.Sprintf("%q", to)
		}
		sep := ","
		if i == len(froms)-1 {
			sep = ""
		}
		fmt.Fprintf(&buf, "    Map.entry(%q, Set.of(%s))%s\n", from, strings.Join(quotedTos, ", "), sep)
	}
	buf.WriteString(");\n")

	return buf.String()
}

func generateKotlinConstTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer
	froms := sortedFromStates(smt)

	fmt.Fprintf(&buf, "// Transitions for %s: maps each state to its allowed target states.\n", smt.TypeName)
	fmt.Fprintf(&buf, "val TRANSITIONS_%s: Map<String, Set<String>> = mapOf(\n", strings.ToUpper(smt.TypeName))

	for i, from := range froms {
		tos := smt.Transitions[from]
		quotedTos := make([]string, len(tos))
		for j, to := range tos {
			quotedTos[j] = fmt.Sprintf("%q", to)
		}
		sep := ","
		if i == len(froms)-1 {
			sep = ""
		}
		fmt.Fprintf(&buf, "    %q to setOf(%s)%s\n", from, strings.Join(quotedTos, ", "), sep)
	}
	buf.WriteString(")\n")

	return buf.String()
}

func generateZigConstTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer
	prefix := strings.ToLower(strings.ReplaceAll(smt.TypeName, "-", "_"))

	fmt.Fprintf(&buf, "// Transitions for %s: maps each state to its allowed target states.\n", smt.TypeName)
	fmt.Fprintf(&buf, "pub const transitions_%s = struct {\n", prefix)

	for _, from := range sortedFromStates(smt) {
		tos := smt.Transitions[from]
		fromName := sanitizeEnumValue(from, LangZig)
		quotedTos := make([]string, len(tos))
		for i, to := range tos {
			quotedTos[i] = fmt.Sprintf("%q", to)
		}
		fmt.Fprintf(&buf, "    pub const %s = [_][]const u8{ %s };\n", fromName, strings.Join(quotedTos, ", "))
	}
	buf.WriteString("};\n")

	return buf.String()
}

func generateZigTransitionMap(smt model.SMTransitionMap) string {
	var buf bytes.Buffer
	prefix := strings.ToLower(strings.ReplaceAll(smt.TypeName, "-", "_"))

	fmt.Fprintf(&buf, "/// Maps each %s state to its allowed target states.\n", smt.TypeName)
	fmt.Fprintf(&buf, "pub const %s_transitions = struct {\n", prefix)

	for _, from := range sortedFromStates(smt) {
		tos := smt.Transitions[from]
		fromName := sanitizeEnumValue(from, LangZig)
		quotedTos := make([]string, len(tos))
		for i, to := range tos {
			quotedTos[i] = fmt.Sprintf("%q", to)
		}
		fmt.Fprintf(&buf, "    pub const %s = [_][]const u8{ %s };\n", fromName, strings.Join(quotedTos, ", "))
	}
	buf.WriteString("};\n")

	return buf.String()
}
