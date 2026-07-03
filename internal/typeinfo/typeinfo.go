// Package typeinfo provides structured PostgreSQL type representation.
// It parses raw type strings (from TOML schemas, format_type() output, etc.)
// into a normalized struct with separated base type and parameters, and can
// reconstruct the SQL type string from the struct.
package typeinfo

import (
	"strconv"
	"strings"
)

// Params holds parsed type parameters. For known types, named fields are
// populated; for unknown/extension types, only RawModifier is set.
type Params struct {
	Precision   *int   `json:"precision,omitempty"`
	Scale       *int   `json:"scale,omitempty"`
	Length      *int   `json:"length,omitempty"`
	RawModifier string `json:"raw_modifier,omitempty"` // raw parenthesized portion for extension types and DDL reconstruction
}

// Type represents a parsed PostgreSQL type with its canonical base name
// and optional parameters.
type Type struct {
	Base       string `json:"base"`                  // normalized short-form PG type: "varchar", "timestamptz", "int4", "bool"
	DomainName string `json:"domain_name,omitempty"` // domain name when column is domain-backed (populated by build.go, NOT by Parse)
	Params     Params `json:"params,omitempty"`
}

// aliases maps long-form and alternate type names to canonical short forms.
// All keys are lowercase.
var aliases = map[string]string{
	"character varying":           "varchar",
	"character":                   "char",
	"char":                        "char",
	"double precision":            "float8",
	"boolean":                     "bool",
	"integer":                     "int4",
	"smallint":                    "int2",
	"bigint":                      "int8",
	"real":                        "float4",
	"timestamp with time zone":    "timestamptz",
	"timestamp without time zone": "timestamp",
	"time with time zone":         "timetz",
	"time without time zone":      "time",
	"bit varying":                 "varbit",
	"int":                         "int4",
	"float":                       "float8",
	"decimal":                     "numeric",
	"serial":                      "serial",
	"bigserial":                   "bigserial",
	"smallserial":                 "smallserial",
}

// multiWordTypes lists canonical multi-word type suffixes (lowercase) that
// must be reassembled after extracting interior parameters.
// For example, "timestamp(3) with time zone" has base word "timestamp",
// interior params "(3)", and suffix "with time zone".
var multiWordSuffixes = map[string]string{
	"with time zone":    "tz", // timestamp/time + "with time zone" → timestamptz/timetz
	"without time zone": "",   // timestamp/time + "without time zone" → timestamp/time (no change)
}

// multiWordPrefixes lists types whose first word can start a multi-word type
// that includes interior parameters.
var multiWordPrefixes = map[string]bool{
	"timestamp": true,
	"time":      true,
}

// precisionTypes are base types where a single numeric param means precision.
var precisionTypes = map[string]bool{
	"timestamp":   true,
	"timestamptz": true,
	"time":        true,
	"timetz":      true,
	"interval":    true,
	"numeric":     true,
}

// lengthTypes are base types where a single numeric param means length.
var lengthTypes = map[string]bool{
	"varchar": true,
	"char":    true,
	"varbit":  true,
	"bit":     true,
}

// Parse parses a raw PostgreSQL type string into a structured Type.
// It normalizes aliases, extracts parameters, and handles multi-word types
// with interior parameters (e.g., "timestamp(3) with time zone").
// DomainName is NEVER set by Parse.
func Parse(raw string) Type {
	s := strings.TrimSpace(raw)
	s = strings.ToLower(s)
	if s == "" {
		return Type{}
	}

	// Extract array suffix if present.
	isArray := false
	if strings.HasSuffix(s, "[]") {
		isArray = true
		s = strings.TrimSuffix(s, "[]")
		s = strings.TrimSpace(s)
	}

	// Try to extract parenthesized portion.
	base, paramStr, trailing := extractParams(s)

	// Check if trailing portion forms a multi-word type.
	trailing = strings.TrimSpace(trailing)
	resolvedBase := resolveBase(base, trailing)

	// Parse parameters based on the resolved base type.
	params := parseParams(resolvedBase, paramStr)

	result := Type{
		Base:   resolvedBase,
		Params: params,
	}

	// Re-append array suffix to base if present.
	if isArray {
		result.Base = result.Base + "[]"
	}

	return result
}

// extractParams splits a type string into the base word, the parenthesized
// parameter string (without parens), and the trailing portion after the params.
// If no params are present, paramStr is empty and trailing contains everything
// after the base word.
func extractParams(s string) (base, paramStr, trailing string) {
	openIdx := strings.IndexByte(s, '(')
	if openIdx < 0 {
		// No parentheses. The entire string might be a multi-word type alias.
		return s, "", ""
	}

	closeIdx := strings.IndexByte(s[openIdx:], ')')
	if closeIdx < 0 {
		// Unclosed paren: treat entire string as base.
		return s, "", ""
	}
	closeIdx += openIdx

	base = strings.TrimSpace(s[:openIdx])
	paramStr = strings.TrimSpace(s[openIdx+1 : closeIdx])
	trailing = ""
	if closeIdx+1 < len(s) {
		trailing = s[closeIdx+1:]
	}
	return base, paramStr, trailing
}

