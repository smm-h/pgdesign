package testdb

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/testenv"
)

// bannedPattern is one piece of text no Go source in this repository may
// contain, plus the reason it is banned and the scope it is banned in.
type bannedPattern struct {
	// text is matched literally against each line of source.
	text string
	// testsOnly restricts the ban to *_test.go files. A pattern is testsOnly
	// when the production code has a legitimate use for it -- the CLI really
	// does build a testdb.Manager from a --db flag -- but a test must not.
	testsOnly bool
	// why explains the ban in the failure message, so the next agent to trip it
	// learns the rule rather than deleting the guard.
	why string
}

// bannedPatterns is the list this guard enforces.
//
// `localhost:5432` is on it because a hardcoded localhost DSN is not merely
// untidy: for most of this suite's life every database-backed package carried
// one as the fallback for an unset PGDESIGN_DB, so running `go test` on a
// developer's machine silently created and dropped databases in whatever
// PostgreSQL server was listening there. The connection string now comes from
// PGDESIGN_DB or from the ephemeral cluster TestMain boots, and from nowhere
// else -- this check is what keeps a fallback from growing back.
var bannedPatterns = []bannedPattern{
	{text: "postgres:///pgdesign_test", why: "a hardcoded DSN: resolve the database through testdb.RequireURL"},
	{text: "localhost:5432", why: "a hardcoded DSN: resolve the database through testdb.RequireURL"},
	{text: "PGDESIGN_TEST_DB", why: "a second connection env: PGDESIGN_DB is the only one"},
	{text: "canSetup()", why: "a legacy skip helper: use testdb.SkipIfNoPostgres"},
	{text: "getTestConnStr()", why: "a legacy DSN helper: use testdb.RequireURL"},
	{text: "connectTestDB(", why: "a legacy connect helper: use testdb.RequireConn"},
}

// allowedLines names the exact source lines that may contain a banned pattern,
// keyed by repository-relative path.
//
// It is a line-by-line allowlist and never a directory exemption. This guard's
// own origin bug -- the default DSN that made the suite dial a developer's
// server -- lived in internal/testdb/skip.go, a NON-test file inside the
// directory the guard used to skip wholesale, so the one place a regression was
// certain to hide was the one place the walk could not see. Every entry below
// is a literal that is parsed, rendered, or quoted, and never dialed; if a line
// here changes, the guard fails and the change gets read.
var allowedLines = map[string][]string{
	// A doc-comment example on SwapDatabase. Prose, not a target: the function
	// rewrites the database name in a URL its caller supplies and never dials.
	"internal/dbutil/dbutil.go": {
		`// Example: SwapDatabase("postgres://localhost:5432/myapp", "otherdb") returns "postgres://localhost:5432/otherdb"`,
	},
	// NewManager parses and rewrites a URL and never connects (Create and Drop
	// are what dial). These two tests assert the connect_timeout it injects into
	// the parsed form, so the literal is input to url.Parse and nothing more.
	"internal/testdb/connect_timeout_test.go": {
		`mgr, err := NewManager("postgres://localhost:5432/pgdesign?sslmode=disable")`,
		`mgr, err := NewManager("postgres://localhost:5432/pgdesign?sslmode=disable&connect_timeout=2")`,
	},
	// Input to RenderTemplate. The rendered wrapper is inspected as text -- the
	// assertion is that the JDBC URL it builds strips userinfo -- and no
	// connection is opened to it.
	"internal/testdb/template_safety_test.go": {
		`baseURL := "postgres://testuser:testpass@localhost:5432/mydb"`,
	},
}

