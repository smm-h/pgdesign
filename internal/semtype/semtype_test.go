package semtype

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/typeinfo"
)

func TestBuiltinResolve(t *testing.T) {
	r := NewBuiltinRegistry()

	tests := []struct {
		name        string
		pgType      typeinfo.Type
		notNull     bool
		defaultVal  *string
		defaultExpr string
		check       string
		generated   string
		identity    string
	}{
		{"id", typeinfo.T("uuid"), true, nil, "gen_random_uuid()", "", "", ""},
		{"ref", typeinfo.T("uuid"), true, nil, "", "", "", ""},
		{"timestamp", typeinfo.T("timestamptz"), true, nil, "now()", "", "", ""},
		{"timestamp_optional", typeinfo.T("timestamptz"), false, nil, "", "", "", ""},
		{"money", typeinfo.T("int8"), true, strPtr("0"), "", "", "", ""},
		{"slug", typeinfo.T("text"), true, nil, "", "VALUE ~ '^[a-z0-9-]+$'", "", ""},
		{"email", typeinfo.T("text"), true, nil, "", "VALUE ~ '^[^@]+@[^@]+\\.[^@]+$'", "", ""},
		{"short_text", typeinfo.T("text"), true, nil, "", "LENGTH(VALUE) <= 255", "", ""},
		{"json", typeinfo.T("jsonb"), true, nil, "'{}'::jsonb", "", "", ""},
		{"json_array", typeinfo.T("jsonb"), true, nil, "'[]'::jsonb", "", "", ""},
		{"counter", typeinfo.T("int8"), true, strPtr("0"), "", "", "", ""},
		{"flag", typeinfo.T("bool"), true, strPtr("false"), "", "", "", ""},
		{"auto_id", typeinfo.T("int8"), true, nil, "", "", "", "ALWAYS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			td, err := r.Resolve(tt.name)
			if err != nil {
				t.Fatalf("Resolve(%q) returned error: %v", tt.name, err)
			}
			if td.BaseType != tt.pgType {
				t.Errorf("BaseType = %v, want %v", td.BaseType, tt.pgType)
			}
			if td.NotNull != tt.notNull {
				t.Errorf("NotNull = %v, want %v", td.NotNull, tt.notNull)
			}
			if !strPtrEqual(td.Default, tt.defaultVal) {
				t.Errorf("Default = %v, want %v", td.Default, tt.defaultVal)
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
	td := &TypeDef{Name: "test", Kind: KindScalar, BaseType: typeinfo.T("text")}
	if err := r.Register(td); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}

	// Identical duplicate registration should succeed (idempotent).
	td2 := &TypeDef{Name: "test", Kind: KindScalar, BaseType: typeinfo.T("text")}
	if err := r.Register(td2); err != nil {
		t.Fatalf("identical duplicate should succeed, got: %v", err)
	}

	// Conflicting duplicate should fail.
	td3 := &TypeDef{Name: "test", Kind: KindScalar, BaseType: typeinfo.T("integer")}
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
	if td.BaseType != typeinfo.T("int4") {
		t.Errorf("BaseType = %v, want %v", td.BaseType, typeinfo.T("int4"))
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
	if rc.PGType != typeinfo.T("uuid") {
		t.Errorf("PGType = %v, want %v", rc.PGType, typeinfo.T("uuid"))
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
	if rc.Default == nil || *rc.Default != "100" {
		got := "<nil>"
		if rc.Default != nil {
			got = *rc.Default
		}
		t.Errorf("Default = %q, want %q", got, "100")
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
	if rc.Default != nil {
		t.Errorf("Default = %q, want nil (expr default takes precedence)", *rc.Default)
	}
}

func TestResolveColumnNoOverrides(t *testing.T) {
	r := NewBuiltinRegistry()

	rc, err := r.ResolveColumn("slug", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("ResolveColumn error: %v", err)
	}
	if rc.PGType != typeinfo.T("text") {
		t.Errorf("PGType = %v, want %v", rc.PGType, typeinfo.T("text"))
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
			Default: strPtr("created"),
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
	if td.Default == nil || *td.Default != "created" {
		got := "<nil>"
		if td.Default != nil {
			got = *td.Default
		}
		t.Errorf("Default = %q, want %q", got, "created")
	}
}

func TestLoadUserEnumType_InvalidDefault_E109(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name:    "status",
			Kind:    "enum",
			Values:  []string{"created", "running"},
			Default: strPtr("'created'"),
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

func TestLoadUserEnumType_EmbeddedQuotes_E110(t *testing.T) {
	r := NewBuiltinRegistry()
	userTypes := []UserTypeDef{
		{
			Name:    "status",
			Kind:    "enum",
			Values:  []string{"created", "running"},
			Default: strPtr("'created'"),
		},
	}
	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for embedded quotes in default, got none")
	}
	foundE110 := false
	foundE109 := false
	for _, d := range diags {
		if d.Code == "E110" {
			foundE110 = true
		}
		if d.Code == "E109" {
			foundE109 = true
		}
	}
	if !foundE110 {
		t.Error("expected E110 for embedded SQL quotes in default")
	}
	if !foundE109 {
		t.Error("expected E109 for invalid enum default (after stripping quotes, it matches, but the raw value doesn't)")
	}
}

func TestLoadUserScalarType_EmbeddedQuotes_E110(t *testing.T) {
	r := NewBuiltinRegistry()
	userTypes := []UserTypeDef{
		{
			Name:    "json_data",
			Kind:    "scalar",
			Base:    "jsonb",
			Default: strPtr("'{}'"),
		},
	}
	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for embedded quotes in default, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E110" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected E110 for embedded SQL quotes in scalar default")
	}
}

func TestLoadUserScalarType_NoQuotes_NoE110(t *testing.T) {
	r := NewBuiltinRegistry()
	userTypes := []UserTypeDef{
		{
			Name:    "json_data",
			Kind:    "scalar",
			Base:    "jsonb",
			Default: strPtr("{}"),
		},
	}
	diags := r.LoadUserTypes(userTypes)
	for _, d := range diags {
		if d.Code == "E110" {
			t.Errorf("unexpected E110 for default without quotes: %s", d.Message)
		}
	}
}

func TestLoadUserScalarType_NumericDefault_NoE110(t *testing.T) {
	r := NewBuiltinRegistry()
	userTypes := []UserTypeDef{
		{
			Name:    "counter",
			Kind:    "scalar",
			Base:    "integer",
			Default: strPtr("0"),
		},
	}
	diags := r.LoadUserTypes(userTypes)
	for _, d := range diags {
		if d.Code == "E110" {
			t.Errorf("unexpected E110 for numeric default: %s", d.Message)
		}
	}
}

func TestExtensionType_Accepted(t *testing.T) {
	r := NewBuiltinRegistry()
	r.AddExtensionTypes([]string{"vector"})

	userTypes := []UserTypeDef{
		{
			Name: "embedding",
			Kind: "scalar",
			Base: "vector(384)",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("expected no errors with extension type, got: %v", diags)
	}

	td, err := r.Resolve("embedding")
	if err != nil {
		t.Fatalf("Resolve(embedding) error: %v", err)
	}
	if td.BaseType != typeinfo.MustParse("vector(384)") {
		t.Errorf("BaseType = %v, want %v", td.BaseType, typeinfo.MustParse("vector(384)"))
	}
}

func TestExtensionType_MissingExtension_E106(t *testing.T) {
	r := NewBuiltinRegistry()
	// No AddExtensionTypes call -- "vector" is not registered.

	userTypes := []UserTypeDef{
		{
			Name: "embedding",
			Kind: "scalar",
			Base: "vector(384)",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected E106 for unregistered extension type, got none")
	}

	found := false
	for _, d := range diags {
		if d.Code == "E106" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E106 for unregistered extension type")
	}
}

func TestExtensionType_UnknownStillRejected(t *testing.T) {
	r := NewBuiltinRegistry()
	r.AddExtensionTypes([]string{"vector"})

	userTypes := []UserTypeDef{
		{
			Name: "bad",
			Kind: "scalar",
			Base: "unknown",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected E106 for truly unknown type, got none")
	}

	found := false
	for _, d := range diags {
		if d.Code == "E106" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E106 for truly unknown base type")
	}
}

func TestLoadUserScalarType_RangeTypes(t *testing.T) {
	rangeTypes := []string{
		"int4range", "int8range", "numrange", "tsrange", "tstzrange", "daterange",
		"int4multirange", "int8multirange", "nummultirange", "tsmultirange", "tstzmultirange", "datemultirange",
	}

	for _, rt := range rangeTypes {
		t.Run(rt, func(t *testing.T) {
			r := NewBuiltinRegistry()
			userTypes := []UserTypeDef{
				{
					Name: "my_" + rt,
					Kind: "scalar",
					Base: rt,
				},
			}
			diags := r.LoadUserTypes(userTypes)
			if diags.HasErrors() {
				t.Fatalf("expected no errors for range type %q, got: %v", rt, diags)
			}
			td, err := r.Resolve("my_" + rt)
			if err != nil {
				t.Fatalf("Resolve(my_%s) error: %v", rt, err)
			}
			if td.BaseType != typeinfo.T(rt) {
				t.Errorf("BaseType = %v, want %v", td.BaseType, typeinfo.T(rt))
			}
		})
	}
}

func TestLoadUserCompositeType(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "address",
			Kind: "composite",
			Fields: map[string]string{
				"street": "text",
				"city":   "text",
				"zip":    "varchar(10)",
			},
			Comment: "Mailing address",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	td, err := r.Resolve("address")
	if err != nil {
		t.Fatalf("Resolve(address) error: %v", err)
	}
	if td.Kind != KindComposite {
		t.Errorf("Kind = %v, want KindComposite", td.Kind)
	}
	if td.BaseType != typeinfo.T("address") {
		t.Errorf("BaseType = %v, want %v", td.BaseType, typeinfo.T("address"))
	}
	if td.Comment != "Mailing address" {
		t.Errorf("Comment = %q, want %q", td.Comment, "Mailing address")
	}
	if len(td.Fields) != 3 {
		t.Fatalf("Fields length = %d, want 3", len(td.Fields))
	}
	// Fields should be sorted by name: city, street, zip
	expected := []CompositeField{
		{Name: "city", PGType: "text"},
		{Name: "street", PGType: "text"},
		{Name: "zip", PGType: "varchar(10)"},
	}
	for i, f := range td.Fields {
		if f != expected[i] {
			t.Errorf("Fields[%d] = %+v, want %+v", i, f, expected[i])
		}
	}
}

func TestLoadUserCompositeType_NoFields(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name:   "empty_composite",
			Kind:   "composite",
			Fields: map[string]string{},
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for composite with no fields, got none")
	}

	found := false
	for _, d := range diags {
		if d.Code == "E103" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E103 for composite with no fields")
	}
}

func TestLoadUserCompositeType_NilFields(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "nil_composite",
			Kind: "composite",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for composite with nil fields, got none")
	}

	found := false
	for _, d := range diags {
		if d.Code == "E103" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E103 for composite with nil fields")
	}
}

func TestLoadUserCompositeType_InvalidFieldType(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "bad_composite",
			Kind: "composite",
			Fields: map[string]string{
				"good_field": "text",
				"bad_field":  "not_a_pg_type",
			},
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for composite with invalid field type, got none")
	}

	found := false
	for _, d := range diags {
		if d.Code == "E103" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E103 for invalid field type")
	}
}

func TestCompositeTypeDefsEqual(t *testing.T) {
	a := &TypeDef{
		Name:     "address",
		Kind:     KindComposite,
		BaseType: typeinfo.T("address"),
		Fields: []CompositeField{
			{Name: "city", PGType: "text"},
			{Name: "street", PGType: "text"},
		},
	}
	b := &TypeDef{
		Name:     "address",
		Kind:     KindComposite,
		BaseType: typeinfo.T("address"),
		Fields: []CompositeField{
			{Name: "city", PGType: "text"},
			{Name: "street", PGType: "text"},
		},
	}
	if !typeDefsEqual(a, b) {
		t.Error("typeDefsEqual returned false for identical composite types")
	}

	// Different field count
	c := &TypeDef{
		Name:     "address",
		Kind:     KindComposite,
		BaseType: typeinfo.T("address"),
		Fields: []CompositeField{
			{Name: "city", PGType: "text"},
		},
	}
	if typeDefsEqual(a, c) {
		t.Error("typeDefsEqual returned true for composite types with different field counts")
	}

	// Different field type
	d := &TypeDef{
		Name:     "address",
		Kind:     KindComposite,
		BaseType: typeinfo.T("address"),
		Fields: []CompositeField{
			{Name: "city", PGType: "varchar"},
			{Name: "street", PGType: "text"},
		},
	}
	if typeDefsEqual(a, d) {
		t.Error("typeDefsEqual returned true for composite types with different field types")
	}

	// Different field name
	e := &TypeDef{
		Name:     "address",
		Kind:     KindComposite,
		BaseType: typeinfo.T("address"),
		Fields: []CompositeField{
			{Name: "town", PGType: "text"},
			{Name: "street", PGType: "text"},
		},
	}
	if typeDefsEqual(a, e) {
		t.Error("typeDefsEqual returned true for composite types with different field names")
	}
}

func TestLoadStateMachineType(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "order_status",
			Kind: "state_machine",
			States: []UserSMState{
				{Name: "pending", Comment: "Order created"},
				{Name: "processing"},
				{Name: "shipped"},
				{Name: "delivered", Terminal: true},
				{Name: "cancelled", Terminal: true},
			},
			Transitions: []UserSMTransition{
				{Name: "start_processing", From: []string{"pending"}, To: "processing"},
				{Name: "ship", From: []string{"processing"}, To: "shipped"},
				{Name: "deliver", From: []string{"shipped"}, To: "delivered"},
				{Name: "cancel", From: []string{"pending", "processing"}, To: "cancelled",
					Requires: map[string]string{"reason": "text"}, Comment: "Cancel with reason"},
			},
			InitialState:   "pending",
			EnforceTrigger: true,
			Comment:        "Order lifecycle",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	td, err := r.Resolve("order_status")
	if err != nil {
		t.Fatalf("Resolve(order_status) error: %v", err)
	}
	if td.Kind != KindStateMachine {
		t.Errorf("Kind = %v, want KindStateMachine", td.Kind)
	}
	if td.BaseType != typeinfo.T("order_status") {
		t.Errorf("BaseType = %v, want %v", td.BaseType, typeinfo.T("order_status"))
	}
	if td.InitialState != "pending" {
		t.Errorf("InitialState = %q, want %q", td.InitialState, "pending")
	}
	if !td.EnforceTrigger {
		t.Error("EnforceTrigger = false, want true")
	}
	if td.Comment != "Order lifecycle" {
		t.Errorf("Comment = %q, want %q", td.Comment, "Order lifecycle")
	}

	// EnumValues should be populated from state names.
	expectedValues := []string{"pending", "processing", "shipped", "delivered", "cancelled"}
	if len(td.EnumValues) != len(expectedValues) {
		t.Fatalf("EnumValues length = %d, want %d", len(td.EnumValues), len(expectedValues))
	}
	for i, v := range expectedValues {
		if td.EnumValues[i] != v {
			t.Errorf("EnumValues[%d] = %q, want %q", i, td.EnumValues[i], v)
		}
	}

	// States should be populated.
	if len(td.States) != 5 {
		t.Fatalf("States length = %d, want 5", len(td.States))
	}
	if td.States[0].Name != "pending" || td.States[0].Comment != "Order created" {
		t.Errorf("States[0] = %+v, want name=pending comment=Order created", td.States[0])
	}
	if !td.States[3].Terminal {
		t.Error("States[3] (delivered) Terminal = false, want true")
	}

	// Transitions should be populated.
	if len(td.Transitions) != 4 {
		t.Fatalf("Transitions length = %d, want 4", len(td.Transitions))
	}
	cancel := td.Transitions[3]
	if cancel.Name != "cancel" {
		t.Errorf("Transitions[3].Name = %q, want %q", cancel.Name, "cancel")
	}
	if len(cancel.From) != 2 {
		t.Fatalf("Transitions[3].From length = %d, want 2", len(cancel.From))
	}
	if cancel.To != "cancelled" {
		t.Errorf("Transitions[3].To = %q, want %q", cancel.To, "cancelled")
	}
	if cancel.Requires["reason"] != "text" {
		t.Errorf("Transitions[3].Requires[reason] = %q, want %q", cancel.Requires["reason"], "text")
	}
	if cancel.Comment != "Cancel with reason" {
		t.Errorf("Transitions[3].Comment = %q, want %q", cancel.Comment, "Cancel with reason")
	}

	// NotNull should default to true.
	if !td.NotNull {
		t.Error("NotNull = false, want true (default)")
	}
}

func TestLoadStateMachineType_MissingInitialState(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "bad_sm",
			Kind: "state_machine",
			States: []UserSMState{
				{Name: "a"},
				{Name: "b"},
			},
			// InitialState not set
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for missing initial state, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E112" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E112 for missing initial state")
	}
}

func TestLoadStateMachineType_InvalidInitialState(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "bad_sm",
			Kind: "state_machine",
			States: []UserSMState{
				{Name: "a"},
				{Name: "b"},
			},
			InitialState: "nonexistent",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for invalid initial state, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E112" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E112 for invalid initial state")
	}
}

