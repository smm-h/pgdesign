package codegen

import "strings"

// Lang identifies a target language for code generation.
type Lang string

const (
	LangGo     Lang = "go"
	LangTS     Lang = "ts"
	LangPython Lang = "python"
	LangJava   Lang = "java"
	LangKotlin Lang = "kotlin"
	LangZig    Lang = "zig"
)

// TypeMapping holds the native type name and optional import for a single
// PG-to-language mapping.
type TypeMapping struct {
	Type   string // native type name (e.g., "int32", "number", "int")
	Import string // import path, empty if none needed (e.g., "time", "java.util.UUID")
}

// typeMap is the shared lookup table: lowercase PG type -> language -> mapping.
var typeMap = buildTypeMap()

// fallbackType is the default mapping for unrecognized PG types (e.g., enums).
var fallbackType = map[Lang]TypeMapping{
	LangGo:     {Type: "string"},
	LangTS:     {Type: "string"},
	LangPython: {Type: "str"},
	LangJava:   {Type: "String"},
	LangKotlin: {Type: "String"},
	LangZig:    {Type: "[]const u8"},
}

// moneyType maps each language to the type used for the "money" semantic type.
// Money is stored as integer cents to avoid floating-point rounding.
var moneyType = map[Lang]string{
	LangGo:     "int64",
	LangTS:     "number",
	LangPython: "int",
	LangJava:   "long",
	LangKotlin: "Long",
	LangZig:    "i64",
}

// LookupType returns the native type mapping for a PG type in the given
// language. Falls back to a string-like type if the PG type is unrecognized.
func LookupType(pgType string, lang Lang) TypeMapping {
	if langs, ok := typeMap[strings.ToLower(pgType)]; ok {
		if m, ok := langs[lang]; ok {
			return m
		}
	}
	return fallbackType[lang]
}

// LookupMoneyType returns the native type for the "money" semantic type.
func LookupMoneyType(lang Lang) string {
	return moneyType[lang]
}

// ApplyNullable wraps a base type with the language's nullable pattern.
//
// Java is a no-op: Java's nullable handling requires boxing primitives to
// wrapper types (int -> Integer), which is context-dependent. Callers should
// use toJavaWrapper from java_types.go instead.
func ApplyNullable(baseType string, lang Lang) string {
	switch lang {
	case LangGo:
		return "*" + baseType
	case LangTS:
		return baseType + " | null"
	case LangPython:
		return "Optional[" + baseType + "]"
	case LangJava:
		// No-op: Java nullable handling needs primitive-to-wrapper boxing,
		// which the caller must handle separately.
		return baseType
	case LangKotlin:
		return baseType + "?"
	case LangZig:
		return "?" + baseType
	default:
		return baseType
	}
}

// ApplyArray wraps a base type with the language's array/list pattern.
//
// For Java, the caller must separately add a "java.util.List" import.
func ApplyArray(baseType string, lang Lang) string {
	switch lang {
	case LangGo:
		return "[]" + baseType
	case LangTS:
		return baseType + "[]"
	case LangPython:
		return "list[" + baseType + "]"
	case LangJava:
		return "List<" + baseType + ">"
	case LangKotlin:
		return "List<" + baseType + ">"
	case LangZig:
		return "[]" + baseType
	default:
		return baseType
	}
}

// entry is a helper for building typeMap: one PG type group mapped to all 6
// languages.
type entry struct {
	pgTypes []string
	go_     TypeMapping
	ts      TypeMapping
	python  TypeMapping
	java    TypeMapping
	kotlin  TypeMapping
	zig     TypeMapping
}

