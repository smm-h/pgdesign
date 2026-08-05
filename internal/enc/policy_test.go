package enc

import (
	"fmt"
	"github.com/smm-h/pgdesign/internal/testenv"
	"reflect"
	"sort"
	"testing"

	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// checkStructPolicy is the mechanical totality guard. Given a struct type and
// its policy, it returns one violation per exported field that is (a) neither
// encoded nor excluded, (b) both encoded and excluded, (c) excluded with an
// empty reason, or per policy entry that names a field the struct does not
// have. A struct fully partitioned by the policy — every exported field either
// encoded or excluded-with-reason — produces zero violations.
func checkStructPolicy(t reflect.Type, p structPolicy) []string {
	var violations []string

	encoded := make(map[string]bool, len(p.encoded))
	for _, f := range p.encoded {
		encoded[f] = true
	}

	// Every exported field must be classified exactly once.
	fieldExists := make(map[string]bool)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		fieldExists[f.Name] = true
		_, isEncoded := encoded[f.Name]
		reason, isExcluded := p.excluded[f.Name]
		switch {
		case isEncoded && isExcluded:
			violations = append(violations, fmt.Sprintf("%s.%s is BOTH encoded and excluded", t.Name(), f.Name))
		case !isEncoded && !isExcluded:
			violations = append(violations, fmt.Sprintf("%s.%s is UNCLASSIFIED (add it to encoded or to the exclusion allowlist with a reason)", t.Name(), f.Name))
		case isExcluded && reason == "":
			violations = append(violations, fmt.Sprintf("%s.%s is excluded WITHOUT a reason", t.Name(), f.Name))
		}
	}

	// Every policy entry must name a real exported field (catches stale entries
	// after a field rename/removal).
	for _, f := range p.encoded {
		if !fieldExists[f] {
			violations = append(violations, fmt.Sprintf("%s: encoded policy names nonexistent field %q", t.Name(), f))
		}
	}
	for f := range p.excluded {
		if !fieldExists[f] {
			violations = append(violations, fmt.Sprintf("%s: exclusion policy names nonexistent field %q", t.Name(), f))
		}
	}

	sort.Strings(violations)
	return violations
}

// modelStructTypes maps every DDL-reaching model struct name to its reflect
// type. Keep in sync with modelFieldPolicy — the guard asserts the key sets
// match, so a new policy entry with no type (or vice versa) is caught.
func modelStructTypes() map[string]reflect.Type {
	return map[string]reflect.Type{
		"Schema":              reflect.TypeOf(model.Schema{}),
		"Table":               reflect.TypeOf(model.Table{}),
		"Column":              reflect.TypeOf(model.Column{}),
		"FK":                  reflect.TypeOf(model.FK{}),
		"Index":               reflect.TypeOf(model.Index{}),
		"UniqueConstraint":    reflect.TypeOf(model.UniqueConstraint{}),
		"CheckConstraint":     reflect.TypeOf(model.CheckConstraint{}),
		"ExclusionElement":    reflect.TypeOf(model.ExclusionElement{}),
		"ExclusionConstraint": reflect.TypeOf(model.ExclusionConstraint{}),
		"Policy":              reflect.TypeOf(model.Policy{}),
		"Trigger":             reflect.TypeOf(model.Trigger{}),
		"Enum":                reflect.TypeOf(model.Enum{}),
		"Sequence":            reflect.TypeOf(model.Sequence{}),
		"FunctionArg":         reflect.TypeOf(model.FunctionArg{}),
		"Function":            reflect.TypeOf(model.Function{}),
		"Domain":              reflect.TypeOf(model.Domain{}),
		"CompositeField":      reflect.TypeOf(model.CompositeField{}),
		"CompositeType":       reflect.TypeOf(model.CompositeType{}),
		"PartitionSpec":       reflect.TypeOf(model.PartitionSpec{}),
		"MaintenanceConfig":   reflect.TypeOf(model.MaintenanceConfig{}),
		"StateMachine":        reflect.TypeOf(model.StateMachine{}),
		"SMState":             reflect.TypeOf(model.SMState{}),
		"SMTransition":        reflect.TypeOf(model.SMTransition{}),
		"View":                reflect.TypeOf(model.View{}),
		"MaterializedView":    reflect.TypeOf(model.MaterializedView{}),
		"Type":                reflect.TypeOf(typeinfo.Type{}),
		"Params":              reflect.TypeOf(typeinfo.Params{}),
		"FuncDep":             reflect.TypeOf(fd.FuncDep{}),
	}
}

