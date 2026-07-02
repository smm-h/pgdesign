// Package semtype implements the semantic type system for pgdesign, mapping user-defined enums, scalar domains, composite types, and state machines.
package semtype

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/graph"
	"github.com/smm-h/pgdesign/internal/typeinfo"
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
	BaseType    typeinfo.Type    // PG type (e.g., "uuid", "text", "bigint")
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
	Source      string           // "builtin" or "user" — metadata, not compared by typeDefsEqual
}

// Registry holds named TypeDefs with thread-safe read access.
type Registry struct {
	mu             sync.RWMutex
	types          map[string]*TypeDef
	extensionTypes map[string]bool
	shadowDiags    diagnostic.Diagnostics // diagnostics from builtin shadowing in Register()
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
// Decision tree:
//  1. Name not in registry: register normally.
//  2. Name exists + identical definition: accept silently (idempotent for multi-file schemas).
//  3. Name exists + different definition + existing is builtin: allow shadowing if sealed
//     fields (Kind, BaseType.Base) match. Emits I101 on success, E114 on sealed field mismatch.
//  4. Name exists + different definition + existing is not builtin: E105 error.
func (r *Registry) Register(td *TypeDef) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.types[td.Name]
	if !exists {
		r.types[td.Name] = td
		return nil
	}

	if typeDefsEqual(existing, td) {
		return nil
	}

	// Different definition: check if shadowing a builtin.
	if existing.Source == "builtin" {
		// Sealed field checks: Kind and BaseType.Base must match.
		if existing.Kind != td.Kind {
			r.shadowDiags = append(r.shadowDiags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E114",
				Message:  fmt.Sprintf("type %q shadows builtin %q but has different Kind (sealed field cannot change)", td.Name, td.Name),
			})
			return nil
		}
		if existing.BaseType.Base != td.BaseType.Base {
			r.shadowDiags = append(r.shadowDiags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E114",
				Message:  fmt.Sprintf("type %q shadows builtin %q but has different BaseType (sealed field cannot change)", td.Name, td.Name),
			})
			return nil
		}

		// Sealed fields match: allow shadowing.
		r.shadowDiags = append(r.shadowDiags, diagnostic.Diagnostic{
			Severity: diagnostic.Info,
			Code:     "I101",
			Message:  fmt.Sprintf("type %q shadows builtin type %q", td.Name, td.Name),
		})
		r.types[td.Name] = td
		return nil
	}

	return fmt.Errorf("type %q already registered with a different definition", td.Name)
}

// ShadowDiags returns diagnostics accumulated during Register() calls for
// builtin shadowing (I101 info, E114 errors). Call after LoadUserTypes().
func (r *Registry) ShadowDiags() diagnostic.Diagnostics {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.shadowDiags
}

// IsBuiltin returns true if the named type exists in the registry with Source "builtin".
func (r *Registry) IsBuiltin(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	td, ok := r.types[name]
	return ok && td.Source == "builtin"
}

