package semtype

// NewBuiltinRegistry creates a Registry pre-populated with all builtin types.
func NewBuiltinRegistry() *Registry {
	r := NewRegistry()

	builtins := []*TypeDef{
		{
			Name:        "id",
			Kind:        KindScalar,
			BaseType:    "uuid",
			NotNull:     true,
			DefaultExpr: "gen_random_uuid()",
		},
		{
			Name:     "ref",
			Kind:     KindScalar,
			BaseType: "uuid",
			NotNull:  true,
		},
		{
			Name:        "timestamp",
			Kind:        KindScalar,
			BaseType:    "timestamptz",
			NotNull:     true,
			DefaultExpr: "now()",
		},
		{
			Name:     "timestamp_optional",
			Kind:     KindScalar,
			BaseType: "timestamptz",
			NotNull:  false,
		},
		{
			Name:     "money",
			Kind:     KindScalar,
			BaseType: "bigint",
			NotNull:  true,
			Default:  "0",
		},
		{
			Name:     "slug",
			Kind:     KindScalar,
			BaseType: "text",
			NotNull:  true,
			Check:    "VALUE ~ '^[a-z0-9-]+$'",
		},
		{
			Name:     "email",
			Kind:     KindScalar,
			BaseType: "text",
			NotNull:  true,
			Check:    "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'",
		},
		{
			Name:     "short_text",
			Kind:     KindScalar,
			BaseType: "text",
			NotNull:  true,
			Check:    "LENGTH(VALUE) <= 255",
		},
		{
			Name:        "json",
			Kind:        KindScalar,
			BaseType:    "jsonb",
			NotNull:     true,
			DefaultExpr: "'{}'::jsonb",
		},
		{
			Name:        "json_array",
			Kind:        KindScalar,
			BaseType:    "jsonb",
			NotNull:     true,
			DefaultExpr: "'[]'::jsonb",
		},
		{
			Name:     "counter",
			Kind:     KindScalar,
			BaseType: "bigint",
			NotNull:  true,
			Default:  "0",
		},
		{
			Name:     "flag",
			Kind:     KindScalar,
			BaseType: "boolean",
			NotNull:  true,
			Default:  "false",
		},
		{
			Name:      "auto_id",
			Kind:      KindScalar,
			BaseType:  "bigint",
			NotNull:   true,
			Generated: "ALWAYS AS IDENTITY",
		},
	}

	for _, td := range builtins {
		// Builtins are guaranteed to not collide, panic on programmer error.
		if err := r.Register(td); err != nil {
			panic("builtin registration failed: " + err.Error())
		}
	}

	return r
}
