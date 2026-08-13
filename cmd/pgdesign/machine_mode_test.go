package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/testenv"
)

// envelope is the framework's machine-mode document (effects contract §19.2).
// Under --json it is the ONLY thing stdout carries, and a command's machine
// output rides its `payload` member.
type envelope struct {
	InterfaceVersion int             `json:"interface_version"`
	App              string          `json:"app"`
	Command          *string         `json:"command"`
	ExitCode         int             `json:"exit_code"`
	Payload          json.RawMessage `json:"payload"`
	DryRun           bool            `json:"dry_run"`
	Preview          []any           `json:"preview"`
}

// decodeEnvelope parses stdout as exactly one envelope.
func decodeEnvelope(t *testing.T, stdout string) envelope {
	t.Helper()
	var env envelope
	dec := json.NewDecoder(strings.NewReader(stdout))
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not an envelope: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout carries more than the envelope\n%s", stdout)
	}
	if env.InterfaceVersion == 0 || env.App != "pgdesign" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	return env
}

// TestDiffMachineModeEnvelope: `diff --against` in machine mode emits the
// framework envelope alone, with the schema diff as its payload. The command
// declares no --json flag of its own -- the framework owns the name.
func TestDiffMachineModeEnvelope(t *testing.T) {
	testenv.Isolate(t)
	// --against and --live are mutually exclusive, and --live resolves from
	// PGDESIGN_DB, which this binary's TestMain exports for its ephemeral
	// cluster. This test compares two files and wants no live mode.
	testenv.Unset(t, "PGDESIGN_DB")
	dir := t.TempDir()
	writeFile(t, dir, "schema.toml", `format_version = 1
[meta]
version = 1
schema = "public"

[tables.orders]
comment = "orders"

[tables.orders.columns.id]
type = "id"
`)
	writeFile(t, dir, "other.toml", `format_version = 1
[meta]
version = 1
schema = "public"

[tables.orders]
comment = "orders"

[tables.orders.columns.id]
type = "id"

[tables.customers]
comment = "customers"

[tables.customers.columns.id]
type = "id"
`)
	t.Chdir(dir)

	res := buildApp().Test([]string{"diff", "schema.toml", "--against", "other.toml", "--json"})
	if res.ExitCode != 0 {
		t.Fatalf("diff --json: exit %d\n%s%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	env := decodeEnvelope(t, res.Stdout)
	if env.Command == nil || *env.Command != "diff" {
		t.Errorf("envelope command = %v, want diff", env.Command)
	}
	var payload map[string]any
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("payload is not the schema diff: %v\n%s", err, env.Payload)
	}
	for _, key := range []string{"tables_added", "tables_removed", "tables_changed"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("payload missing %q: %v", key, payload)
		}
	}
	removed, _ := payload["tables_removed"].([]any)
	if len(removed) != 1 || removed[0] != "customers" {
		t.Errorf("tables_removed = %v, want [customers]", payload["tables_removed"])
	}

	// Human mode is unchanged: the terminal rendering, no envelope.
	human := buildApp().Test([]string{"diff", "schema.toml", "--against", "other.toml"})
	if human.ExitCode != 0 {
		t.Fatalf("diff human: exit %d\n%s%s", human.ExitCode, human.Stdout, human.Stderr)
	}
	if strings.Contains(human.Stdout, "interface_version") {
		t.Errorf("human mode emitted an envelope:\n%s", human.Stdout)
	}
	if !strings.Contains(human.Stdout, "customers") {
		t.Errorf("human diff missing the removed table:\n%s", human.Stdout)
	}
}
