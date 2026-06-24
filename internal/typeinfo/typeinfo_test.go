package typeinfo

import (
	"testing"
)

// --- Parse: alias map entries ---

func TestParseAliases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"character varying", "varchar"},
		{"CHARACTER VARYING", "varchar"},
		{"character", "char"},
		{"char", "char"},
		{"double precision", "float8"},
		{"boolean", "bool"},
		{"BOOLEAN", "bool"},
		{"integer", "int4"},
		{"smallint", "int2"},
		{"bigint", "int8"},
		{"real", "float4"},
		{"timestamp with time zone", "timestamptz"},
		{"timestamp without time zone", "timestamp"},
		{"time with time zone", "timetz"},
		{"time without time zone", "time"},
		{"bit varying", "varbit"},
		{"int", "int4"},
		{"float", "float8"},
		{"decimal", "numeric"},
		{"serial", "serial"},
		{"bigserial", "bigserial"},
		{"smallserial", "smallserial"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Parse(tt.input)
			if got.Base != tt.want {
				t.Errorf("Parse(%q).Base = %q, want %q", tt.input, got.Base, tt.want)
			}
		})
	}
}

// --- Parse: parameterized types ---

func TestParseParameterized(t *testing.T) {
	t.Run("numeric(12,6)", func(t *testing.T) {
		got := Parse("numeric(12,6)")
		if got.Base != "numeric" {
			t.Errorf("Base = %q, want %q", got.Base, "numeric")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 12 {
			t.Errorf("Precision = %v, want 12", got.Params.Precision)
		}
		if got.Params.Scale == nil || *got.Params.Scale != 6 {
			t.Errorf("Scale = %v, want 6", got.Params.Scale)
		}
	})

	t.Run("decimal(10,2)", func(t *testing.T) {
		got := Parse("decimal(10,2)")
		if got.Base != "numeric" {
			t.Errorf("Base = %q, want %q", got.Base, "numeric")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 10 {
			t.Errorf("Precision = %v, want 10", got.Params.Precision)
		}
		if got.Params.Scale == nil || *got.Params.Scale != 2 {
			t.Errorf("Scale = %v, want 2", got.Params.Scale)
		}
	})

	t.Run("numeric(5)", func(t *testing.T) {
		got := Parse("numeric(5)")
		if got.Base != "numeric" {
			t.Errorf("Base = %q, want %q", got.Base, "numeric")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 5 {
			t.Errorf("Precision = %v, want 5", got.Params.Precision)
		}
		if got.Params.Scale != nil {
			t.Errorf("Scale = %v, want nil", got.Params.Scale)
		}
	})

	t.Run("varchar(255)", func(t *testing.T) {
		got := Parse("varchar(255)")
		if got.Base != "varchar" {
			t.Errorf("Base = %q, want %q", got.Base, "varchar")
		}
		if got.Params.Length == nil || *got.Params.Length != 255 {
			t.Errorf("Length = %v, want 255", got.Params.Length)
		}
	})

	t.Run("character varying(100)", func(t *testing.T) {
		got := Parse("character varying(100)")
		if got.Base != "varchar" {
			t.Errorf("Base = %q, want %q", got.Base, "varchar")
		}
		if got.Params.Length == nil || *got.Params.Length != 100 {
			t.Errorf("Length = %v, want 100", got.Params.Length)
		}
	})

	t.Run("char(1)", func(t *testing.T) {
		got := Parse("char(1)")
		if got.Base != "char" {
			t.Errorf("Base = %q, want %q", got.Base, "char")
		}
		if got.Params.Length == nil || *got.Params.Length != 1 {
			t.Errorf("Length = %v, want 1", got.Params.Length)
		}
	})

	t.Run("varbit(64)", func(t *testing.T) {
		got := Parse("varbit(64)")
		if got.Base != "varbit" {
			t.Errorf("Base = %q, want %q", got.Base, "varbit")
		}
		if got.Params.Length == nil || *got.Params.Length != 64 {
			t.Errorf("Length = %v, want 64", got.Params.Length)
		}
	})

	t.Run("bit(8)", func(t *testing.T) {
		got := Parse("bit(8)")
		if got.Base != "bit" {
			t.Errorf("Base = %q, want %q", got.Base, "bit")
		}
		if got.Params.Length == nil || *got.Params.Length != 8 {
			t.Errorf("Length = %v, want 8", got.Params.Length)
		}
	})

	t.Run("timestamp(3)", func(t *testing.T) {
		got := Parse("timestamp(3)")
		if got.Base != "timestamp" {
			t.Errorf("Base = %q, want %q", got.Base, "timestamp")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 3 {
			t.Errorf("Precision = %v, want 3", got.Params.Precision)
		}
	})

	t.Run("timestamptz(6)", func(t *testing.T) {
		got := Parse("timestamptz(6)")
		if got.Base != "timestamptz" {
			t.Errorf("Base = %q, want %q", got.Base, "timestamptz")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 6 {
			t.Errorf("Precision = %v, want 6", got.Params.Precision)
		}
	})

	t.Run("time(0)", func(t *testing.T) {
		got := Parse("time(0)")
		if got.Base != "time" {
			t.Errorf("Base = %q, want %q", got.Base, "time")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 0 {
			t.Errorf("Precision = %v, want 0", got.Params.Precision)
		}
	})

	t.Run("timetz(3)", func(t *testing.T) {
		got := Parse("timetz(3)")
		if got.Base != "timetz" {
			t.Errorf("Base = %q, want %q", got.Base, "timetz")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 3 {
			t.Errorf("Precision = %v, want 3", got.Params.Precision)
		}
	})

	t.Run("interval(4)", func(t *testing.T) {
		got := Parse("interval(4)")
		if got.Base != "interval" {
			t.Errorf("Base = %q, want %q", got.Base, "interval")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 4 {
			t.Errorf("Precision = %v, want 4", got.Params.Precision)
		}
	})
}

// --- Parse: multi-word types with interior params ---

func TestParseMultiWordWithParams(t *testing.T) {
	t.Run("timestamp(3) with time zone", func(t *testing.T) {
		got := Parse("timestamp(3) with time zone")
		if got.Base != "timestamptz" {
			t.Errorf("Base = %q, want %q", got.Base, "timestamptz")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 3 {
			t.Errorf("Precision = %v, want 3", got.Params.Precision)
		}
	})

	t.Run("timestamp(6) without time zone", func(t *testing.T) {
		got := Parse("timestamp(6) without time zone")
		if got.Base != "timestamp" {
			t.Errorf("Base = %q, want %q", got.Base, "timestamp")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 6 {
			t.Errorf("Precision = %v, want 6", got.Params.Precision)
		}
	})

	t.Run("time(2) with time zone", func(t *testing.T) {
		got := Parse("time(2) with time zone")
		if got.Base != "timetz" {
			t.Errorf("Base = %q, want %q", got.Base, "timetz")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 2 {
			t.Errorf("Precision = %v, want 2", got.Params.Precision)
		}
	})

	t.Run("time(0) without time zone", func(t *testing.T) {
		got := Parse("time(0) without time zone")
		if got.Base != "time" {
			t.Errorf("Base = %q, want %q", got.Base, "time")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 0 {
			t.Errorf("Precision = %v, want 0", got.Params.Precision)
		}
	})

	t.Run("TIMESTAMP(3) WITH TIME ZONE", func(t *testing.T) {
		got := Parse("TIMESTAMP(3) WITH TIME ZONE")
		if got.Base != "timestamptz" {
			t.Errorf("Base = %q, want %q", got.Base, "timestamptz")
		}
		if got.Params.Precision == nil || *got.Params.Precision != 3 {
			t.Errorf("Precision = %v, want 3", got.Params.Precision)
		}
	})
}

// --- Parse: extension types ---

func TestParseExtensionTypes(t *testing.T) {
	t.Run("vector(1536)", func(t *testing.T) {
		got := Parse("vector(1536)")
		if got.Base != "vector" {
			t.Errorf("Base = %q, want %q", got.Base, "vector")
		}
		if got.Params.RawModifier != "1536" {
			t.Errorf("RawModifier = %q, want %q", got.Params.RawModifier, "1536")
		}
		// Named params should not be set.
		if got.Params.Precision != nil {
			t.Errorf("Precision should be nil for extension type, got %v", *got.Params.Precision)
		}
		if got.Params.Length != nil {
			t.Errorf("Length should be nil for extension type, got %v", *got.Params.Length)
		}
	})

	t.Run("geometry(Point,4326)", func(t *testing.T) {
		got := Parse("geometry(Point,4326)")
		if got.Base != "geometry" {
			t.Errorf("Base = %q, want %q", got.Base, "geometry")
		}
		if got.Params.RawModifier != "point,4326" {
			t.Errorf("RawModifier = %q, want %q", got.Params.RawModifier, "point,4326")
		}
	})
}

// --- Parse: bare types ---

func TestParseBareTypes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"text", "text"},
		{"uuid", "uuid"},
		{"jsonb", "jsonb"},
		{"json", "json"},
		{"bytea", "bytea"},
		{"inet", "inet"},
		{"cidr", "cidr"},
		{"macaddr", "macaddr"},
		{"xml", "xml"},
		{"money", "money"},
		{"oid", "oid"},
		{"int4range", "int4range"},
		{"tsrange", "tsrange"},
		{"daterange", "daterange"},
		{"tsvector", "tsvector"},
		{"tsquery", "tsquery"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Parse(tt.input)
			if got.Base != tt.want {
				t.Errorf("Parse(%q).Base = %q, want %q", tt.input, got.Base, tt.want)
			}
			if got.Params.Precision != nil || got.Params.Scale != nil ||
				got.Params.Length != nil || got.Params.RawModifier != "" {
				t.Errorf("Parse(%q).Params should be zero, got %+v", tt.input, got.Params)
			}
		})
	}
}

// --- Parse: empty and whitespace ---

func TestParseEmpty(t *testing.T) {
	got := Parse("")
	if got.Base != "" {
		t.Errorf("Parse(\"\").Base = %q, want \"\"", got.Base)
	}

	got = Parse("   ")
	if got.Base != "" {
		t.Errorf("Parse(\"   \").Base = %q, want \"\"", got.Base)
	}
}

// --- Parse: array types ---

func TestParseArrayTypes(t *testing.T) {
	t.Run("text[]", func(t *testing.T) {
		got := Parse("text[]")
		if got.Base != "text[]" {
			t.Errorf("Base = %q, want %q", got.Base, "text[]")
		}
	})

	t.Run("integer[]", func(t *testing.T) {
		got := Parse("integer[]")
		if got.Base != "int4[]" {
			t.Errorf("Base = %q, want %q", got.Base, "int4[]")
		}
	})

	t.Run("varchar(100)[]", func(t *testing.T) {
		got := Parse("varchar(100)[]")
		if got.Base != "varchar[]" {
			t.Errorf("Base = %q, want %q", got.Base, "varchar[]")
		}
		if got.Params.Length == nil || *got.Params.Length != 100 {
			t.Errorf("Length = %v, want 100", got.Params.Length)
		}
	})
}

// --- Reconstruct round-trips ---

func TestReconstructRoundTrips(t *testing.T) {
	tests := []struct {
		input string
		want  string // expected canonical form after round-trip
	}{
		{"varchar(255)", "varchar(255)"},
		{"numeric(12,6)", "numeric(12,6)"},
		{"numeric(5)", "numeric(5)"},
		{"timestamp(3)", "timestamp(3)"},
		{"timestamptz(6)", "timestamptz(6)"},
		{"time(0)", "time(0)"},
		{"timetz(3)", "timetz(3)"},
		{"interval(4)", "interval(4)"},
		{"char(1)", "char(1)"},
		{"bit(8)", "bit(8)"},
		{"varbit(64)", "varbit(64)"},
		{"text", "text"},
		{"uuid", "uuid"},
		{"jsonb", "jsonb"},
		{"int4", "int4"},
		{"bool", "bool"},
		{"float8", "float8"},
		{"vector(1536)", "vector(1536)"},
		{"text[]", "text[]"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			parsed := Parse(tt.input)
			got := Reconstruct(parsed)
			if got != tt.want {
				t.Errorf("Reconstruct(Parse(%q)) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Reconstruct with alias normalization ---

func TestReconstructNormalized(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"integer", "int4"},
		{"boolean", "bool"},
		{"double precision", "float8"},
		{"character varying(100)", "varchar(100)"},
		{"timestamp(3) with time zone", "timestamptz(3)"},
		{"timestamp without time zone", "timestamp"},
		{"smallint", "int2"},
		{"bigint", "int8"},
		{"real", "float4"},
		{"decimal(10,2)", "numeric(10,2)"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			parsed := Parse(tt.input)
			got := Reconstruct(parsed)
			if got != tt.want {
				t.Errorf("Reconstruct(Parse(%q)) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Reconstruct with DomainName ---

func TestReconstructDomainName(t *testing.T) {
	typ := Type{
		Base:       "varchar",
		DomainName: "email_address",
		Params:     Params{Length: intPtr(255)},
	}
	got := Reconstruct(typ)
	if got != "email_address" {
		t.Errorf("Reconstruct with DomainName = %q, want %q", got, "email_address")
	}
}

// --- Reconstruct empty ---

func TestReconstructEmpty(t *testing.T) {
	got := Reconstruct(Type{})
	if got != "" {
		t.Errorf("Reconstruct(Type{}) = %q, want \"\"", got)
	}
}

// --- T() helper ---

func TestT(t *testing.T) {
	got := T("text")
	if got.Base != "text" {
		t.Errorf("T(\"text\").Base = %q, want %q", got.Base, "text")
	}
	if got.DomainName != "" {
		t.Errorf("T(\"text\").DomainName = %q, want \"\"", got.DomainName)
	}
	if got.Params.Precision != nil || got.Params.Scale != nil ||
		got.Params.Length != nil || got.Params.RawModifier != "" {
		t.Errorf("T(\"text\").Params should be zero, got %+v", got.Params)
	}
}

// --- MustParse ---

func TestMustParsePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("MustParse(\"\") did not panic")
		}
	}()
	MustParse("")
}

func TestMustParsePanicsWhitespace(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("MustParse(\"   \") did not panic")
		}
	}()
	MustParse("   ")
}

func TestMustParseValid(t *testing.T) {
	got := MustParse("varchar(255)")
	if got.Base != "varchar" {
		t.Errorf("MustParse(\"varchar(255)\").Base = %q, want %q", got.Base, "varchar")
	}
	if got.Params.Length == nil || *got.Params.Length != 255 {
		t.Errorf("Length = %v, want 255", got.Params.Length)
	}
}

// --- DomainName is never set by Parse ---

func TestParseDomainNameAlwaysEmpty(t *testing.T) {
	inputs := []string{
		"text",
		"varchar(255)",
		"integer",
		"timestamp(3) with time zone",
		"numeric(12,6)",
		"vector(1536)",
		"boolean",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got := Parse(input)
			if got.DomainName != "" {
				t.Errorf("Parse(%q).DomainName = %q, want \"\"", input, got.DomainName)
			}
		})
	}
}

// --- Reconstruct array types with params ---

func TestReconstructArrayWithParams(t *testing.T) {
	typ := Type{
		Base:   "varchar[]",
		Params: Params{Length: intPtr(100)},
	}
	got := Reconstruct(typ)
	if got != "varchar(100)[]" {
		t.Errorf("Reconstruct = %q, want %q", got, "varchar(100)[]")
	}
}

// --- Parse: bit varying with params ---

func TestParseBitVaryingWithParams(t *testing.T) {
	got := Parse("bit varying(128)")
	if got.Base != "varbit" {
		t.Errorf("Base = %q, want %q", got.Base, "varbit")
	}
	if got.Params.Length == nil || *got.Params.Length != 128 {
		t.Errorf("Length = %v, want 128", got.Params.Length)
	}
}

// --- Parse: character with params ---

func TestParseCharacterWithParams(t *testing.T) {
	got := Parse("character(10)")
	if got.Base != "char" {
		t.Errorf("Base = %q, want %q", got.Base, "char")
	}
	if got.Params.Length == nil || *got.Params.Length != 10 {
		t.Errorf("Length = %v, want 10", got.Params.Length)
	}
}
