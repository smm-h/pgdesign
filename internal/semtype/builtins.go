package semtype

import "github.com/smm-h/pgdesign/internal/typeinfo"

// NewBuiltinRegistry creates a Registry pre-populated with all builtin types.
func NewBuiltinRegistry() *Registry {
	r := NewRegistry()

	builtins := []*TypeDef{
		{
			Name:        "id",
			Kind:        KindScalar,
			BaseType:    typeinfo.Type{Base: "uuid"},
			NotNull:     true,
			DefaultExpr: "gen_random_uuid()",
		},
		{
			Name:     "ref",
			Kind:     KindScalar,
			BaseType: typeinfo.Type{Base: "uuid"},
			NotNull:  true,
		},
		{
			Name:        "timestamp",
			Kind:        KindScalar,
			BaseType:    typeinfo.Type{Base: "timestamptz"},
			NotNull:     true,
			DefaultExpr: "now()",
		},
		{
			Name:     "timestamp_optional",
			Kind:     KindScalar,
			BaseType: typeinfo.Type{Base: "timestamptz"},
			NotNull:  false,
		},
		{
			Name:     "money",
			Kind:     KindScalar,
			BaseType: typeinfo.Type{Base: "int8"},
			NotNull:  true,
			Default:  strPtr("0"),
		},
		{
			Name:     "slug",
			Kind:     KindScalar,
			BaseType: typeinfo.Type{Base: "text"},
			NotNull:  true,
			Check:    "VALUE ~ '^[a-z0-9-]+$'",
		},
		{
			Name:     "email",
			Kind:     KindScalar,
			BaseType: typeinfo.Type{Base: "text"},
			NotNull:  true,
			Check:    "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'",
		},
		{
			Name:     "short_text",
			Kind:     KindScalar,
			BaseType: typeinfo.Type{Base: "text"},
			NotNull:  true,
			Check:    "LENGTH(VALUE) <= 255",
		},
		{
			Name:        "json",
			Kind:        KindScalar,
			BaseType:    typeinfo.Type{Base: "jsonb"},
			NotNull:     true,
			DefaultExpr: "'{}'::jsonb",
		},
		{
			Name:        "json_array",
			Kind:        KindScalar,
			BaseType:    typeinfo.Type{Base: "jsonb"},
			NotNull:     true,
			DefaultExpr: "'[]'::jsonb",
		},
		{
			Name:     "counter",
			Kind:     KindScalar,
			BaseType: typeinfo.Type{Base: "int8"},
			NotNull:  true,
			Default:  strPtr("0"),
		},
		{
			Name:     "flag",
			Kind:     KindScalar,
			BaseType: typeinfo.Type{Base: "bool"},
			NotNull:  true,
			Default:  strPtr("false"),
		},
		{
			Name:     "auto_id",
			Kind:     KindScalar,
			BaseType: typeinfo.Type{Base: "int8"},
			NotNull:  true,
			Identity: "ALWAYS",
		},
	}

	for _, td := range builtins {
		td.Source = "builtin"
		// Builtins are guaranteed to not collide, panic on programmer error.
		if err := r.Register(td); err != nil {
			panic("builtin registration failed: " + err.Error())
		}
	}

	return r
}