// typeDefsEqual returns true if two TypeDefs have equivalent definitions.
func typeDefsEqual(a, b *TypeDef) bool {
	if a.Kind != b.Kind || !a.BaseType.Equal(b.BaseType) || a.NotNull != b.NotNull {
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
	Extends     string // parent type name for type derivation
	Base        string // PG base type (for scalars)
	Values      []string // enum values
	Fields      []CompositeField // composite fields, in declaration order (order is semantic: it becomes the PostgreSQL composite field order)
	States         []UserSMState    // state machine states
	Transitions    []UserSMTransition // state machine transitions
	InitialState   string           // state machine initial state
	EnforceTrigger *bool            // state machine: generate enforcement trigger (nil = not set)
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
// Types with extends references are topologically sorted before processing
// so that parent types are always loaded before their children.
// Returns diagnostics for any validation failures.
func (r *Registry) LoadUserTypes(types []UserTypeDef) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	// Pre-validate names before topo sort.
	var valid []UserTypeDef
	for _, ut := range types {
		if ut.Name == "" {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E100",
				Message:  "user-defined type has empty name",
			})
			continue
		}
		valid = append(valid, ut)
	}

	// Build set of user type names being loaded, for extends target validation.
	userTypeNames := make(map[string]bool, len(valid))
	for _, ut := range valid {
		userTypeNames[ut.Name] = true
	}

	// Validate extends targets before topo sort: each target must exist in the
	// registry or be another user type being loaded.
	for _, ut := range valid {
		if ut.Extends == "" || ut.Extends == ut.Name {
			// No extends, or self-shadowing (self-dep is fine, topo sort skips it).
			continue
		}
		r.mu.RLock()
		_, inRegistry := r.types[ut.Extends]
		r.mu.RUnlock()
		if !inRegistry && !userTypeNames[ut.Extends] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E116",
				Message:  fmt.Sprintf("type %q extends unknown type %q", ut.Name, ut.Extends),
			})
		}
	}
	if diags.HasErrors() {
		return diags
	}

	// Topological sort by extends references.
	sorted, cycles := graph.TopoSort(valid,
		func(ut UserTypeDef) string { return ut.Name },
		func(ut UserTypeDef) []string {
			if ut.Extends == "" {
				return nil
			}
			return []string{ut.Extends}
		},
	)

	// Emit E115 for cycle members.
	for _, cycle := range cycles {
		names := make([]string, len(cycle))
		for i, ut := range cycle {
			names[i] = ut.Name
		}
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E115",
			Message:  fmt.Sprintf("circular extends reference: %s", strings.Join(names, ", ")),
		})
	}
	if diags.HasErrors() {
		return diags
	}

	// Process types in topological order.
	for _, ut := range sorted {
		if ut.Extends != "" {
			diags = append(diags, r.loadExtendedType(ut)...)
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

// loadExtendedType handles a type with extends set.
// It resolves the parent from the registry, merges fields, and registers.
func (r *Registry) loadExtendedType(ut UserTypeDef) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	parent, err := r.Resolve(ut.Extends)
	if err != nil {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E116",
			Message:  fmt.Sprintf("type %q extends unknown type %q", ut.Name, ut.Extends),
		})
		return diags
	}

	var merged *TypeDef

	switch parent.Kind {
	case KindScalar:
		// Sealed field enforcement: BaseType cannot be changed via extends.
		if ut.Base != "" {
			childBase := typeinfo.Parse(ut.Base)
			if childBase.Base != parent.BaseType.Base {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E114",
					Message:  fmt.Sprintf("type %q extends %q but has different BaseType (sealed field cannot change)", ut.Name, ut.Extends),
				})
				return diags
			}
		}
		merged = mergeScalarType(parent, ut)

	case KindEnum:
		var mergeDiags diagnostic.Diagnostics
		merged, mergeDiags = mergeEnumType(parent, ut)
		diags = append(diags, mergeDiags...)
		if diags.HasErrors() {
			return diags
		}

	case KindComposite:
		var mergeDiags diagnostic.Diagnostics
		merged, mergeDiags = mergeCompositeType(parent, ut)
		diags = append(diags, mergeDiags...)
		if diags.HasErrors() {
			return diags
		}

	case KindStateMachine:
		var mergeDiags diagnostic.Diagnostics
		merged, mergeDiags = mergeStateMachineType(parent, ut)
		diags = append(diags, mergeDiags...)
		if diags.HasErrors() {
			return diags
		}

	default:
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E116",
			Message:  fmt.Sprintf("extends not supported for %s types", parent.Kind),
		})
		return diags
	}

	if err := r.Register(merged); err != nil {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E105",
			Message:  fmt.Sprintf("type %q: %s", ut.Name, err.Error()),
		})
	}

	return diags
}