func TestLoadStateMachineType_InvalidTransitionTarget(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "bad_sm",
			Kind: "state_machine",
			States: []UserSMState{
				{Name: "a"},
				{Name: "b"},
			},
			Transitions: []UserSMTransition{
				{Name: "go", From: []string{"a"}, To: "nonexistent"},
			},
			InitialState: "a",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for invalid transition target, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E113" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E113 for invalid transition target")
	}
}

func TestLoadStateMachineType_InvalidTransitionFrom(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "bad_sm",
			Kind: "state_machine",
			States: []UserSMState{
				{Name: "a"},
				{Name: "b"},
			},
			Transitions: []UserSMTransition{
				{Name: "go", From: []string{"nonexistent"}, To: "b"},
			},
			InitialState: "a",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for invalid transition from-state, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E113" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E113 for invalid transition from-state")
	}
}

func TestLoadStateMachineType_DuplicateStates(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "bad_sm",
			Kind: "state_machine",
			States: []UserSMState{
				{Name: "a"},
				{Name: "b"},
				{Name: "a"}, // duplicate
			},
			InitialState: "a",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for duplicate state names, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E111" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E111 for duplicate state names")
	}
}

func TestLoadStateMachineType_EmptyStates(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name:         "bad_sm",
			Kind:         "state_machine",
			InitialState: "a",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for empty states, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E111" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E111 for empty states")
	}
}

