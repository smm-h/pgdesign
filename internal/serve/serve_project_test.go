package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/semtype"
)

// buildTestProject parses and builds a small model plus its registry for
// project-mode serve tests. It uses model.Build directly (registry-present class).
func buildTestProject(t *testing.T, toml string) (*model.Schema, *semtype.Registry) {
	t.Helper()
	raw, pdiags := parse.Bytes([]byte(toml))
	if raw == nil {
		t.Fatalf("parse failed: %v", pdiags)
	}
	reg := semtype.NewBuiltinRegistry()
	if uts := parse.CollectUserTypes(raw); len(uts) > 0 {
		if d := reg.LoadUserTypes(uts); d.HasErrors() {
			t.Fatalf("load user types: %v", d)
		}
	}
	schema, bdiags := model.Build(raw, reg)
	if bdiags.HasErrors() {
		t.Fatalf("build failed: %v", bdiags)
	}
	return schema, reg
}

const projectTOML = `format_version = 1
[meta]
version = 15

[tables.users]
comment = "user accounts"
pk = ["id"]

[tables.users.columns.id]
type = "id"

[tables.users.columns.name]
type = "short_text"

[tables.orders]
comment = "orders placed by users"
pk = ["id"]

[tables.orders.columns.id]
type = "id"

[tables.orders.columns.user_id]
type = "ref"

[tables.orders.fks.fk_orders_user]
columns = ["user_id"]
ref_table = "users"
ref_columns = ["id"]
on_delete = "CASCADE"
`

// TestProjectMode_SchemaByteConsistentWithGenerateJSON verifies the DB-free
// /api/schema body is byte-identical to `generate json` for the same model (L1:
// one canonical serializer everywhere). Both call rev.Marshal with the
// registry-present class and no diagnostics.
func TestProjectMode_SchemaByteConsistentWithGenerateJSON(t *testing.T) {
	schema, reg := buildTestProject(t, projectTOML)
	srv := NewProject(schema, reg, nil, []string{"public"}, "")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/schema")
	if err != nil {
		t.Fatalf("GET /api/schema: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	want, _, genErr := generate.Generate(schema, generate.Options{Format: "json", ModelClass: rev.RegistryPresent})
	if genErr != nil {
		t.Fatalf("generate json: %v", genErr)
	}
	if string(body) != want {
		t.Fatalf("/api/schema body not byte-consistent with generate json\n--- serve ---\n%s\n--- generate ---\n%s", body, want)
	}

	// The envelope must parse and be registry-present.
	env, err := rev.Parse(body)
	if err != nil {
		t.Fatalf("envelope Parse failed: %v", err)
	}
	if env.Revision.Class() != rev.RegistryPresent {
		t.Errorf("expected registry-present class in project mode, got %s", env.Revision.Class())
	}
}

// TestProjectMode_DBEndpointDegrades verifies database-backed endpoints return an
// explicit 503 (not a nil-pool panic) when the server runs without a database.
func TestProjectMode_DBEndpointDegrades(t *testing.T) {
	schema, reg := buildTestProject(t, projectTOML)
	srv := NewProject(schema, reg, nil, []string{"public"}, "")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	for _, path := range []string{"/api/stats", "/api/extensions", "/api/migrations"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("GET %s: expected 503 in project mode, got %d (body: %s)", path, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "project mode") {
			t.Errorf("GET %s: expected an explicit project-mode message, got: %s", path, body)
		}
	}
}

// TestProjectMode_Graph verifies the FK-graph projection endpoint returns the
// resolved edges/nodes of the compiled model.
func TestProjectMode_Graph(t *testing.T) {
	schema, reg := buildTestProject(t, projectTOML)
	srv := NewProject(schema, reg, nil, []string{"public"}, "")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/schema/graph")
	if err != nil {
		t.Fatalf("GET /api/schema/graph: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// The orders->users FK must appear as an edge.
	if !strings.Contains(string(body), "\"to_table\": \"users\"") {
		t.Fatalf("expected orders->users edge in projection, got: %s", body)
	}
}

const smTOML = `format_version = 1
[meta]
version = 15

[types.order_state]
kind = "state_machine"
initial = "pending"
comment = "order lifecycle"

[types.order_state.states.pending]
[types.order_state.states.shipped]
[types.order_state.states.delivered]
terminal = true

[[types.order_state.transitions]]
name = "ship"
from = ["pending"]
to = "shipped"

[[types.order_state.transitions]]
name = "deliver"
from = ["shipped"]
to = "delivered"

[tables.orders]
comment = "orders with a state machine column"
pk = ["id"]

[tables.orders.columns.id]
type = "id"

[tables.orders.columns.state]
type = "order_state"
`

// TestProjectMode_SMDiagramRenders verifies state-machine state diagrams render in
// project mode because the real semtype registry is passed to D2 generation (the
// registry-absent database path silently drops them).
func TestProjectMode_SMDiagramRenders(t *testing.T) {
	schema, reg := buildTestProject(t, smTOML)
	srv := NewProject(schema, reg, nil, []string{"public"}, "")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/schema/d2")
	if err != nil {
		t.Fatalf("GET /api/schema/d2: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	d2 := string(body)

	// A nil registry (the introspect path) would drop the state diagram entirely;
	// with the real registry the states must appear in the D2 output.
	if !strings.Contains(d2, "pending") || !strings.Contains(d2, "shipped") {
		t.Fatalf("expected state-machine states in D2 output (registry passed), got: %s", d2)
	}

	// Byte-consistency of the SM D2 with generate's D2 (same registry).
	want := generate.GenerateD2(schema, reg)
	if d2 != want {
		t.Fatalf("project-mode D2 differs from generate D2 with the same registry")
	}
}
