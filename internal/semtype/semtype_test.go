package semtype

import (
	"testing"
)

func TestBuiltinResolve(t *testing.T) {
	r := NewBuiltinRegistry()

	tests := []struct {
		name        string
		pgType      string
		notNull     bool
		defaultVal  string
		defaultExpr string
		check       string
		generated   string
		identity    string
	}{
		{"id", "uuid", true, "", "gen_random_uuid()", "", "", ""},
		{"ref", "uuid", true, "", "", "", "", ""},
		{"timestamp", "timestamptz", true, "", "now()", "", "", ""},
		{"timestamp_optional", "timestamptz", false, "", "", "", "", ""},
		{"money", "bigint", true, "0", "", "", "", ""},
		{"slug", "text", true, "", "", "VALUE ~ '^[a-z0-9-]+$'", "", ""},
		{"email", "text", true, "", "", "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'", "", ""},
		{"short_text", "text", true, "", "", "LENGTH(VALUE) <= 255", "", ""},
		{"json", "jsonb", true, "", "'{}'::jsonb", "", "", ""},
		{"json_array", "jsonb", true, "", "'[]'::jsonb", "", "", ""},
		{"counter", "bigint", true, "0", "", "", "", ""},
		{"flag", "boolean", true, "false", "", "", "", ""},
		{"auto_id", "bigint", true, "", "", "", "", "ALWAYS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			td, err := r.Resolve(tt.name)
			if err != nil {
				t.Fatalf("Resolve(%q) returned error: %v", tt.name, err)
			}
			if td.BaseType != tt.pgType {
				t.Errorf("BaseType = %q, want %q", td.BaseType, tt.pgType)
			}
			if td.NotNull != tt.notNull {
				t.Errorf("NotNull = %v, want %v", td.NotNull, tt.notNull)
			}
			if td.Default != tt.defaultVal {
				t.Errorf("Default = %q, want %q", td.Default, tt.defaultVal)
			}
			if td.DefaultExpr != tt.defaultExpr {
				t.Errorf("DefaultExpr = %q, want %q", td.DefaultExpr, tt.defaultExpr)
			}
			if td.Check != tt.check {
				t.Errorf("Check = %q, want %q", td.Check, tt.check)
			}
			if td.Generated != tt.generated {
				t.Errorf("Generated = %q, want %q", td.Generated, tt.generated)
			}
			if td.Identity != tt.identity {
				t.Errorf("Identity = %q, want %q", td.Identity, tt.identity)
			}
		})
	}
}

func TestResolveUnknownType(t *testing.T) {
	r := NewBuiltinRegistry()
	_, err := r.Resolve("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	td := &TypeDef{Name: "test", Kind: KindScalar, BaseType: "text"}
	if err := r.Register(td); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}

	// Identical duplicate registration should succeed (idempotent).
	td2 := &TypeDef{Name: "test", Kind: KindScalar, BaseType: "text"}
	if err := r.Register(td2); err != nil {
		t.Fatalf("identical duplicate should succeed, got: %v", err)
	}

	// Conflicting duplicate should fail.
	td3 := &TypeDef{Name: "test", Kind: KindScalar, BaseType: "integer"}
	if err := r.Register(td3); err == nil {
		t.Fatal("expected error for conflicting duplicate registration, got nil")
	}
}

func TestLoadUserEnumType(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name:   "status",
			Kind:   "enum",
			Values: []string{"active", "inactive", "pending"},
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	td, err := r.Resolve("status")
	if err != nil {
		t.Fatalf("Resolve(status) error: %v", err)
	}
	if td.Kind != KindEnum {
		t.Errorf("Kind = %v, want KindEnum", td.Kind)
	}
	if len(td.EnumValues) != 3 {
		t.Errorf("EnumValues length = %d, want 3", len(td.EnumValues))
	}
	if td.EnumValues[0] != "active" {
		t.Errorf("EnumValues[0] = %q, want %q", td.EnumValues[0], "active")
	}
}

func TestLoadUserScalarTypeWithCheck(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name:  "positive_int",
			Kind:  "scalar",
			Base:  "integer",
			Check: "VALUE > 0",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	td, err := r.Resolve("positive_int")
	if err != nil {
		t.Fatalf("Resolve(positive_int) error: %v", err)
	}
	if td.BaseType != "integer" {
		t.Errorf("BaseType = %q, want %q", td.BaseType, "integer")
	}
	if td.Check != "VALUE > 0" {
		t.Errorf("Check = %q, want %q", td.Check, "VALUE > 0")
	}
	if !td.NotNull {
		t.Error("NotNull = false, want true (default)")
	}
}

