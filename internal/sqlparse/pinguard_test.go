package sqlparse

import (
	"bufio"
	"github.com/smm-h/pgdesign/internal/testenv"
	"os"
	"strings"
	"testing"
)

// THE CI PIN GUARD (roadmap 1.2, boundary item 12).
//
// N — and therefore pgdesign's entire content identity — is DEFINED by
// go-pgquery's deparse output. A version bump can silently shift ≈_syn and
// re-key the world. This guard makes an accidental bump STRUCTURALLY IMPOSSIBLE:
// it asserts that go.mod's go-pgquery version equals the RECORDED SANCTIONED
// version in testdata/sanctioned_pgquery_version.txt. The pin moves ONLY by
// editing that file — an unmistakably deliberate act. It runs under `go test`,
// so CI enforces it with no extra wiring.
//
// EPOCH POLICY: essentially NEVER bump. When eventually forced (new PG syntax
// support, toolchain rot), a go-pgquery bump is a deliberate BREAKING MAJOR
// release carrying the event-time epoch-recovery procedure (L2) — every stored
// content id is invalidated. To sanction a new version: bump go.mod, update the
// sanctioned file in the SAME commit, and treat it as an epoch event.

const (
	pgqueryModulePath  = "github.com/wasilibs/go-pgquery"
	sanctionedFilePath = "testdata/sanctioned_pgquery_version.txt"
	goModRelPath       = "../../go.mod"
)

func TestPinGuard_GoPgqueryVersionSanctioned(t *testing.T) {
	testenv.Isolate(t)
	sanctioned, err := os.ReadFile(sanctionedFilePath)
	if err != nil {
		t.Fatalf("read sanctioned version file: %v", err)
	}
	want := strings.TrimSpace(string(sanctioned))
	if want == "" {
		t.Fatal("sanctioned version file is empty")
	}

	got, err := goModRequireVersion(goModRelPath, pgqueryModulePath)
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if got == "" {
		t.Fatalf("go.mod has no require for %s", pgqueryModulePath)
	}

	if got != want {
		t.Fatalf(`go-pgquery version DIVERGED from the sanctioned epoch version.
  go.mod:      %s
  sanctioned:  %s

N (and hence every content id) is defined by go-pgquery's deparse output.
A bump is a DELIBERATE BREAKING-MAJOR EPOCH EVENT, never an accident. If this
bump is intended, update %s in the SAME commit and follow the epoch-recovery
procedure. Otherwise, pin go-pgquery back to the sanctioned version.`,
			got, want, sanctionedFilePath)
	}
}

// TestPinGuard_DetectsDivergence proves the guard is RED on a divergent version
// (not vacuously green): a go.mod pinning a different version must be detected
// as unequal to the sanctioned string.
func TestPinGuard_DetectsDivergence(t *testing.T) {
	testenv.Isolate(t)
	sanctioned, err := os.ReadFile(sanctionedFilePath)
	if err != nil {
		t.Fatalf("read sanctioned version file: %v", err)
	}
	want := strings.TrimSpace(string(sanctioned))

	tmp := t.TempDir() + "/go.mod"
	divergent := "v0.0.0-29990101000000-deadbeefcafe"
	content := "module example.com/x\n\ngo 1.24\n\nrequire (\n\t" +
		pgqueryModulePath + " " + divergent + "\n)\n"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp go.mod: %v", err)
	}

	got, err := goModRequireVersion(tmp, pgqueryModulePath)
	if err != nil {
		t.Fatalf("parse temp go.mod: %v", err)
	}
	if got != divergent {
		t.Fatalf("parser failed to read version from require block: got %q", got)
	}
	if got == want {
		t.Fatal("expected divergence to be detected, but versions matched")
	}
}

// goModRequireVersion returns the version pinned for modulePath in the given
// go.mod file, or "" if absent. It handles both single-line requires and
// require blocks.
func goModRequireVersion(goModPath, modulePath string) (string, error) {
	f, err := os.Open(goModPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		line = strings.TrimPrefix(line, "require ")
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == modulePath {
			return fields[1], nil
		}
	}
	return "", sc.Err()
}
