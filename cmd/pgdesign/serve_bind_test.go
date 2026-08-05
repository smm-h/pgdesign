package main

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"strings"
	"testing"
)

// TestServeBindPosture verifies serve's stated security posture (roadmap 8.2): the
// server binds loopback (127.0.0.1) by default, and the --bind override flag's help
// text explicitly states there is NO AUTHENTICATION. Auth itself is the deferred
// frontend's concern — a decided non-goal, not an omission.
func TestServeBindPosture(t *testing.T) {
	testenv.Isolate(t)
	app := buildApp()
	res := app.Test([]string{"serve", "--help"})

	help := res.Stdout + res.Stderr

	// The default bind is loopback-only.
	if !strings.Contains(help, "127.0.0.1") {
		t.Fatalf("expected serve --help to document the 127.0.0.1 default bind, got:\n%s", help)
	}
	// The override flag must warn there is no authentication.
	if !strings.Contains(help, "--bind") {
		t.Fatalf("expected a --bind flag in serve --help, got:\n%s", help)
	}
	lowered := strings.ToLower(help)
	if !strings.Contains(lowered, "no authentication") {
		t.Fatalf("expected the --bind help text to state the server has NO AUTHENTICATION, got:\n%s", help)
	}
}
