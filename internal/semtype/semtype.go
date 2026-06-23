// Package semtype implements the semantic type system for pgdesign.
// It maps type names to PostgreSQL types with enforced attributes.
package semtype

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/smm-h/pgdesign/internal/diagnostic"
)

// Kind represents the kind of a type definition.
type Kind int

const (
	KindScalar       Kind = iota
	KindEnum         Kind = iota
	KindComposite    Kind = iota
	KindStateMachine Kind = iota
)

func (k Kind) String() string {
	switch k {
	case KindScalar:
		return "scalar"
	case KindEnum:
		return "enum"
	case KindComposite:
		return "composite"
	case KindStateMachine:
		return "state_machine"
	default:
		return "unknown"
	}
}

// CompositeField represents a single field in a composite type.
type CompositeField struct {
	Name   string
	PGType string
}

// SMStateDef represents a state in a state machine type definition.
type SMStateDef struct {
	Name     string
	Terminal bool
	Comment  string
}

// SMTransitionDef represents a transition in a state machine type definition.
type SMTransitionDef struct {
	Name     string
	From     []string
	To       string
	Requires map[string]string
	Comment  string
}

// TypeDef defines a semantic type with its PostgreSQL mapping and constraints.
type TypeDef struct {
	Name        string
	Kind        Kind
	BaseType    string           // PG type (e.g., "uuid", "text", "bigint")
	NotNull     bool
	Default     *string          // literal default value
	DefaultExpr string           // SQL expression default (e.g., "gen_random_uuid()")
	Check       string           // check constraint expression (VALUE placeholder)
	Unique      bool
	Comment     string
	EnumValues  []string         // values for enum types
	Fields      []CompositeField // fields for composite types
	States         []SMStateDef     // states for state machine types
	Transitions    []SMTransitionDef // transitions for state machine types
	InitialState   string           // initial state for state machine types
	EnforceTrigger bool             // whether to generate transition-enforcement trigger
	Generated   string           // generated expression (e.g., "price * 0.2")
	Stored      bool             // whether generated column is stored
	Identity    string           // identity generation: "ALWAYS" or "BY DEFAULT"
	Array       bool
}

// Registry holds named TypeDefs with thread-safe read access.
type Registry struct {
	mu             sync.RWMutex
	types          map[string]*TypeDef
	extensionTypes map[string]bool
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		types: make(map[string]*TypeDef),
	}
}

