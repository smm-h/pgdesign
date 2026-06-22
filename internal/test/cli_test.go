package test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCLIRegistration verifies that all commands and groups register without
// panicking. This catches strictcli flag-spec errors (e.g., missing Unique on
// Repeatable flags) that cause a panic on any invocation.
func TestCLIRegistration(t *testing.T) {
	root := projectRoot(t)
	cmd := exec.Command("go", "run", "./cmd/pgdesign/", "--help")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pgdesign --help failed: %v\n%s", err, out)
	}

	output := string(out)

	// Verify that all expected command groups appear in help output.
	for _, want := range []string{"testdb", "migrate"} {
		if !strings.Contains(output, want) {
			t.Errorf("command group %q not found in --help output", want)
		}
	}

	// Verify a sample of top-level commands.
	for _, want := range []string{"generate", "check", "introspect", "diff", "serve"} {
		if !strings.Contains(output, want) {
			t.Errorf("command %q not found in --help output", want)
		}
	}
}