func registryStructTypes() map[string]reflect.Type {
	return map[string]reflect.Type{
		"TypeDef":         reflect.TypeOf(semtype.TypeDef{}),
		"SMStateDef":      reflect.TypeOf(semtype.SMStateDef{}),
		"SMTransitionDef": reflect.TypeOf(semtype.SMTransitionDef{}),
	}
}

// TestModelEncoderTotality is the reflection coverage guard over model structs:
// every exported field of every DDL-reaching struct is either encoded or on the
// exclusion allowlist with a reason. It turns red when a new model field is
// added without a deliberate encode-or-exclude decision.
func TestModelEncoderTotality(t *testing.T) {
	testenv.Isolate(t)
	types := modelStructTypes()
	assertKeySetsMatch(t, "modelFieldPolicy", modelFieldPolicy, types)
	for name, typ := range types {
		for _, v := range checkStructPolicy(typ, modelFieldPolicy[name]) {
			t.Errorf("model totality: %s", v)
		}
	}
}

// TestRegistrySnapshotTotality is the same coverage guard over the
// registry-snapshot structs.
func TestRegistrySnapshotTotality(t *testing.T) {
	testenv.Isolate(t)
	types := registryStructTypes()
	assertKeySetsMatch(t, "registryFieldPolicy", registryFieldPolicy, types)
	for name, typ := range types {
		for _, v := range checkStructPolicy(typ, registryFieldPolicy[name]) {
			t.Errorf("registry totality: %s", v)
		}
	}
}

func assertKeySetsMatch(t *testing.T, label string, policy map[string]structPolicy, types map[string]reflect.Type) {
	t.Helper()
	for name := range policy {
		if _, ok := types[name]; !ok {
			t.Errorf("%s has policy for %q but no reflect type is registered", label, name)
		}
	}
	for name := range types {
		if _, ok := policy[name]; !ok {
			t.Errorf("%s: struct %q has a registered type but no field policy", label, name)
		}
	}
}

// TestTotalityGuardCatchesUnclassifiedField proves the guard MECHANISM works:
// fed a struct with a field absent from its policy, checkStructPolicy reports a
// violation. This is what guarantees the real guards above go red on a new
// unencoded field.
func TestTotalityGuardCatchesUnclassifiedField(t *testing.T) {
	testenv.Isolate(t)
	type synthetic struct {
		Covered   string
		Excluded  string
		Forgotten string // deliberately left out of the policy
	}
	p := structPolicy{
		encoded:  []string{"Covered"},
		excluded: map[string]string{"Excluded": "test reason"},
	}
	violations := checkStructPolicy(reflect.TypeOf(synthetic{}), p)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation for the forgotten field, got %d: %v", len(violations), violations)
	}
}

// TestTotalityGuardCatchesEmptyReason proves an exclusion without a reason is a
// violation: exclusions must always justify themselves.
func TestTotalityGuardCatchesEmptyReason(t *testing.T) {
	testenv.Isolate(t)
	type synthetic struct {
		X string
	}
	p := structPolicy{excluded: map[string]string{"X": ""}}
	if v := checkStructPolicy(reflect.TypeOf(synthetic{}), p); len(v) != 1 {
		t.Fatalf("expected 1 violation for empty reason, got: %v", v)
	}
}
