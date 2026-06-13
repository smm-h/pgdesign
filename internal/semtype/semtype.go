// Package semtype implements the semantic type system for pgdesign.
// It maps type names to PostgreSQL types with enforced attributes.
package semtype

import (
	"fmt"
	"strings"
	"sync"

	"github.com/smm-h/pgdesign/internal/diagnostic"
)

// Kind represents the kind of a type definition.
type Kind int

const (
	KindScalar    Kind = iota
	KindEnum      Kind = iota
	KindComposite Kind = iota
)

func (k Kind) String() string {
	switch k {
	case KindScalar:
		return "scalar"
	case KindEnum:
		return "enum"
	case KindComposite:
		return "composite"
	default:
		return "unknown"
	}
}

// TypeDef defines a semantic type with its PostgreSQL mapping and constraints.
type TypeDef struct {
	Name        string
	Kind        Kind
	BaseType    string   // PG type (e.g., "uuid", "text", "bigint")
	NotNull     bool
	Default     string   // literal default value
	DefaultExpr string   // SQL expression default (e.g., "gen_random_uuid()")
	Check       string   // check constraint expression (VALUE placeholder)
	Unique      bool
	Comment     string
	EnumValues  []string // values for enum types
	Generated   string   // generated expression (e.g., "price * 0.2")
	Stored      bool     // whether generated column is stored
	Identity    string   // identity generation: "ALWAYS" or "BY DEFAULT"
}

// Registry holds named TypeDefs with thread-safe read access.
type Registry struct {
	mu    sync.RWMutex
	types map[string]*TypeDef
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		types: make(map[string]*TypeDef),
	}
}

// Register adds a type definition to the registry.
// If a type with the same name already exists with an identical definition,
// the registration is silently accepted (idempotent for multi-file schemas).
// Returns an error if a type with the same name exists with a different definition.
func (r *Registry) Register(td *TypeDef) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, exists := r.types[td.Name]; exists {
		if typeDefsEqual(existing, td) {
			return nil
		}
		return fmt.Errorf("type %q already registered with a different definition", td.Name)
	}
	r.types[td.Name] = td
	return nil
}

// typeDefsEqual returns true if two TypeDefs have equivalent definitions.
func typeDefsEqual(a, b *TypeDef) bool {
	if a.Kind != b.Kind || a.BaseType != b.BaseType || a.NotNull != b.NotNull {
		return false
	}
	if a.Default != b.Default || a.DefaultExpr != b.DefaultExpr {
		return false
	}
	if a.Check != b.Check || a.Unique != b.Unique {
		return false
	}
	if len(a.EnumValues) != len(b.EnumValues) {
		return false
	}
	for i := range a.EnumValues {
		if a.EnumValues[i] != b.EnumValues[i] {
			return false
		}
	}
	if a.Generated != b.Generated || a.Stored != b.Stored {
		return false
	}
	if a.Identity != b.Identity {
		return false
	}
	return true
}

// Resolve looks up a type by name.
// Returns an error if the type is not found.
func (r *Registry) Resolve(name string) (*TypeDef, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	td, ok := r.types[name]
	if !ok {
		return nil, fmt.Errorf("unknown type %q", name)
	}
	return td, nil
}

// UserTypeDef represents a user-defined type loaded from configuration.
type UserTypeDef struct {
	Name        string
	Kind        string // "scalar", "enum", "composite"
	Base        string // PG base type (for scalars)
	Values      []string // enum values
	NotNull     *bool
	Default     string
	DefaultExpr string
	Check       string
	Unique      bool
	Comment     string
}

// pgTypeAllowlist contains valid PostgreSQL base types for user-defined scalars.
var pgTypeAllowlist = map[string]bool{
	"bigint":      true,
	"boolean":     true,
	"bytea":       true,
	"char":        true,
	"citext":      true,
	"date":        true,
	"float4":      true,
	"float8":      true,
	"inet":        true,
	"integer":     true,
	"interval":    true,
	"json":        true,
	"jsonb":       true,
	"macaddr":     true,
	"numeric":     true,
	"oid":         true,
	"real":        true,
	"serial":      true,
	"bigserial":   true,
	"smallint":    true,
	"smallserial": true,
	"text":        true,
	"time":        true,
	"timetz":      true,
	"timestamp":   true,
	"timestamptz": true,
	"tsquery":     true,
	"tsvector":    true,
	"uuid":        true,
	"varchar":     true,
	"xml":         true,
}

// LoadUserTypes validates and registers user-defined types into the registry.
// Returns diagnostics for any validation failures.
func (r *Registry) LoadUserTypes(types []UserTypeDef) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	for _, ut := range types {
		if ut.Name == "" {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E100",
				Message:  "user-defined type has empty name",
			})
			continue
		}

		kind := parseKind(ut.Kind)

		switch kind {
		case KindEnum:
			diags = append(diags, r.loadEnumType(ut)...)
		case KindScalar:
			diags = append(diags, r.loadScalarType(ut)...)
		case KindComposite:
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E103",
				Message:  fmt.Sprintf("type %q: composite types are not yet supported", ut.Name),
			})
		default:
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E104",
				Message:  fmt.Sprintf("type %q: unknown kind %q", ut.Name, ut.Kind),
			})
		}
	}

	return diags
}

