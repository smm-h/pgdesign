package codegen

import "testing"

func TestLookupType_AllLanguages(t *testing.T) {
	type langExpect struct {
		lang       Lang
		wantType   string
		wantImport string
	}

	tests := []struct {
		pgType string
		langs  []langExpect
	}{
		{
			pgType: "integer",
			langs: []langExpect{
				{LangGo, "int32", ""},
				{LangTS, "number", ""},
				{LangPython, "int", ""},
				{LangJava, "int", ""},
				{LangKotlin, "Int", ""},
				{LangZig, "i32", ""},
			},
		},
		{
			pgType: "text",
			langs: []langExpect{
				{LangGo, "string", ""},
				{LangTS, "string", ""},
				{LangPython, "str", ""},
				{LangJava, "String", ""},
				{LangKotlin, "String", ""},
				{LangZig, "[]const u8", ""},
			},
		},
		{
			pgType: "boolean",
			langs: []langExpect{
				{LangGo, "bool", ""},
				{LangTS, "boolean", ""},
				{LangPython, "bool", ""},
				{LangJava, "boolean", ""},
				{LangKotlin, "Boolean", ""},
				{LangZig, "bool", ""},
			},
		},
		{
			pgType: "uuid",
			langs: []langExpect{
				{LangGo, "uuid.UUID", "github.com/google/uuid"},
				{LangTS, "string", ""},
				{LangPython, "UUID", "UUID"},
				{LangJava, "UUID", "java.util.UUID"},
				{LangKotlin, "UUID", "java.util.UUID"},
				{LangZig, "[16]u8", ""},
			},
		},
		{
			pgType: "timestamptz",
			langs: []langExpect{
				{LangGo, "time.Time", "time"},
				{LangTS, "Date", ""},
				{LangPython, "datetime", "datetime"},
				{LangJava, "Instant", "java.time.Instant"},
				{LangKotlin, "Instant", "java.time.Instant"},
				{LangZig, "i64", ""},
			},
		},
		{
			pgType: "jsonb",
			langs: []langExpect{
				{LangGo, "json.RawMessage", "encoding/json"},
				{LangTS, "Record<string, unknown>", ""},
				{LangPython, "dict[str, Any]", "Any"},
				{LangJava, "JsonNode", "com.fasterxml.jackson.databind.JsonNode"},
				{LangKotlin, "JsonNode", "com.fasterxml.jackson.databind.JsonNode"},
				{LangZig, "[]const u8", ""},
			},
		},
		{
			pgType: "bytea",
			langs: []langExpect{
				{LangGo, "[]byte", ""},
				{LangTS, "Uint8Array", ""},
				{LangPython, "bytes", ""},
				{LangJava, "byte[]", ""},
				{LangKotlin, "ByteArray", ""},
				{LangZig, "[]const u8", ""},
			},
		},
	}

	for _, tt := range tests {
		for _, le := range tt.langs {
			got := LookupType(tt.pgType, le.lang)
			if got.Type != le.wantType {
				t.Errorf("LookupType(%q, %s).Type = %q, want %q", tt.pgType, le.lang, got.Type, le.wantType)
			}
			if got.Import != le.wantImport {
				t.Errorf("LookupType(%q, %s).Import = %q, want %q", tt.pgType, le.lang, got.Import, le.wantImport)
			}
		}
	}
}

func TestLookupType_Aliases(t *testing.T) {
	aliases := []struct {
		alias     string
		canonical string
	}{
		{"int4", "integer"},
		{"int8", "bigint"},
		{"int2", "smallint"},
		{"float4", "real"},
		{"float8", "double precision"},
		{"bool", "boolean"},
		{"varchar", "text"},
		{"character varying", "text"},
	}

	langs := []Lang{LangGo, LangTS, LangPython, LangJava, LangKotlin, LangZig}

	for _, a := range aliases {
		for _, lang := range langs {
			got := LookupType(a.alias, lang)
			want := LookupType(a.canonical, lang)
			if got.Type != want.Type {
				t.Errorf("LookupType(%q, %s).Type = %q, want %q (same as %q)",
					a.alias, lang, got.Type, want.Type, a.canonical)
			}
			if got.Import != want.Import {
				t.Errorf("LookupType(%q, %s).Import = %q, want %q (same as %q)",
					a.alias, lang, got.Import, want.Import, a.canonical)
			}
		}
	}
}

