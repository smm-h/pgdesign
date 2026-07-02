package parse

import (
	"testing"

	"github.com/smm-h/pgdesign/internal/semtype"
)

// stateOrderTOML declares state machine states in a deliberately
// non-alphabetical order that also does not start with the initial state's
// alphabetical position. Declaration order is the semantic order: PostgreSQL
// enum value order affects comparison operators and ORDER BY.
const stateOrderTOML = `
[meta]
version = 1
schema = "public"

[types.job_status]
kind = "state_machine"
initial = "queued"

[types.job_status.states.queued]

[types.job_status.states.running]

[types.job_status.states.paused]

[types.job_status.states.retrying]

[types.job_status.states.failed]
terminal = true

[types.job_status.states.succeeded]
terminal = true

[types.job_status.states.archived]
terminal = true

[[types.job_status.transitions]]
name = "start"
from = ["queued", "retrying"]
to = "running"

[[types.job_status.transitions]]
name = "pause"
from = ["running"]
to = "paused"

[[types.job_status.transitions]]
name = "resume"
from = ["paused"]
to = "running"

[[types.job_status.transitions]]
name = "retry"
from = ["failed"]
to = "retrying"

[[types.job_status.transitions]]
name = "fail"
from = ["running"]
to = "failed"

[[types.job_status.transitions]]
name = "succeed"
from = ["running"]
to = "succeeded"

[[types.job_status.transitions]]
name = "archive"
from = ["succeeded", "failed"]
to = "archived"
`

// declaredStateOrder is the exact document order of the [types.job_status.states.*]
// sections in stateOrderTOML above.
var declaredStateOrder = []string{
	"queued", "running", "paused", "retrying", "failed", "succeeded", "archived",
}

// TestStateMachineStateOrder_DeclarationOrder verifies that a single fresh
// parse preserves TOML declaration order for state machine states, end to end
// through CollectUserTypes and the semtype registry's EnumValues.
func TestStateMachineStateOrder_DeclarationOrder(t *testing.T) {
	names, enumValues := parseStateOrder(t)
	assertOrder(t, "CollectUserTypes states", names, declaredStateOrder)
	assertOrder(t, "semtype EnumValues", enumValues, declaredStateOrder)
}

// TestStateMachineStateOrder_RebuildDeterminism parses and loads the same TOML
// from scratch N times. Every rebuild must yield the identical state order.
// This is the rebuild-per-iteration variant of the codegen determinism
// contract: a single built schema is always internally stable, but map-backed
// parsing randomizes the order across builds, which flaps freshness checks and
// silently reorders PostgreSQL enum values (a semantic change).
func TestStateMachineStateOrder_RebuildDeterminism(t *testing.T) {
	const runs = 20
	for i := 0; i < runs; i++ {
		names, enumValues := parseStateOrder(t)
		assertOrder(t, "CollectUserTypes states", names, declaredStateOrder)
		assertOrder(t, "semtype EnumValues", enumValues, declaredStateOrder)
		if t.Failed() {
			t.Fatalf("order diverged on rebuild %d of %d", i+1, runs)
		}
	}
}

// parseStateOrder does one fresh parse+load cycle and returns the state name
// order as seen by CollectUserTypes and by the semtype registry's EnumValues.
func parseStateOrder(t *testing.T) (names, enumValues []string) {
	t.Helper()

	raw, diags := Bytes([]byte(stateOrderTOML))
	if raw == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	userTypes := CollectUserTypes(raw)
	if len(userTypes) != 1 {
		t.Fatalf("expected 1 user type, got %d", len(userTypes))
	}
	for _, s := range userTypes[0].States {
		names = append(names, s.Name)
	}

	reg := semtype.NewBuiltinRegistry()
	if loadDiags := reg.LoadUserTypes(userTypes); loadDiags.HasErrors() {
		t.Fatalf("LoadUserTypes errors: %v", loadDiags)
	}
	td, err := reg.Resolve("job_status")
	if err != nil {
		t.Fatalf("Resolve(job_status): %v", err)
	}
	return names, td.EnumValues
}

func assertOrder(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %d entries %v, want %d %v", label, len(got), got, len(want), want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: order mismatch at index %d: got %v, want %v", label, i, got, want)
			return
		}
	}
}
