package codegen

import (
	"github.com/smm-h/pgdesign/internal/model"
)

// TypeResolver resolves model columns to native type mappings based on TypeKind.
// It dispatches enum/state_machine columns to PascalCase type names, while
// scalar and builtin columns use the standard type lookup table.
type TypeResolver struct {
	Lang Lang
}

// NewTypeResolver creates a TypeResolver for the given target language.
func NewTypeResolver(lang Lang) *TypeResolver {
	return &TypeResolver{Lang: lang}
}

// Resolve returns the native type mapping for a column based on its TypeKind.
//
// Dispatch logic:
//   - "enum", "state_machine": PascalCase type name (Zig uses []const u8)
//   - "scalar" with DomainName: domain-backed column, use underlying PG base type
//   - "scalar" without DomainName, "": builtin type, use standard lookup
//   - money semantic type: integer cents type
func (r *TypeResolver) Resolve(col model.Column) TypeMapping {
	switch col.TypeKind {
	case "enum", "state_machine":
		if r.Lang == LangZig {
			return TypeMapping{Type: "[]const u8"}
		}
		return TypeMapping{Type: EnumTypeName(col.PGType.Base)}
	case "scalar":
		// Scalar covers both domain types and builtins. Domain types use the
		// underlying PG base type for the native mapping. Builtins go through
		// standard lookup which handles the money semantic type override.
		if col.SemanticTypeName == "money" {
			return TypeMapping{Type: LookupMoneyType(r.Lang)}
		}
		return LookupType(col.PGType.Base, r.Lang)
	default:
		// Unresolved or introspected columns without TypeKind.
		if col.SemanticTypeName == "money" {
			return TypeMapping{Type: LookupMoneyType(r.Lang)}
		}
		return LookupType(col.PGType.Base, r.Lang)
	}
}

// EnumTypeName converts a PG enum/state_machine type name to a PascalCase
// identifier suitable for use as a type name in generated code.
func EnumTypeName(name string) string {
	return toPascalCase(name)
}