// resolveBase resolves the base type name by checking multi-word suffixes
// and the alias map.
func resolveBase(base, trailing string) string {
	// First check if the full string (base + trailing) is a known alias
	// without any multi-word prefix logic.
	if trailing != "" {
		full := base + " " + trailing
		if canonical, ok := aliases[full]; ok {
			return canonical
		}
	}

	// Check for multi-word types with interior params:
	// e.g., base="timestamp", trailing="with time zone"
	if multiWordPrefixes[base] && trailing != "" {
		trimmed := strings.TrimSpace(trailing)
		if suffix, ok := multiWordSuffixes[trimmed]; ok {
			resolved := base + suffix
			if canonical, ok := aliases[resolved]; ok {
				return canonical
			}
			return resolved
		}
	}

	// Single-word alias lookup.
	if canonical, ok := aliases[base]; ok {
		return canonical
	}

	return base
}

// parseParams parses the parameter string according to the base type's semantics.
func parseParams(base, paramStr string) Params {
	if paramStr == "" {
		return Params{}
	}

	// Strip array suffix for parameter semantics lookup.
	lookupBase := strings.TrimSuffix(base, "[]")

	// For numeric/decimal with two params: precision, scale.
	if lookupBase == "numeric" || lookupBase == "decimal" {
		parts := strings.SplitN(paramStr, ",", 2)
		if len(parts) == 2 {
			p := parseInt(strings.TrimSpace(parts[0]))
			s := parseInt(strings.TrimSpace(parts[1]))
			return Params{Precision: p, Scale: s}
		}
		// Single param for numeric = precision only.
		if p := parseInt(strings.TrimSpace(paramStr)); p != nil {
			return Params{Precision: p}
		}
	}

	// Single numeric param.
	val := parseInt(strings.TrimSpace(paramStr))

	if precisionTypes[lookupBase] && val != nil {
		return Params{Precision: val}
	}

	if lengthTypes[lookupBase] && val != nil {
		return Params{Length: val}
	}

	// Unknown type or non-numeric params: store as raw modifier.
	return Params{RawModifier: paramStr}
}

// parseInt attempts to parse a string as a non-negative integer.
// Returns nil if parsing fails.
func parseInt(s string) *int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

// Reconstruct rebuilds a SQL type string from a Type struct.
// If DomainName is set, it is returned directly (domain-backed columns use
// the domain name in DDL). Otherwise, the type is reconstructed from Base
// and Params.
func Reconstruct(t Type) string {
	if t.DomainName != "" {
		return t.DomainName
	}

	if t.Base == "" {
		return ""
	}

	base := t.Base
	arraySuffix := ""
	if strings.HasSuffix(base, "[]") {
		base = strings.TrimSuffix(base, "[]")
		arraySuffix = "[]"
	}

	params := reconstructParams(base, t.Params)
	if params != "" {
		return base + "(" + params + ")" + arraySuffix
	}

	return base + arraySuffix
}

// reconstructParams builds the parenthesized parameter string from Params.
func reconstructParams(base string, p Params) string {
	// RawModifier takes lowest priority -- only used when no named fields are set.
	lookupBase := strings.TrimSuffix(base, "[]")

	// numeric/decimal with precision and optional scale.
	if (lookupBase == "numeric" || lookupBase == "decimal") && p.Precision != nil {
		if p.Scale != nil {
			return strconv.Itoa(*p.Precision) + "," + strconv.Itoa(*p.Scale)
		}
		return strconv.Itoa(*p.Precision)
	}

	// Precision types.
	if precisionTypes[lookupBase] && p.Precision != nil {
		return strconv.Itoa(*p.Precision)
	}

	// Length types.
	if lengthTypes[lookupBase] && p.Length != nil {
		return strconv.Itoa(*p.Length)
	}

	// Fallback to raw modifier.
	if p.RawModifier != "" {
		return p.RawModifier
	}

	return ""
}

// Equal returns true if two Types have the same Base, DomainName, and Params
// (deep comparison of pointer fields).
func (t Type) Equal(other Type) bool {
	if t.Base != other.Base || t.DomainName != other.DomainName || t.Params.RawModifier != other.Params.RawModifier {
		return false
	}
	if !intPtrEqual(t.Params.Precision, other.Params.Precision) {
		return false
	}
	if !intPtrEqual(t.Params.Scale, other.Params.Scale) {
		return false
	}
	if !intPtrEqual(t.Params.Length, other.Params.Length) {
		return false
	}
	return true
}

// intPtrEqual returns true if two *int values are deeply equal.
func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// T is a concise constructor for test literals. It returns Type{Base: base}
// with all params at zero values.
func T(base string) Type {
	return Type{Base: base}
}

// MustParse parses a raw type string and panics if the input is empty.
// Intended for test setup only.
func MustParse(raw string) Type {
	if strings.TrimSpace(raw) == "" {
		panic("typeinfo.MustParse: empty input")
	}
	return Parse(raw)
}

// intPtr is a helper for constructing pointer-to-int values.
func intPtr(n int) *int {
	return &n
}