func TestLoadUserScalarTypeWithNotNull(t *testing.T) {
	r := NewBuiltinRegistry()

	notNull := false
	userTypes := []UserTypeDef{
		{
			Name:    "optional_text",
			Kind:    "scalar",
			Base:    "text",
			NotNull: &notNull,
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	td, err := r.Resolve("optional_text")
	if err != nil {
		t.Fatalf("Resolve(optional_text) error: %v", err)
	}
	if td.NotNull {
		t.Error("NotNull = true, want false")
	}
}

func TestLoadUserTypeEnumNoValues(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "empty_enum",
			Kind: "enum",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for enum with no values, got none")
	}

	found := false
	for _, d := range diags {
		if d.Code == "E101" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E101 for enum with no values")
	}
}

func TestLoadUserTypeUnknownBaseType(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "bad_scalar",
			Kind: "scalar",
			Base: "not_a_pg_type",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for unknown base type, got none")
	}

	found := false
	for _, d := range diags {
		if d.Code == "E106" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E106 for unknown base type")
	}
}

func TestLoadUserTypeCheckWithoutValuePlaceholder(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name:  "bad_check",
			Kind:  "scalar",
			Base:  "integer",
			Check: "x > 0",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for check without VALUE, got none")
	}

	found := false
	for _, d := range diags {
		if d.Code == "E108" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E108 for check without VALUE")
	}
}

func TestResolveColumnOverrideNullable(t *testing.T) {
	r := NewBuiltinRegistry()

	// id type is NOT NULL by default
	nullable := true
	rc, err := r.ResolveColumn("id", &nullable, nil, nil, nil)
	if err != nil {
		t.Fatalf("ResolveColumn error: %v", err)
	}
	if rc.NotNull {
		t.Error("NotNull = true, want false (nullable override)")
	}
	if rc.PGType != "uuid" {
		t.Errorf("PGType = %q, want %q", rc.PGType, "uuid")
	}
	if rc.DefaultExpr != "gen_random_uuid()" {
		t.Errorf("DefaultExpr = %q, want %q", rc.DefaultExpr, "gen_random_uuid()")
	}
}

func TestResolveColumnOverrideDefault(t *testing.T) {
	r := NewBuiltinRegistry()

	// money type has Default="0"
	newDefault := "100"
	rc, err := r.ResolveColumn("money", nil, &newDefault, nil, nil)
	if err != nil {
		t.Fatalf("ResolveColumn error: %v", err)
	}
	if rc.Default != "100" {
		t.Errorf("Default = %q, want %q", rc.Default, "100")
	}
	if rc.DefaultExpr != "" {
		t.Errorf("DefaultExpr = %q, want empty (literal default takes precedence)", rc.DefaultExpr)
	}
}

func TestResolveColumnOverrideDefaultExpr(t *testing.T) {
	r := NewBuiltinRegistry()

	// counter type has Default="0", override with expression
	newExpr := "nextval('my_seq')"
	rc, err := r.ResolveColumn("counter", nil, nil, &newExpr, nil)
	if err != nil {
		t.Fatalf("ResolveColumn error: %v", err)
	}
	if rc.DefaultExpr != "nextval('my_seq')" {
		t.Errorf("DefaultExpr = %q, want %q", rc.DefaultExpr, "nextval('my_seq')")
	}
	if rc.Default != "" {
		t.Errorf("Default = %q, want empty (expr default takes precedence)", rc.Default)
	}
}

func TestResolveColumnNoOverrides(t *testing.T) {
	r := NewBuiltinRegistry()

	rc, err := r.ResolveColumn("slug", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("ResolveColumn error: %v", err)
	}
	if rc.PGType != "text" {
		t.Errorf("PGType = %q, want %q", rc.PGType, "text")
	}
	if !rc.NotNull {
		t.Error("NotNull = false, want true")
	}
	if rc.Check != "VALUE ~ '^[a-z0-9-]+$'" {
		t.Errorf("Check = %q, want %q", rc.Check, "VALUE ~ '^[a-z0-9-]+$'")
	}
}

func TestResolveColumnUnknownType(t *testing.T) {
	r := NewBuiltinRegistry()

	_, err := r.ResolveColumn("nonexistent", nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}

func TestResolveColumnIdentity(t *testing.T) {
	r := NewBuiltinRegistry()

	rc, err := r.ResolveColumn("auto_id", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("ResolveColumn error: %v", err)
	}
	if rc.Identity != "ALWAYS" {
		t.Errorf("Identity = %q, want %q", rc.Identity, "ALWAYS")
	}
	if rc.Generated != "" {
		t.Errorf("Generated = %q, want empty (identity columns use Identity field)", rc.Generated)
	}
}

func TestLoadUserEnumType_ValidDefault(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name:    "status",
			Kind:    "enum",
			Values:  []string{"created", "running", "done"},
			Default: "created",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	td, err := r.Resolve("status")
	if err != nil {
		t.Fatalf("Resolve(status) error: %v", err)
	}
	if td.Default != "created" {
		t.Errorf("Default = %q, want %q", td.Default, "created")
	}
}

func TestLoadUserEnumType_InvalidDefault_E109(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name:    "status",
			Kind:    "enum",
			Values:  []string{"created", "running"},
			Default: "'created'",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for invalid enum default, got none")
	}

	found := false
	for _, d := range diags {
		if d.Code == "E109" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E109 for invalid enum default")
	}
}