// mergeScalarType creates a new TypeDef by copying the parent and overriding
// with any non-zero fields from the child UserTypeDef.
func mergeScalarType(parent *TypeDef, child UserTypeDef) *TypeDef {
	td := &TypeDef{
		Name:        child.Name,
		Kind:        parent.Kind,
		BaseType:    parent.BaseType,
		NotNull:     parent.NotNull,
		Default:     parent.Default,
		DefaultExpr: parent.DefaultExpr,
		Check:       parent.Check,
		Unique:      parent.Unique,
		Array:       parent.Array,
		Comment:     parent.Comment,
		Source:      "extended",
	}

	// Override with child's non-zero fields.
	if child.NotNull != nil {
		td.NotNull = *child.NotNull
	}
	if child.Default != nil {
		td.Default = child.Default
	}
	if child.DefaultExpr != "" {
		td.DefaultExpr = child.DefaultExpr
	}
	if child.Check != "" {
		td.Check = child.Check
	}
	if child.Unique {
		td.Unique = true
	}
	if child.Array {
		td.Array = true
	}
	if child.Comment != "" {
		td.Comment = child.Comment
	}

	return td
}

// mergeEnumType creates a new TypeDef by copying the parent enum and appending
// the child's values (with deduplication).
func mergeEnumType(parent *TypeDef, child UserTypeDef) (*TypeDef, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	td := &TypeDef{
		Name:       child.Name,
		Kind:       KindEnum,
		BaseType:   typeinfo.Type{Base: child.Name},
		NotNull:    parent.NotNull,
		Default:    parent.Default,
		EnumValues: make([]string, len(parent.EnumValues)),
		Array:      parent.Array,
		Comment:    parent.Comment,
		Source:     "extended",
	}
	copy(td.EnumValues, parent.EnumValues)

	// Append child values, deduplicating against parent.
	parentSet := make(map[string]bool, len(parent.EnumValues))
	for _, v := range parent.EnumValues {
		parentSet[v] = true
	}
	for _, v := range child.Values {
		if !parentSet[v] {
			td.EnumValues = append(td.EnumValues, v)
		}
	}

	// Override non-zero fields from child.
	if child.Default != nil {
		td.Default = child.Default
	}
	if child.NotNull != nil {
		td.NotNull = *child.NotNull
	}
	if child.Comment != "" {
		td.Comment = child.Comment
	}
	if child.Array {
		td.Array = true
	}

	// E117: enum extends with no new values and no other overrides.
	hasNewValues := len(child.Values) > 0
	hasOverrides := child.Default != nil || child.NotNull != nil || child.Comment != "" || child.Array
	if !hasNewValues && !hasOverrides {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Warning,
			Code:     "E117",
			Message:  fmt.Sprintf("enum type %q extends %q with no new values or overrides", child.Name, child.Extends),
		})
	}

	return td, diags
}

// mergeCompositeType creates a new TypeDef by merging the parent composite's
// fields with the child's fields. Duplicate field names are a hard error.
func mergeCompositeType(parent *TypeDef, child UserTypeDef) (*TypeDef, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	td := &TypeDef{
		Name:     child.Name,
		Kind:     KindComposite,
		BaseType: typeinfo.Type{Base: child.Name},
		Comment:  parent.Comment,
		Source:   "extended",
	}

	// Check for duplicate field names within the child itself (fields are a
	// declaration-ordered slice, so duplicates are no longer structurally
	// impossible the way they were with a map).
	childFieldSet := make(map[string]bool, len(child.Fields))
	for _, f := range child.Fields {
		if childFieldSet[f.Name] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E103",
				Message:  fmt.Sprintf("composite type %q: duplicate field name %q", child.Name, f.Name),
			})
		}
		childFieldSet[f.Name] = true
	}

	// Check for field name collisions with the parent.
	parentFieldSet := make(map[string]bool, len(parent.Fields))
	for _, f := range parent.Fields {
		parentFieldSet[f.Name] = true
	}
	for _, f := range child.Fields {
		if parentFieldSet[f.Name] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E118",
				Message:  fmt.Sprintf("composite type %q extends %q: field %q exists in both parent and child", child.Name, child.Extends, f.Name),
			})
		}
	}
	if diags.HasErrors() {
		return nil, diags
	}

	// Copy parent fields (in parent declaration order), then append child
	// fields in child declaration order. Order is semantic: it becomes the
	// PostgreSQL composite field order.
	td.Fields = make([]CompositeField, len(parent.Fields))
	copy(td.Fields, parent.Fields)
	td.Fields = append(td.Fields, child.Fields...)

	// Override non-zero fields from child.
	if child.Comment != "" {
		td.Comment = child.Comment
	}

	return td, diags
}