func TestStateMachineTypeDefsEqual(t *testing.T) {
	a := &TypeDef{
		Name:         "order_status",
		Kind:         KindStateMachine,
		BaseType:     typeinfo.T("order_status"),
		InitialState: "pending",
		States: []SMStateDef{
			{Name: "pending"},
			{Name: "done", Terminal: true},
		},
		Transitions: []SMTransitionDef{
			{Name: "finish", From: []string{"pending"}, To: "done",
				Requires: map[string]string{"note": "text"}},
		},
	}
	b := &TypeDef{
		Name:         "order_status",
		Kind:         KindStateMachine,
		BaseType:     typeinfo.T("order_status"),
		InitialState: "pending",
		States: []SMStateDef{
			{Name: "pending"},
			{Name: "done", Terminal: true},
		},
		Transitions: []SMTransitionDef{
			{Name: "finish", From: []string{"pending"}, To: "done",
				Requires: map[string]string{"note": "text"}},
		},
	}
	if !typeDefsEqual(a, b) {
		t.Error("typeDefsEqual returned false for identical state machine types")
	}

	// Different initial state
	c := &TypeDef{
		Name:         "order_status",
		Kind:         KindStateMachine,
		BaseType:     typeinfo.T("order_status"),
		InitialState: "done",
		States:       a.States,
		Transitions:  a.Transitions,
	}
	if typeDefsEqual(a, c) {
		t.Error("typeDefsEqual returned true for state machine types with different initial state")
	}

	// Different enforce trigger
	d := &TypeDef{
		Name:           "order_status",
		Kind:           KindStateMachine,
		BaseType:       typeinfo.T("order_status"),
		InitialState:   "pending",
		EnforceTrigger: true,
		States:         a.States,
		Transitions:    a.Transitions,
	}
	if typeDefsEqual(a, d) {
		t.Error("typeDefsEqual returned true for state machine types with different enforce trigger")
	}

	// Different number of states
	e := &TypeDef{
		Name:         "order_status",
		Kind:         KindStateMachine,
		BaseType:     typeinfo.T("order_status"),
		InitialState: "pending",
		States: []SMStateDef{
			{Name: "pending"},
		},
		Transitions: a.Transitions,
	}
	if typeDefsEqual(a, e) {
		t.Error("typeDefsEqual returned true for state machine types with different state count")
	}
}