// scanHardcodedDSNs walks root and returns one violation per source line that
// contains a banned pattern and is not allowlisted.
//
// It reads EVERY .go file, not only tests, because the fallback this guard
// exists to prevent lived in production-shaped code (internal/testdb/skip.go)
// that tests merely called. selfRel is the repository-relative path of the
// guard's own source, which is excluded because it IS the pattern list.
func scanHardcodedDSNs(root, selfRel string) ([]string, error) {
	var violations []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// .git holds no Go source and is large; node_modules is vendored
			// JavaScript. Neither is an exemption for pgdesign's own code.
			if info.Name() == ".git" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == selfRel {
			return nil
		}
		isTest := strings.HasSuffix(info.Name(), "_test.go")

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		allowed := allowedLines[filepath.ToSlash(rel)]
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			for _, pat := range bannedPatterns {
				if pat.testsOnly && !isTest {
					continue
				}
				if !strings.Contains(line, pat.text) {
					continue
				}
				if allowedContains(allowed, trimmed) {
					continue
				}
				violations = append(violations, fmt.Sprintf("%s:%d: %s -- %s",
					filepath.ToSlash(rel), i+1, pat.text, pat.why))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return violations, nil
}

func allowedContains(allowed []string, trimmed string) bool {
	for _, a := range allowed {
		if a == trimmed {
			return true
		}
	}
	return false
}

// projectRoot returns the repository root, derived from this file's own path.
func projectRoot(t testing.TB) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile is <root>/internal/testdb/no_hardcoded_test.go.
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

// guardSelfRel is this guard's own repository-relative path. It is the one file
// the walk skips, because it is where the banned patterns are written down.
const guardSelfRel = "internal/testdb/no_hardcoded_test.go"

// TestNoHardcodedTestConnStr scans every Go file in the repository -- test and
// non-test alike, internal/testdb included -- for hardcoded connection strings
// and legacy helper references.
func TestNoHardcodedTestConnStr(t *testing.T) {
	testenv.Isolate(t)

	violations, err := scanHardcodedDSNs(projectRoot(t), filepath.FromSlash(guardSelfRel))
	if err != nil {
		t.Fatalf("walking the project root: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("found banned test-database patterns (resolve the database through the testdb package):\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// TestScanSeesNonTestSourceInsideTestdb is the regression test for the guard's
// own blind spot. The default DSN that made this suite dial a developer's own
// PostgreSQL lived in internal/testdb/skip.go: a non-test file, inside the one
// directory the walk skipped wholesale and outside the *_test.go suffix it
// matched on. Two exemptions stacked so that the guard could not have caught a
// regression of the exact bug it was written for.
func TestScanSeesNonTestSourceInsideTestdb(t *testing.T) {
	testenv.Isolate(t)
	root := t.TempDir()

	// The historical shape, verbatim: a package-level default in testdb's own
	// non-test source.
	plant(t, root, "internal/testdb/skip.go", `package testdb

const defaultTestDB = "postgres://localhost:5432/pgdesign?sslmode=disable"
`)
	// A regular test file elsewhere, which the old walk did cover.
	plant(t, root, "internal/migrate/apply_db_test.go", `package migrate

func canSetup() bool { return true }
`)
	// A file the guard must NOT flag.
	plant(t, root, "internal/model/model.go", `package model

const Name = "pgdesign"
`)

	violations, err := scanHardcodedDSNs(root, filepath.FromSlash(guardSelfRel))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	joined := strings.Join(violations, "\n")
	if !strings.Contains(joined, "internal/testdb/skip.go") {
		t.Errorf("the scan did not see a default DSN in internal/testdb/skip.go -- "+
			"the guard cannot catch a regression of its own origin bug.\nviolations:\n%s", joined)
	}
	if !strings.Contains(joined, "internal/migrate/apply_db_test.go") {
		t.Errorf("the scan missed a legacy helper in a test file:\n%s", joined)
	}
	if strings.Contains(joined, "internal/model/model.go") {
		t.Errorf("the scan flagged a clean file:\n%s", joined)
	}
	if len(violations) != 2 {
		t.Errorf("expected exactly 2 violations, got %d:\n%s", len(violations), joined)
	}
}

// TestAllowlistIsLineScopedNotDirectoryScoped pins that the allowlist cannot be
// used to exempt a file: an allowlisted file with a NEW offending line still
// fails.
func TestAllowlistIsLineScopedNotDirectoryScoped(t *testing.T) {
	testenv.Isolate(t)
	root := t.TempDir()

	plant(t, root, "internal/testdb/connect_timeout_test.go", `package testdb

func x() {
	mgr, err := NewManager("postgres://localhost:5432/pgdesign?sslmode=disable")
	other := "postgres://localhost:5432/sneaked_back_in"
	_, _, _ = mgr, err, other
}
`)

	violations, err := scanHardcodedDSNs(root, filepath.FromSlash(guardSelfRel))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected exactly the non-allowlisted line to be flagged, got %d:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
	if !strings.Contains(violations[0], ":5:") {
		t.Errorf("the flagged line is not the newly added one: %s", violations[0])
	}
}

// plant writes content to rel under root, creating parent directories.
func plant(t testing.TB, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