func TestLookupType_CaseInsensitive(t *testing.T) {
	variants := []string{"INTEGER", "Integer", "iNtEgEr", "TEXT", "Text", "BOOLEAN", "Boolean", "UUID", "Uuid"}

	for _, v := range variants {
		got := LookupType(v, LangGo)
		if got.Type == "string" && v != "TEXT" && v != "Text" {
			// Fallback to string means lookup failed (except for text which IS string)
			t.Errorf("LookupType(%q, LangGo) fell through to fallback, got %q", v, got.Type)
		}
		if got.Type == "" {
			t.Errorf("LookupType(%q, LangGo) returned empty type", v)
		}
	}

	// Verify specific case-insensitive lookups produce the exact expected type.
	if got := LookupType("INTEGER", LangGo); got.Type != "int32" {
		t.Errorf("LookupType(\"INTEGER\", LangGo).Type = %q, want \"int32\"", got.Type)
	}
	if got := LookupType("Boolean", LangTS); got.Type != "boolean" {
		t.Errorf("LookupType(\"Boolean\", LangTS).Type = %q, want \"boolean\"", got.Type)
	}
	if got := LookupType("UUID", LangPython); got.Type != "UUID" {
		t.Errorf("LookupType(\"UUID\", LangPython).Type = %q, want \"UUID\"", got.Type)
	}
	if got := LookupType("TIMESTAMPTZ", LangJava); got.Type != "Instant" {
		t.Errorf("LookupType(\"TIMESTAMPTZ\", LangJava).Type = %q, want \"Instant\"", got.Type)
	}
}

func TestLookupType_Fallback(t *testing.T) {
	unknownTypes := []string{"user_status", "my_custom_type", "order_state", ""}

	expected := map[Lang]string{
		LangGo:     "string",
		LangTS:     "string",
		LangPython: "str",
		LangJava:   "String",
		LangKotlin: "String",
		LangZig:    "[]const u8",
	}

	for _, pgType := range unknownTypes {
		for lang, wantType := range expected {
			got := LookupType(pgType, lang)
			if got.Type != wantType {
				t.Errorf("LookupType(%q, %s).Type = %q, want %q (fallback)", pgType, lang, got.Type, wantType)
			}
			if got.Import != "" {
				t.Errorf("LookupType(%q, %s).Import = %q, want empty (fallback)", pgType, lang, got.Import)
			}
		}
	}
}

func TestLookupMoneyType(t *testing.T) {
	expected := map[Lang]string{
		LangGo:     "int64",
		LangTS:     "number",
		LangPython: "int",
		LangJava:   "long",
		LangKotlin: "Long",
		LangZig:    "i64",
	}

	for lang, want := range expected {
		got := LookupMoneyType(lang)
		if got != want {
			t.Errorf("LookupMoneyType(%s) = %q, want %q", lang, got, want)
		}
	}
}

func TestApplyNullable(t *testing.T) {
	tests := []struct {
		lang Lang
		want string
	}{
		{LangGo, "*int32"},
		{LangTS, "int32 | null"},
		{LangPython, "Optional[int32]"},
		{LangJava, "int32"},
		{LangKotlin, "int32?"},
		{LangZig, "?int32"},
	}

	for _, tt := range tests {
		got := ApplyNullable("int32", tt.lang)
		if got != tt.want {
			t.Errorf("ApplyNullable(\"int32\", %s) = %q, want %q", tt.lang, got, tt.want)
		}
	}
}

func TestApplyArray(t *testing.T) {
	tests := []struct {
		lang Lang
		want string
	}{
		{LangGo, "[]string"},
		{LangTS, "string[]"},
		{LangPython, "list[string]"},
		{LangJava, "List<string>"},
		{LangKotlin, "List<string>"},
		{LangZig, "[]string"},
	}

	for _, tt := range tests {
		got := ApplyArray("string", tt.lang)
		if got != tt.want {
			t.Errorf("ApplyArray(\"string\", %s) = %q, want %q", tt.lang, got, tt.want)
		}
	}
}

func TestApplyNullable_Array_Combination(t *testing.T) {
	tests := []struct {
		lang Lang
		want string
	}{
		{LangGo, "*[]int32"},
		{LangTS, "int32[] | null"},
		{LangPython, "Optional[list[int32]]"},
		{LangJava, "List<int32>"},
		{LangKotlin, "List<int32>?"},
		{LangZig, "?[]int32"},
	}

	for _, tt := range tests {
		arrType := ApplyArray("int32", tt.lang)
		got := ApplyNullable(arrType, tt.lang)
		if got != tt.want {
			t.Errorf("ApplyNullable(ApplyArray(\"int32\", %s), %s) = %q, want %q",
				tt.lang, tt.lang, got, tt.want)
		}
	}
}
