package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/testenv"
)

// bytePinSchema is the fixture the byte pin below compiles. It is deliberately
// small and deliberately fixed: the pin is about the transport, not about how
// much of the model it exercises.
const bytePinSchema = `format_version = 1
[meta]
version = 1
schema = "public"

[tables.orders]
comment = "orders"

[tables.orders.columns.id]
type = "id"

[tables.orders.columns.label]
type = "short_text"
`

// TestGenerateJSONIsByteExactOnStdout pins the ONE property `generate --format
// json` must never lose: its stdout is the canonical whole-model envelope
// produced by rev.Marshal and NOTHING else -- no framework machine envelope
// around it, no trailing newline added, no re-encoding, no reordering.
//
// Downstream readers verify the document by re-hashing its `model` member and
// comparing against its `revision` member, so ANY byte change breaks them. The
// command deliberately declares no PayloadSchema: its JSON is the command's own
// document written straight to stdout, not a payload the framework wraps.
//
// This is the pin the strictcli migrations run against. A flag declaration may
// change spelling (presence options, choices records) as often as the framework
// requires; this document may not move a byte.
func TestGenerateJSONIsByteExactOnStdout(t *testing.T) {
	testenv.Isolate(t)
	dir := t.TempDir()
	writeFile(t, dir, "schema.toml", bytePinSchema)
	t.Chdir(dir)

	res := buildApp().Test([]string{"generate", "schema.toml", "--format", "json"})
	if res.ExitCode != 0 {
		t.Fatalf("generate --format json: exit %d\n%s%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	// The reference bytes: the same serializer, called directly.
	raw, diags := parse.File("schema.toml")
	if raw == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	schema, buildDiags := model.Build(raw, semtype.NewBuiltinRegistry())
	if buildDiags.HasErrors() {
		t.Fatalf("build failed: %v", buildDiags)
	}
	want, err := rev.Marshal(schema, rev.RegistryPresent, nil)
	if err != nil {
		t.Fatalf("rev.Marshal: %v", err)
	}

	if res.Stdout != string(want) {
		t.Errorf("generate --format json stdout is not the canonical envelope byte-for-byte\ngot  (%d bytes): %q\nwant (%d bytes): %q",
			len(res.Stdout), res.Stdout, len(want), string(want))
	}

	// Stated independently of the comparison above, so a change to the
	// serializer cannot make this assertion vacuous: stdout is exactly one JSON
	// document, and it is the schema envelope rather than a machine envelope.
	dec := json.NewDecoder(strings.NewReader(res.Stdout))
	var doc map[string]json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, res.Stdout)
	}
	if dec.More() {
		t.Fatalf("stdout carries more than one document:\n%s", res.Stdout)
	}
	for _, key := range []string{"format_version", "revision", "model"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("envelope is missing %q", key)
		}
	}
	for _, key := range []string{"interface_version", "payload", "exit_code"} {
		if _, ok := doc[key]; ok {
			t.Errorf("the framework machine envelope wrapped the schema document: %q is present", key)
		}
	}
}

// TestGenerateJSONHelpPinsTheFormatChoices pins the format vocabulary as a set.
// The choices are a declaration that may be respelled (bare values became
// value-plus-help records at strictcli 0.33); the accepted values may not
// silently shrink or grow, because "json" leaving this list would take the
// byte-pinned document with it.
func TestGenerateJSONHelpPinsTheFormatChoices(t *testing.T) {
	testenv.Isolate(t)
	for _, format := range []string{"sql", "json", "d2", "doc", "graphql"} {
		dir := t.TempDir()
		writeFile(t, dir, "schema.toml", bytePinSchema)
		t.Chdir(dir)
		res := buildApp().Test([]string{"generate", "schema.toml", "--format", format})
		if res.ExitCode != 0 {
			t.Errorf("generate --format %s: exit %d\n%s", format, res.ExitCode, res.Stderr)
		}
	}
	dir := t.TempDir()
	writeFile(t, dir, "schema.toml", bytePinSchema)
	t.Chdir(dir)
	res := buildApp().Test([]string{"generate", "schema.toml", "--format", "yaml"})
	if res.ExitCode == 0 {
		t.Errorf("generate --format yaml was accepted; the choices list is not enforced")
	}
}