func (r *Registry) loadEnumType(ut UserTypeDef) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	if len(ut.Values) == 0 {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E101",
			Message:  fmt.Sprintf("enum type %q must have at least one value", ut.Name),
		})
		return diags
	}

	td := &TypeDef{
		Name:       ut.Name,
		Kind:       KindEnum,
		BaseType:   ut.Name, // enums use their own name as PG type
		NotNull:    true,
		EnumValues: ut.Values,
		Comment:    ut.Comment,
	}

	if ut.NotNull != nil {
		td.NotNull = *ut.NotNull
	}
	if ut.Default != "" {
		found := false
		for _, v := range ut.Values {
			if v == ut.Default {
				found = true
				break
			}
		}
		if !found {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E109",
				Message:  fmt.Sprintf("enum type %q default %q is not a declared value (valid: %s)", ut.Name, ut.Default, strings.Join(ut.Values, ", ")),
			})
			return diags
		}
		td.Default = ut.Default
	}

	if err := r.Register(td); err != nil {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E105",
			Message:  fmt.Sprintf("type %q: %s", ut.Name, err.Error()),
		})
	}

	return diags
}

func (r *Registry) loadScalarType(ut UserTypeDef) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	if ut.Base == "" {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E102",
			Message:  fmt.Sprintf("scalar type %q must have a base PG type", ut.Name),
		})
		return diags
	}

	// Check against allowlist (strip length/precision modifiers for lookup)
	baseForCheck := strings.ToLower(ut.Base)
	if idx := strings.IndexByte(baseForCheck, '('); idx != -1 {
		baseForCheck = baseForCheck[:idx]
	}
	if !pgTypeAllowlist[baseForCheck] {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E106",
			Message:  fmt.Sprintf("scalar type %q: unknown base type %q", ut.Name, ut.Base),
			Suggestion: "base must be a valid PostgreSQL type",
		})
		return diags
	}

	// Check that base is not another user type (no circular refs)
	r.mu.RLock()
	_, isUserType := r.types[ut.Base]
	r.mu.RUnlock()
	if isUserType {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E107",
			Message:  fmt.Sprintf("scalar type %q: base type %q references another user type (circular references not allowed)", ut.Name, ut.Base),
		})
		return diags
	}

	// Validate check expression contains VALUE placeholder if present
	if ut.Check != "" && !strings.Contains(ut.Check, "VALUE") {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E108",
			Message:  fmt.Sprintf("scalar type %q: check expression must contain VALUE placeholder", ut.Name),
		})
		return diags
	}

	td := &TypeDef{
		Name:        ut.Name,
		Kind:        KindScalar,
		BaseType:    ut.Base,
		NotNull:     true,
		Default:     ut.Default,
		DefaultExpr: ut.DefaultExpr,
		Check:       ut.Check,
		Unique:      ut.Unique,
		Comment:     ut.Comment,
	}

	if ut.NotNull != nil {
		td.NotNull = *ut.NotNull
	}

	if err := r.Register(td); err != nil {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E105",
			Message:  fmt.Sprintf("type %q: %s", ut.Name, err.Error()),
		})
	}

	return diags
}

func parseKind(s string) Kind {
	switch strings.ToLower(s) {
	case "enum":
		return KindEnum
	case "composite":
		return KindComposite
	default:
		// Default to scalar for empty or "scalar" kind
		return KindScalar
	}
}

// ResolvedColumn represents the final resolved attributes for a column after
// applying type defaults and column-level overrides.
type ResolvedColumn struct {
	PGType      string
	NotNull     bool
	Default     string
	DefaultExpr string
	Check       string
	Generated   string
	Stored      bool
	Identity    string
}

// ResolveColumn resolves a column's final attributes by looking up the type
// and applying column-level overrides.
func (r *Registry) ResolveColumn(typeName string, nullable *bool, defaultOverride *string, defaultExprOverride *string) (*ResolvedColumn, error) {
	td, err := r.Resolve(typeName)
	if err != nil {
		return nil, err
	}

	rc := &ResolvedColumn{
		PGType:      td.BaseType,
		NotNull:     td.NotNull,
		Default:     td.Default,
		DefaultExpr: td.DefaultExpr,
		Check:       td.Check,
		Generated:   td.Generated,
		Stored:      td.Stored,
		Identity:    td.Identity,
	}

	// Column nullable overrides type NotNull
	if nullable != nil {
		rc.NotNull = !*nullable
	}

	// Column default overrides type default
	if defaultOverride != nil {
		rc.Default = *defaultOverride
		rc.DefaultExpr = "" // literal default takes precedence
	}

	// Column default_expr overrides type default_expr
	if defaultExprOverride != nil {
		rc.DefaultExpr = *defaultExprOverride
		rc.Default = "" // expression default takes precedence
	}

	return rc, nil
}