// AddExtensionTypes registers extension-provided type names as valid base types
// for scalar definitions. This allows extensions like pgvector to provide types
// (e.g., "vector") that pass the allowlist check without mutating global state.
func (r *Registry) AddExtensionTypes(typeNames []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.extensionTypes == nil {
		r.extensionTypes = make(map[string]bool, len(typeNames))
	}
	for _, name := range typeNames {
		r.extensionTypes[name] = true
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
	if !strPtrEqual(a.Default, b.Default) || a.DefaultExpr != b.DefaultExpr {
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
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for i := range a.Fields {
		if a.Fields[i] != b.Fields[i] {
			return false
		}
	}
	if a.Generated != b.Generated || a.Stored != b.Stored {
		return false
	}
	if a.Identity != b.Identity {
		return false
	}
	if a.Array != b.Array {
		return false
	}
	// State machine fields
	if a.InitialState != b.InitialState || a.EnforceTrigger != b.EnforceTrigger {
		return false
	}
	if len(a.States) != len(b.States) {
		return false
	}
	for i := range a.States {
		if a.States[i].Name != b.States[i].Name ||
			a.States[i].Terminal != b.States[i].Terminal ||
			a.States[i].Comment != b.States[i].Comment {
			return false
		}
	}
	if len(a.Transitions) != len(b.Transitions) {
		return false
	}
	for i := range a.Transitions {
		at, bt := a.Transitions[i], b.Transitions[i]
		if at.Name != bt.Name || at.To != bt.To || at.Comment != bt.Comment {
			return false
		}
		if len(at.From) != len(bt.From) {
			return false
		}
		for j := range at.From {
			if at.From[j] != bt.From[j] {
				return false
			}
		}
		if len(at.Requires) != len(bt.Requires) {
			return false
		}
		for k, v := range at.Requires {
			if bt.Requires[k] != v {
				return false
			}
		}
	}
	return true
}

func strPtrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func strPtr(s string) *string {
	return &s
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

// StateMachineTypes returns all registered state machine type definitions,
// sorted by name for deterministic output.
func (r *Registry) StateMachineTypes() []*TypeDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*TypeDef
	for _, td := range r.types {
		if td.Kind == KindStateMachine {
			result = append(result, td)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// UserSMState represents a state in a user-defined state machine type.
type UserSMState struct {
	Name     string
	Terminal bool
	Comment  string
}

// UserSMTransition represents a transition in a user-defined state machine type.
type UserSMTransition struct {
	Name     string
	From     []string
	To       string
	Requires map[string]string
	Comment  string
}

// UserTypeDef represents a user-defined type loaded from configuration.
type UserTypeDef struct {
	Name        string
	Kind        string // "scalar", "enum", "composite", "state_machine"
	Base        string // PG base type (for scalars)
	Values      []string // enum values
	Fields      map[string]string // composite fields: field name -> PG type
	States         []UserSMState    // state machine states
	Transitions    []UserSMTransition // state machine transitions
	InitialState   string           // state machine initial state
	EnforceTrigger bool             // state machine: generate enforcement trigger
	NotNull     *bool
	Default     *string
	DefaultExpr string
	Check       string
	Unique      bool
	Array       bool
	Comment     string
}

// pgTypeAllowlist contains valid PostgreSQL base types for user-defined scalars.
var pgTypeAllowlist = map[string]bool{
	"bigint":          true,
	"bigserial":       true,
	"boolean":         true,
	"bytea":           true,
	"char":            true,
	"citext":          true,
	"date":            true,
	"datemultirange":  true,
	"daterange":       true,
	"float4":          true,
	"float8":          true,
	"inet":            true,
	"int4multirange":  true,
	"int4range":       true,
	"int8multirange":  true,
	"int8range":       true,
	"integer":         true,
	"interval":        true,
	"json":            true,
	"jsonb":           true,
	"macaddr":         true,
	"numeric":         true,
	"nummultirange":   true,
	"numrange":        true,
	"oid":             true,
	"real":            true,
	"serial":          true,
	"smallint":        true,
	"smallserial":     true,
	"text":            true,
	"time":            true,
	"timetz":          true,
	"timestamp":       true,
	"timestamptz":     true,
	"tsmultirange":    true,
	"tsquery":         true,
	"tsrange":         true,
	"tstzmultirange":  true,
	"tstzrange":       true,
	"tsvector":        true,
	"uuid":            true,
	"varchar":         true,
	"xml":             true,
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

		kind, err := parseKind(ut.Kind)
		if err != nil {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E104",
				Message:  fmt.Sprintf("type %q: %s", ut.Name, err.Error()),
			})
			continue
		}

		switch kind {
		case KindEnum:
			diags = append(diags, r.loadEnumType(ut)...)
		case KindScalar:
			diags = append(diags, r.loadScalarType(ut)...)
		case KindComposite:
			diags = append(diags, r.loadCompositeType(ut)...)
		case KindStateMachine:
			diags = append(diags, r.loadStateMachineType(ut)...)
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
		Array:      ut.Array,
		Comment:    ut.Comment,
	}

	if ut.NotNull != nil {
		td.NotNull = *ut.NotNull
	}
	if ut.Default != nil {
		if strings.HasPrefix(*ut.Default, "'") && strings.HasSuffix(*ut.Default, "'") {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E110",
				Message:  fmt.Sprintf("type %q default %q appears to contain SQL quotes; use raw values (e.g., \"created\" not \"'created'\")", ut.Name, *ut.Default),
			})
		}
		found := false
		for _, v := range ut.Values {
			if v == *ut.Default {
				found = true
				break
			}
		}
		if !found {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E109",
				Message:  fmt.Sprintf("enum type %q default %q is not a declared value (valid: %s)", ut.Name, *ut.Default, strings.Join(ut.Values, ", ")),
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

func (r *Registry) loadCompositeType(ut UserTypeDef) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	if len(ut.Fields) == 0 {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E103",
			Message:  fmt.Sprintf("composite type %q must have at least one field", ut.Name),
		})
		return diags
	}

	// Sort field names for deterministic output
	fieldNames := make([]string, 0, len(ut.Fields))
	for name := range ut.Fields {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)

	// Validate each field's PG type and build the sorted field list
	fields := make([]CompositeField, 0, len(ut.Fields))
	for _, name := range fieldNames {
		pgType := ut.Fields[name]
		baseForCheck := strings.ToLower(pgType)
		if idx := strings.IndexByte(baseForCheck, '('); idx != -1 {
			baseForCheck = baseForCheck[:idx]
		}
		if !pgTypeAllowlist[baseForCheck] && !r.extensionTypes[baseForCheck] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Error,
				Code:       "E103",
				Message:    fmt.Sprintf("composite type %q field %q: unknown base type %q", ut.Name, name, pgType),
				Suggestion: "field type must be a valid PostgreSQL type",
			})
			continue
		}
		fields = append(fields, CompositeField{Name: name, PGType: pgType})
	}

	if diags.HasErrors() {
		return diags
	}

	td := &TypeDef{
		Name:     ut.Name,
		Kind:     KindComposite,
		BaseType: ut.Name, // composite types use their own name as PG type
		Fields:   fields,
		Comment:  ut.Comment,
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
	if !pgTypeAllowlist[baseForCheck] && !r.extensionTypes[baseForCheck] {
		diags = append(diags, diagnostic.Diagnostic{
			Severity:   diagnostic.Error,
			Code:       "E106",
			Message:    fmt.Sprintf("scalar type %q: unknown base type %q", ut.Name, ut.Base),
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

	if ut.Default != nil && strings.HasPrefix(*ut.Default, "'") && strings.HasSuffix(*ut.Default, "'") {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E110",
			Message:  fmt.Sprintf("type %q default %q appears to contain SQL quotes; use raw values (e.g., \"created\" not \"'created'\")", ut.Name, *ut.Default),
		})
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
		Array:       ut.Array,
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

func (r *Registry) loadStateMachineType(ut UserTypeDef) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	// Must have at least one state.
	if len(ut.States) == 0 {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E111",
			Message:  fmt.Sprintf("state machine type %q must have at least one state", ut.Name),
		})
		return diags
	}

	// Build set of valid state names and check for duplicates.
	stateSet := make(map[string]bool, len(ut.States))
	for _, s := range ut.States {
		if stateSet[s.Name] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E111",
				Message:  fmt.Sprintf("state machine type %q: duplicate state name %q", ut.Name, s.Name),
			})
		}
		stateSet[s.Name] = true
	}
	if diags.HasErrors() {
		return diags
	}

	// InitialState must reference a valid state.
	if ut.InitialState == "" {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E112",
			Message:  fmt.Sprintf("state machine type %q: initial state is required", ut.Name),
		})
		return diags
	}
	if !stateSet[ut.InitialState] {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E112",
			Message:  fmt.Sprintf("state machine type %q: initial state %q is not a declared state", ut.Name, ut.InitialState),
		})
		return diags
	}

	// Validate transitions: from/to must reference valid state names.
	for _, tr := range ut.Transitions {
		if tr.Name == "" {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E113",
				Message:  fmt.Sprintf("state machine type %q: transition missing name", ut.Name),
			})
			continue
		}
		for _, from := range tr.From {
			if !stateSet[from] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E113",
					Message:  fmt.Sprintf("state machine type %q: transition %q references unknown from-state %q", ut.Name, tr.Name, from),
				})
			}
		}
		if !stateSet[tr.To] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E113",
				Message:  fmt.Sprintf("state machine type %q: transition %q references unknown to-state %q", ut.Name, tr.Name, tr.To),
			})
		}
	}
	if diags.HasErrors() {
		return diags
	}

	// Build EnumValues from state names (for DDL: the column is an enum).
	enumValues := make([]string, len(ut.States))
	for i, s := range ut.States {
		enumValues[i] = s.Name
	}

	// Build internal state and transition defs.
	states := make([]SMStateDef, len(ut.States))
	for i, s := range ut.States {
		states[i] = SMStateDef{
			Name:     s.Name,
			Terminal: s.Terminal,
			Comment:  s.Comment,
		}
	}
	transitions := make([]SMTransitionDef, len(ut.Transitions))
	for i, tr := range ut.Transitions {
		requires := make(map[string]string, len(tr.Requires))
		for k, v := range tr.Requires {
			requires[k] = v
		}
		transitions[i] = SMTransitionDef{
			Name:     tr.Name,
			From:     tr.From,
			To:       tr.To,
			Requires: requires,
			Comment:  tr.Comment,
		}
	}

	td := &TypeDef{
		Name:           ut.Name,
		Kind:           KindStateMachine,
		BaseType:       ut.Name, // state machines use their own name as PG type (enum)
		NotNull:        true,
		EnumValues:     enumValues,
		States:         states,
		Transitions:    transitions,
		InitialState:   ut.InitialState,
		EnforceTrigger: ut.EnforceTrigger,
		Comment:        ut.Comment,
	}

	if ut.NotNull != nil {
		td.NotNull = *ut.NotNull
	}
	if ut.Default != nil {
		// Validate default is a valid state name.
		if !stateSet[*ut.Default] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E109",
				Message:  fmt.Sprintf("state machine type %q default %q is not a declared state (valid: %s)", ut.Name, *ut.Default, strings.Join(enumValues, ", ")),
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

func parseKind(s string) (Kind, error) {
	switch strings.ToLower(s) {
	case "", "scalar":
		return KindScalar, nil
	case "enum":
		return KindEnum, nil
	case "composite":
		return KindComposite, nil
	case "state_machine":
		return KindStateMachine, nil
	default:
		return 0, fmt.Errorf("unrecognized kind %q", s)
	}
}

// ResolvedColumn represents the final resolved attributes for a column after
// applying type defaults and column-level overrides.
type ResolvedColumn struct {
	PGType      string
	NotNull     bool
	Default     *string
	DefaultExpr string
	Check       string
	Generated   string
	Stored      bool
	Identity    string
	Array       bool
}

// ResolveColumn resolves a column's final attributes by looking up the type
// and applying column-level overrides.
func (r *Registry) ResolveColumn(typeName string, nullable *bool, defaultOverride *string, defaultExprOverride *string, array *bool) (*ResolvedColumn, error) {
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
		Array:       td.Array,
	}

	// Column nullable overrides type NotNull
	if nullable != nil {
		rc.NotNull = !*nullable
	}

	// Column default overrides type default
	if defaultOverride != nil {
		v := *defaultOverride
		rc.Default = &v
		rc.DefaultExpr = "" // literal default takes precedence
	}

	// Column default_expr overrides type default_expr
	if defaultExprOverride != nil {
		rc.DefaultExpr = *defaultExprOverride
		rc.Default = nil // expression default takes precedence
	}

	// Column array overrides type array
	if array != nil {
		rc.Array = *array
	}

	return rc, nil
}