// mergeStateMachineType creates a new TypeDef by merging the parent state
// machine with the child's states and transitions. Duplicate state names are a
// hard error. After merge, reachability is validated from the (possibly
// overridden) initial state.
func mergeStateMachineType(parent *TypeDef, child UserTypeDef) (*TypeDef, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	td := &TypeDef{
		Name:           child.Name,
		Kind:           KindStateMachine,
		BaseType:       typeinfo.Type{Base: child.Name},
		NotNull:        parent.NotNull,
		InitialState:   parent.InitialState,
		EnforceTrigger: parent.EnforceTrigger,
		Comment:        parent.Comment,
		Source:         "extended",
	}

	// Check for state name collisions.
	parentStateSet := make(map[string]bool, len(parent.States))
	for _, s := range parent.States {
		parentStateSet[s.Name] = true
	}
	for _, s := range child.States {
		if parentStateSet[s.Name] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E119",
				Message:  fmt.Sprintf("state machine type %q extends %q: state %q exists in both parent and child", child.Name, child.Extends, s.Name),
			})
		}
	}
	if diags.HasErrors() {
		return nil, diags
	}

	// Copy parent states and append child states.
	td.States = make([]SMStateDef, len(parent.States))
	copy(td.States, parent.States)
	for _, s := range child.States {
		td.States = append(td.States, SMStateDef{
			Name:     s.Name,
			Terminal: s.Terminal,
			Comment:  s.Comment,
		})
	}

	// Copy parent transitions and append child transitions.
	td.Transitions = make([]SMTransitionDef, len(parent.Transitions))
	for i, tr := range parent.Transitions {
		fromCopy := make([]string, len(tr.From))
		copy(fromCopy, tr.From)
		requires := make(map[string]string, len(tr.Requires))
		for k, v := range tr.Requires {
			requires[k] = v
		}
		td.Transitions[i] = SMTransitionDef{
			Name:     tr.Name,
			From:     fromCopy,
			To:       tr.To,
			Requires: requires,
			Comment:  tr.Comment,
		}
	}
	for _, tr := range child.Transitions {
		requires := make(map[string]string, len(tr.Requires))
		for k, v := range tr.Requires {
			requires[k] = v
		}
		td.Transitions = append(td.Transitions, SMTransitionDef{
			Name:     tr.Name,
			From:     tr.From,
			To:       tr.To,
			Requires: requires,
			Comment:  tr.Comment,
		})
	}

	// Override initial state if child sets it.
	if child.InitialState != "" {
		td.InitialState = child.InitialState
	}

	// Override enforce trigger if child explicitly sets it.
	if child.EnforceTrigger != nil {
		td.EnforceTrigger = *child.EnforceTrigger
	}

	// Override comment if child sets it.
	if child.Comment != "" {
		td.Comment = child.Comment
	}

	// Build EnumValues from merged states.
	td.EnumValues = make([]string, len(td.States))
	for i, s := range td.States {
		td.EnumValues[i] = s.Name
	}

	// Validate all transition from/to references against merged state set.
	stateSet := make(map[string]bool, len(td.States))
	for _, s := range td.States {
		stateSet[s.Name] = true
	}
	for _, tr := range td.Transitions {
		for _, from := range tr.From {
			if !stateSet[from] {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error,
					Code:     "E113",
					Message:  fmt.Sprintf("state machine type %q: transition %q references unknown from-state %q", child.Name, tr.Name, from),
				})
			}
		}
		if !stateSet[tr.To] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E113",
				Message:  fmt.Sprintf("state machine type %q: transition %q references unknown to-state %q", child.Name, tr.Name, tr.To),
			})
		}
	}
	if diags.HasErrors() {
		return nil, diags
	}

	// Validate initial state is in the merged state set.
	if !stateSet[td.InitialState] {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     "E112",
			Message:  fmt.Sprintf("state machine type %q: initial state %q is not a declared state", child.Name, td.InitialState),
		})
		return nil, diags
	}

	// Reachability: BFS from initial state.
	reachable := make(map[string]bool)
	queue := []string{td.InitialState}
	reachable[td.InitialState] = true
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, tr := range td.Transitions {
			for _, from := range tr.From {
				if from == current && !reachable[tr.To] {
					reachable[tr.To] = true
					queue = append(queue, tr.To)
				}
			}
		}
	}
	for _, s := range td.States {
		if !reachable[s.Name] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Warning,
				Code:       "W027",
				Message:    fmt.Sprintf("state machine %q: state %q is unreachable from initial state %q", td.Name, s.Name, td.InitialState),
				Suggestion: "Add a transition leading to this state, or remove it",
			})
		}
	}

	return td, diags
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
		BaseType:   typeinfo.Type{Base: ut.Name}, // enums use their own name as PG type
		NotNull:    true,
		EnumValues: ut.Values,
		Array:      ut.Array,
		Comment:    ut.Comment,
		Source:     "user",
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

	// Check for duplicate field names. Fields arrive in declaration order
	// (order is semantic: it becomes the PostgreSQL composite field order).
	fieldSet := make(map[string]bool, len(ut.Fields))
	for _, f := range ut.Fields {
		if fieldSet[f.Name] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Code:     "E103",
				Message:  fmt.Sprintf("composite type %q: duplicate field name %q", ut.Name, f.Name),
			})
		}
		fieldSet[f.Name] = true
	}
	if diags.HasErrors() {
		return diags
	}

	// Validate each field's PG type and build the field list in declaration order.
	fields := make([]CompositeField, 0, len(ut.Fields))
	for _, f := range ut.Fields {
		pgType := f.PGType
		baseForCheck := strings.ToLower(pgType)
		if idx := strings.IndexByte(baseForCheck, '('); idx != -1 {
			baseForCheck = baseForCheck[:idx]
		}
		if !pgTypeAllowlist[baseForCheck] && !r.extensionTypes[baseForCheck] {
			diags = append(diags, diagnostic.Diagnostic{
				Severity:   diagnostic.Error,
				Code:       "E103",
				Message:    fmt.Sprintf("composite type %q field %q: unknown base type %q", ut.Name, f.Name, pgType),
				Suggestion: "field type must be a valid PostgreSQL type",
			})
			continue
		}
		fields = append(fields, CompositeField{Name: f.Name, PGType: pgType})
	}

	if diags.HasErrors() {
		return diags
	}

	td := &TypeDef{
		Name:     ut.Name,
		Kind:     KindComposite,
		BaseType: typeinfo.Type{Base: ut.Name}, // composite types use their own name as PG type
		Fields:   fields,
		Comment:  ut.Comment,
		Source:   "user",
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
		BaseType:    typeinfo.Parse(ut.Base),
		NotNull:     true,
		Default:     ut.Default,
		DefaultExpr: ut.DefaultExpr,
		Check:       ut.Check,
		Unique:      ut.Unique,
		Array:       ut.Array,
		Comment:     ut.Comment,
		Source:      "user",
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
		BaseType:       typeinfo.Type{Base: ut.Name}, // state machines use their own name as PG type (enum)
		NotNull:        true,
		EnumValues:     enumValues,
		States:         states,
		Transitions:    transitions,
		InitialState:   ut.InitialState,
		EnforceTrigger: ut.EnforceTrigger != nil && *ut.EnforceTrigger,
		Comment:        ut.Comment,
		Source:         "user",
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
	PGType      typeinfo.Type
	NotNull     bool
	Default     *string
	DefaultExpr string
	Check       string
	Generated   string
	Stored      bool
	Identity    string
	Array       bool
	Kind        string // "scalar", "enum", "composite", "state_machine"
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
		Kind:        td.Kind.String(),
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
