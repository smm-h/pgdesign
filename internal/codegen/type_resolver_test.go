package codegen

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

func TestTypeResolver_EnumColumn(t *testing.T) {
	col := model.Column{
		Name:     "status",
		PGType:   typeinfo.Type{Base: "user_status"},
		TypeKind: "enum",
	}

	tests := []struct {
		lang Lang
		want string
	}{
		{LangGo, "UserStatus"},
		{LangTS, "UserStatus"},
		{LangPython, "UserStatus"},
		{LangJava, "UserStatus"},
		{LangKotlin, "UserStatus"},
	}

	for _, tt := range tests {
		r := NewTypeResolver(tt.lang)
		got := r.Resolve(col)
		if got.Type != tt.want {
			t.Errorf("Resolve(enum col, %s).Type = %q, want %q", tt.lang, got.Type, tt.want)
		}
	}
}

func TestTypeResolver_EnumColumn_Zig(t *testing.T) {
	col := model.Column{
		Name:     "status",
		PGType:   typeinfo.Type{Base: "user_status"},
		TypeKind: "enum",
	}

	r := NewTypeResolver(LangZig)
	got := r.Resolve(col)
	if got.Type != "[]const u8" {
		t.Errorf("Resolve(enum col, Zig).Type = %q, want %q", got.Type, "[]const u8")
	}
}

func TestTypeResolver_StateMachineColumn(t *testing.T) {
	col := model.Column{
		Name:     "order_status",
		PGType:   typeinfo.Type{Base: "order_status"},
		TypeKind: "state_machine",
	}

	tests := []struct {
		lang Lang
		want string
	}{
		{LangGo, "OrderStatus"},
		{LangTS, "OrderStatus"},
		{LangPython, "OrderStatus"},
	}

	for _, tt := range tests {
		r := NewTypeResolver(tt.lang)
		got := r.Resolve(col)
		if got.Type != tt.want {
			t.Errorf("Resolve(state_machine col, %s).Type = %q, want %q", tt.lang, got.Type, tt.want)
		}
	}

	// Zig should fall back to string.
	r := NewTypeResolver(LangZig)
	got := r.Resolve(col)
	if got.Type != "[]const u8" {
		t.Errorf("Resolve(state_machine col, Zig).Type = %q, want %q", got.Type, "[]const u8")
	}
}

func TestTypeResolver_BuiltinColumn(t *testing.T) {
	col := model.Column{
		Name:     "age",
		PGType:   typeinfo.Type{Base: "integer"},
		TypeKind: "scalar",
	}

	tests := []struct {
		lang Lang
		want string
	}{
		{LangGo, "int32"},
		{LangTS, "number"},
		{LangPython, "int"},
		{LangJava, "int"},
		{LangKotlin, "Int"},
		{LangZig, "i32"},
	}

	for _, tt := range tests {
		r := NewTypeResolver(tt.lang)
		got := r.Resolve(col)
		expected := LookupType("integer", tt.lang)
		if got.Type != expected.Type {
			t.Errorf("Resolve(builtin col, %s).Type = %q, want %q", tt.lang, got.Type, expected.Type)
		}
		if got.Type != tt.want {
			t.Errorf("Resolve(builtin col, %s).Type = %q, want %q", tt.lang, got.Type, tt.want)
		}
	}
}

func TestTypeResolver_MoneyColumn(t *testing.T) {
	col := model.Column{
		Name:             "price",
		PGType:           typeinfo.Type{Base: "bigint"},
		TypeKind:         "scalar",
		SemanticTypeName: "money",
	}

	tests := []struct {
		lang Lang
		want string
	}{
		{LangGo, "int64"},
		{LangTS, "number"},
		{LangPython, "int"},
		{LangJava, "long"},
		{LangKotlin, "Long"},
		{LangZig, "i64"},
	}

	for _, tt := range tests {
		r := NewTypeResolver(tt.lang)
		got := r.Resolve(col)
		if got.Type != tt.want {
			t.Errorf("Resolve(money col, %s).Type = %q, want %q", tt.lang, got.Type, tt.want)
		}
	}
}

func TestTypeResolver_DomainColumn(t *testing.T) {
	// A scalar type with a CHECK becomes a domain. The underlying PG base
	// type should be used for the native type mapping.
	col := model.Column{
		Name:             "email",
		PGType:           typeinfo.Type{Base: "text", DomainName: "email_address"},
		TypeKind:         "scalar",
		SemanticTypeName: "email_address",
	}

	r := NewTypeResolver(LangGo)
	got := r.Resolve(col)
	if got.Type != "string" {
		t.Errorf("Resolve(domain col, Go).Type = %q, want %q", got.Type, "string")
	}

	r = NewTypeResolver(LangTS)
	got = r.Resolve(col)
	if got.Type != "string" {
		t.Errorf("Resolve(domain col, TS).Type = %q, want %q", got.Type, "string")
	}
}

func TestTypeResolver_UnresolvedColumn(t *testing.T) {
	// Columns from introspection may have no TypeKind.
	col := model.Column{
		Name:     "data",
		PGType:   typeinfo.Type{Base: "jsonb"},
		TypeKind: "",
	}

	r := NewTypeResolver(LangGo)
	got := r.Resolve(col)
	expected := LookupType("jsonb", LangGo)
	if got.Type != expected.Type {
		t.Errorf("Resolve(unresolved col, Go).Type = %q, want %q", got.Type, expected.Type)
	}
}

func TestEnumTypeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user_status", "UserStatus"},
		{"order_status", "OrderStatus"},
		{"priority", "Priority"},
		{"http_method", "HttpMethod"},
	}

	for _, tt := range tests {
		got := EnumTypeName(tt.input)
		if got != tt.want {
			t.Errorf("EnumTypeName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