func TestLoadStateMachineType_TransitionMissingName(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name: "bad_sm",
			Kind: "state_machine",
			States: []UserSMState{
				{Name: "a"},
				{Name: "b"},
			},
			Transitions: []UserSMTransition{
				{Name: "", From: []string{"a"}, To: "b"},
			},
			InitialState: "a",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for transition missing name, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E113" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic code E113 for transition missing name")
	}
}

func TestShadowBuiltin_Success(t *testing.T) {
	r := NewBuiltinRegistry()

	// The builtin "id" is KindScalar with BaseType "uuid" and DefaultExpr "gen_random_uuid()".
	// Shadow it with same Kind and BaseType but a different Check constraint.
	userTypes := []UserTypeDef{
		{
			Name:  "id",
			Kind:  "scalar",
			Base:  "uuid",
			Check: "VALUE IS NOT NULL",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	// I101 should be emitted via ShadowDiags.
	shadowDiags := r.ShadowDiags()
	foundI101 := false
	for _, d := range shadowDiags {
		if d.Code == "I101" {
			foundI101 = true
			break
		}
	}
	if !foundI101 {
		t.Error("expected I101 diagnostic for builtin shadowing, got none")
	}

	// Resolve should return the user's version.
	td, err := r.Resolve("id")
	if err != nil {
		t.Fatalf("Resolve(id) error: %v", err)
	}
	if td.Source != "user" {
		t.Errorf("Source = %q, want %q", td.Source, "user")
	}
	if td.Check != "VALUE IS NOT NULL" {
		t.Errorf("Check = %q, want %q", td.Check, "VALUE IS NOT NULL")
	}
	// DefaultExpr should be gone (user version doesn't set it).
	if td.DefaultExpr != "" {
		t.Errorf("DefaultExpr = %q, want empty (user version)", td.DefaultExpr)
	}
}

func TestShadowBuiltin_SealedViolation_Kind(t *testing.T) {
	r := NewBuiltinRegistry()

	// Builtin "id" is KindScalar. Try to shadow with KindEnum.
	userTypes := []UserTypeDef{
		{
			Name:   "id",
			Kind:   "enum",
			Values: []string{"a", "b"},
		},
	}

	diags := r.LoadUserTypes(userTypes)
	// The loadEnumType path won't produce E114 directly; it comes from Register
	// via ShadowDiags. Check both.
	shadowDiags := r.ShadowDiags()
	allDiags := append(diags, shadowDiags...)

	foundE114 := false
	for _, d := range allDiags {
		if d.Code == "E114" {
			foundE114 = true
			break
		}
	}
	if !foundE114 {
		t.Error("expected E114 for sealed field Kind violation, got none")
	}
}

func TestShadowBuiltin_SealedViolation_BaseType(t *testing.T) {
	r := NewBuiltinRegistry()

	// Builtin "id" has BaseType "uuid". Try to shadow with BaseType "text".
	userTypes := []UserTypeDef{
		{
			Name: "id",
			Kind: "scalar",
			Base: "text",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	shadowDiags := r.ShadowDiags()
	allDiags := append(diags, shadowDiags...)

	foundE114 := false
	for _, d := range allDiags {
		if d.Code == "E114" {
			foundE114 = true
			break
		}
	}
	if !foundE114 {
		t.Error("expected E114 for sealed field BaseType violation, got none")
	}
}

func TestIsBuiltin(t *testing.T) {
	r := NewBuiltinRegistry()

	// Before shadowing: "id" is builtin.
	if !r.IsBuiltin("id") {
		t.Error("IsBuiltin(id) = false before shadowing, want true")
	}

	// Non-existent type is not builtin.
	if r.IsBuiltin("nonexistent") {
		t.Error("IsBuiltin(nonexistent) = true, want false")
	}

	// Shadow the builtin.
	userTypes := []UserTypeDef{
		{
			Name:  "id",
			Kind:  "scalar",
			Base:  "uuid",
			Check: "VALUE IS NOT NULL",
		},
	}
	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	// After shadowing: "id" is no longer builtin.
	if r.IsBuiltin("id") {
		t.Error("IsBuiltin(id) = true after shadowing, want false")
	}
}

func TestIdempotentRegistration(t *testing.T) {
	r := NewRegistry()
	td1 := &TypeDef{Name: "test", Kind: KindScalar, BaseType: typeinfo.T("text"), Source: "user"}
	if err := r.Register(td1); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}

	// Identical registration should succeed silently.
	td2 := &TypeDef{Name: "test", Kind: KindScalar, BaseType: typeinfo.T("text"), Source: "user"}
	if err := r.Register(td2); err != nil {
		t.Fatalf("identical duplicate should succeed, got: %v", err)
	}

	// No shadow diagnostics should be emitted.
	if len(r.ShadowDiags()) != 0 {
		t.Errorf("expected no shadow diagnostics for idempotent registration, got %d", len(r.ShadowDiags()))
	}
}

func TestExtendsScalarBuiltin(t *testing.T) {
	r := NewBuiltinRegistry()

	// Extend builtin "id" (uuid, NOT NULL, DefaultExpr gen_random_uuid())
	// with a custom DefaultExpr.
	userTypes := []UserTypeDef{
		{
			Name:        "custom_id",
			Extends:     "id",
			DefaultExpr: "uuid_generate_v7()",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	td, err := r.Resolve("custom_id")
	if err != nil {
		t.Fatalf("Resolve(custom_id) error: %v", err)
	}

	// Kind and BaseType inherited from builtin "id".
	if td.Kind != KindScalar {
		t.Errorf("Kind = %v, want KindScalar", td.Kind)
	}
	if td.BaseType != typeinfo.T("uuid") {
		t.Errorf("BaseType = %v, want %v", td.BaseType, typeinfo.T("uuid"))
	}

	// DefaultExpr overridden by child.
	if td.DefaultExpr != "uuid_generate_v7()" {
		t.Errorf("DefaultExpr = %q, want %q", td.DefaultExpr, "uuid_generate_v7()")
	}

	// Source should be "extended".
	if td.Source != "extended" {
		t.Errorf("Source = %q, want %q", td.Source, "extended")
	}

	// NotNull inherited from parent.
	if !td.NotNull {
		t.Error("NotNull = false, want true (inherited from parent)")
	}

	// I101 should be emitted for shadowing builtin "id" (the child name is
	// "custom_id" which is not "id", so no I101 expected here). Verify that
	// the original "id" is untouched.
	origID, err := r.Resolve("id")
	if err != nil {
		t.Fatalf("Resolve(id) error: %v", err)
	}
	if origID.DefaultExpr != "gen_random_uuid()" {
		t.Errorf("original id DefaultExpr = %q, want %q", origID.DefaultExpr, "gen_random_uuid()")
	}
}

func TestExtendsScalarUserType(t *testing.T) {
	r := NewBuiltinRegistry()

	// Type A is a user scalar, then B extends A.
	userTypes := []UserTypeDef{
		{
			Name:  "positive_int",
			Kind:  "scalar",
			Base:  "integer",
			Check: "VALUE > 0",
		},
		{
			Name:        "even_positive",
			Extends:     "positive_int",
			Check:       "VALUE > 0 AND VALUE % 2 = 0",
			DefaultExpr: "2",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	td, err := r.Resolve("even_positive")
	if err != nil {
		t.Fatalf("Resolve(even_positive) error: %v", err)
	}

	// BaseType inherited from positive_int.
	if td.BaseType != typeinfo.T("int4") {
		t.Errorf("BaseType = %v, want %v", td.BaseType, typeinfo.T("int4"))
	}

	// Check overridden by child.
	if td.Check != "VALUE > 0 AND VALUE % 2 = 0" {
		t.Errorf("Check = %q, want %q", td.Check, "VALUE > 0 AND VALUE % 2 = 0")
	}

	// DefaultExpr set by child.
	if td.DefaultExpr != "2" {
		t.Errorf("DefaultExpr = %q, want %q", td.DefaultExpr, "2")
	}

	if td.Source != "extended" {
		t.Errorf("Source = %q, want %q", td.Source, "extended")
	}
}

func TestExtendsSelfShadowing(t *testing.T) {
	r := NewBuiltinRegistry()

	// [types.id] extends = "id" — same name. Should resolve against the
	// builtin "id", not itself.
	userTypes := []UserTypeDef{
		{
			Name:        "id",
			Extends:     "id",
			DefaultExpr: "uuid_generate_v7()",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	td, err := r.Resolve("id")
	if err != nil {
		t.Fatalf("Resolve(id) error: %v", err)
	}

	// Should have builtin's Kind and BaseType + child's DefaultExpr.
	if td.Kind != KindScalar {
		t.Errorf("Kind = %v, want KindScalar", td.Kind)
	}
	if td.BaseType != typeinfo.T("uuid") {
		t.Errorf("BaseType = %v, want %v", td.BaseType, typeinfo.T("uuid"))
	}
	if td.DefaultExpr != "uuid_generate_v7()" {
		t.Errorf("DefaultExpr = %q, want %q", td.DefaultExpr, "uuid_generate_v7()")
	}
	if td.Source != "extended" {
		t.Errorf("Source = %q, want %q", td.Source, "extended")
	}

	// I101 should be emitted via ShadowDiags for shadowing the builtin.
	shadowDiags := r.ShadowDiags()
	foundI101 := false
	for _, d := range shadowDiags {
		if d.Code == "I101" {
			foundI101 = true
			break
		}
	}
	if !foundI101 {
		t.Error("expected I101 diagnostic for self-shadowing builtin, got none")
	}
}

func TestExtendsCircular(t *testing.T) {
	r := NewBuiltinRegistry()

	// A extends B, B extends A — circular.
	userTypes := []UserTypeDef{
		{
			Name:    "type_a",
			Extends: "type_b",
		},
		{
			Name:    "type_b",
			Extends: "type_a",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for circular extends, got none")
	}

	foundE115 := false
	for _, d := range diags {
		if d.Code == "E115" {
			foundE115 = true
			break
		}
	}
	if !foundE115 {
		t.Error("expected diagnostic code E115 for circular extends")
	}
}

func TestExtendsUnknownTarget(t *testing.T) {
	r := NewBuiltinRegistry()

	userTypes := []UserTypeDef{
		{
			Name:    "orphan",
			Extends: "nonexistent",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for unknown extends target, got none")
	}

	foundE116 := false
	for _, d := range diags {
		if d.Code == "E116" {
			foundE116 = true
			break
		}
	}
	if !foundE116 {
		t.Error("expected diagnostic code E116 for unknown extends target")
	}
}

func TestExtendsMultiLevel(t *testing.T) {
	r := NewBuiltinRegistry()

	// C extends B extends A. Verify C has A's fields + B's overrides + C's overrides.
	userTypes := []UserTypeDef{
		{
			Name:    "base_text",
			Kind:    "scalar",
			Base:    "text",
			Check:   "VALUE IS NOT NULL",
			Comment: "base comment",
		},
		{
			Name:    "short_base",
			Extends: "base_text",
			Check:   "LENGTH(VALUE) <= 100",
			Comment: "short comment",
		},
		{
			Name:    "slug_short",
			Extends: "short_base",
			Check:   "VALUE ~ '^[a-z0-9-]+$' AND LENGTH(VALUE) <= 100",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}

	// Verify base_text.
	a, err := r.Resolve("base_text")
	if err != nil {
		t.Fatalf("Resolve(base_text) error: %v", err)
	}
	if a.Check != "VALUE IS NOT NULL" {
		t.Errorf("base_text.Check = %q, want %q", a.Check, "VALUE IS NOT NULL")
	}

	// Verify short_base inherits from base_text.
	b, err := r.Resolve("short_base")
	if err != nil {
		t.Fatalf("Resolve(short_base) error: %v", err)
	}
	if b.BaseType != typeinfo.T("text") {
		t.Errorf("short_base.BaseType = %v, want %v", b.BaseType, typeinfo.T("text"))
	}
	if b.Check != "LENGTH(VALUE) <= 100" {
		t.Errorf("short_base.Check = %q, want %q", b.Check, "LENGTH(VALUE) <= 100")
	}
	if b.Comment != "short comment" {
		t.Errorf("short_base.Comment = %q, want %q", b.Comment, "short comment")
	}

	// Verify slug_short inherits from short_base.
	c, err := r.Resolve("slug_short")
	if err != nil {
		t.Fatalf("Resolve(slug_short) error: %v", err)
	}
	if c.BaseType != typeinfo.T("text") {
		t.Errorf("slug_short.BaseType = %v, want %v", c.BaseType, typeinfo.T("text"))
	}
	if c.Check != "VALUE ~ '^[a-z0-9-]+$' AND LENGTH(VALUE) <= 100" {
		t.Errorf("slug_short.Check = %q, want %q", c.Check, "VALUE ~ '^[a-z0-9-]+$' AND LENGTH(VALUE) <= 100")
	}
	// Comment inherited from short_base (child doesn't set it).
	if c.Comment != "short comment" {
		t.Errorf("slug_short.Comment = %q, want %q (inherited from short_base)", c.Comment, "short comment")
	}
	if c.Source != "extended" {
		t.Errorf("slug_short.Source = %q, want %q", c.Source, "extended")
	}
}

func TestExtendsSealedFieldViolation(t *testing.T) {
	r := NewBuiltinRegistry()

	// Extend builtin "id" (BaseType uuid) but try to set a different BaseType.
	userTypes := []UserTypeDef{
		{
			Name:    "bad_id",
			Extends: "id",
			Base:    "text",
		},
	}

	diags := r.LoadUserTypes(userTypes)
	if !diags.HasErrors() {
		t.Fatal("expected errors for sealed field violation, got none")
	}

	foundE114 := false
	for _, d := range diags {
		if d.Code == "E114" {
			foundE114 = true
			break
		}
	}
	if !foundE114 {
		t.Error("expected diagnostic code E114 for sealed field BaseType violation")
	}
}