func buildTypeMap() map[string]map[Lang]TypeMapping {
	entries := []entry{
		{
			pgTypes: []string{"integer", "int4"},
			go_:     TypeMapping{Type: "int32"},
			ts:      TypeMapping{Type: "number"},
			python:  TypeMapping{Type: "int"},
			java:    TypeMapping{Type: "int"},
			kotlin:  TypeMapping{Type: "Int"},
			zig:     TypeMapping{Type: "i32"},
		},
		{
			pgTypes: []string{"bigint", "int8"},
			go_:     TypeMapping{Type: "int64"},
			ts:      TypeMapping{Type: "number"},
			python:  TypeMapping{Type: "int"},
			java:    TypeMapping{Type: "long"},
			kotlin:  TypeMapping{Type: "Long"},
			zig:     TypeMapping{Type: "i64"},
		},
		{
			pgTypes: []string{"smallint", "int2"},
			go_:     TypeMapping{Type: "int16"},
			ts:      TypeMapping{Type: "number"},
			python:  TypeMapping{Type: "int"},
			java:    TypeMapping{Type: "short"},
			kotlin:  TypeMapping{Type: "Short"},
			zig:     TypeMapping{Type: "i16"},
		},
		{
			pgTypes: []string{"text", "varchar", "character varying", "char", "character", "bpchar"},
			go_:     TypeMapping{Type: "string"},
			ts:      TypeMapping{Type: "string"},
			python:  TypeMapping{Type: "str"},
			java:    TypeMapping{Type: "String"},
			kotlin:  TypeMapping{Type: "String"},
			zig:     TypeMapping{Type: "[]const u8"},
		},
		{
			pgTypes: []string{"boolean", "bool"},
			go_:     TypeMapping{Type: "bool"},
			ts:      TypeMapping{Type: "boolean"},
			python:  TypeMapping{Type: "bool"},
			java:    TypeMapping{Type: "boolean"},
			kotlin:  TypeMapping{Type: "Boolean"},
			zig:     TypeMapping{Type: "bool"},
		},
		{
			pgTypes: []string{"timestamptz", "timestamp", "timestamp with time zone", "timestamp without time zone"},
			go_:     TypeMapping{Type: "time.Time", Import: "time"},
			ts:      TypeMapping{Type: "Date"},
			python:  TypeMapping{Type: "datetime", Import: "datetime"},
			java:    TypeMapping{Type: "Instant", Import: "java.time.Instant"},
			kotlin:  TypeMapping{Type: "Instant", Import: "java.time.Instant"},
			zig:     TypeMapping{Type: "i64"},
		},
		{
			pgTypes: []string{"date"},
			go_:     TypeMapping{Type: "time.Time", Import: "time"},
			ts:      TypeMapping{Type: "Date"},
			python:  TypeMapping{Type: "datetime", Import: "datetime"},
			java:    TypeMapping{Type: "Instant", Import: "java.time.Instant"},
			kotlin:  TypeMapping{Type: "Instant", Import: "java.time.Instant"},
			zig:     TypeMapping{Type: "i64"},
		},
		{
			pgTypes: []string{"uuid"},
			go_:     TypeMapping{Type: "uuid.UUID", Import: "github.com/google/uuid"},
			ts:      TypeMapping{Type: "string"},
			python:  TypeMapping{Type: "UUID", Import: "UUID"},
			java:    TypeMapping{Type: "UUID", Import: "java.util.UUID"},
			kotlin:  TypeMapping{Type: "UUID", Import: "java.util.UUID"},
			zig:     TypeMapping{Type: "[16]u8"},
		},
		{
			pgTypes: []string{"jsonb", "json"},
			go_:     TypeMapping{Type: "json.RawMessage", Import: "encoding/json"},
			ts:      TypeMapping{Type: "Record<string, unknown>"},
			python:  TypeMapping{Type: "dict[str, Any]", Import: "Any"},
			java:    TypeMapping{Type: "JsonNode", Import: "com.fasterxml.jackson.databind.JsonNode"},
			kotlin:  TypeMapping{Type: "JsonNode", Import: "com.fasterxml.jackson.databind.JsonNode"},
			zig:     TypeMapping{Type: "[]const u8"},
		},
		{
			pgTypes: []string{"numeric", "decimal"},
			go_:     TypeMapping{Type: "string"},
			ts:      TypeMapping{Type: "string"},
			python:  TypeMapping{Type: "Decimal", Import: "Decimal"},
			java:    TypeMapping{Type: "BigDecimal", Import: "java.math.BigDecimal"},
			kotlin:  TypeMapping{Type: "BigDecimal", Import: "java.math.BigDecimal"},
			zig:     TypeMapping{Type: "[]const u8"},
		},
		{
			pgTypes: []string{"real", "float4"},
			go_:     TypeMapping{Type: "float32"},
			ts:      TypeMapping{Type: "number"},
			python:  TypeMapping{Type: "float"},
			java:    TypeMapping{Type: "float"},
			kotlin:  TypeMapping{Type: "Float"},
			zig:     TypeMapping{Type: "f32"},
		},
		{
			pgTypes: []string{"double precision", "float8"},
			go_:     TypeMapping{Type: "float64"},
			ts:      TypeMapping{Type: "number"},
			python:  TypeMapping{Type: "float"},
			java:    TypeMapping{Type: "double"},
			kotlin:  TypeMapping{Type: "Double"},
			zig:     TypeMapping{Type: "f64"},
		},
		{
			pgTypes: []string{"bytea"},
			go_:     TypeMapping{Type: "[]byte"},
			ts:      TypeMapping{Type: "Uint8Array"},
			python:  TypeMapping{Type: "bytes"},
			java:    TypeMapping{Type: "byte[]"},
			kotlin:  TypeMapping{Type: "ByteArray"},
			zig:     TypeMapping{Type: "[]const u8"},
		},
		{
			pgTypes: []string{"interval"},
			go_:     TypeMapping{Type: "string"},
			ts:      TypeMapping{Type: "string"},
			python:  TypeMapping{Type: "str"},
			java:    TypeMapping{Type: "String"},
			kotlin:  TypeMapping{Type: "String"},
			zig:     TypeMapping{Type: "[]const u8"},
		},
	}

	m := make(map[string]map[Lang]TypeMapping)
	for _, e := range entries {
		langMap := map[Lang]TypeMapping{
			LangGo:     e.go_,
			LangTS:     e.ts,
			LangPython: e.python,
			LangJava:   e.java,
			LangKotlin: e.kotlin,
			LangZig:    e.zig,
		}
		for _, pg := range e.pgTypes {
			m[pg] = langMap
		}
	}
	return m
}
