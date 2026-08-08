package testdb

import (
	"github.com/smm-h/pgdesign/internal/testenv"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNoHardcodedTestConnStr walks every *_test.go file in the repository
// (excluding testdb/ itself) and fails if any contains a hardcoded database
// connection string or a legacy helper reference.
//
// `localhost:5432` is on the list because a hardcoded localhost DSN is not
// merely untidy: for most of this suite's life every database-backed package
// carried one as the fallback for an unset PGDESIGN_DB, so running `go test`
// on a developer's machine silently created and dropped databases in whatever
// PostgreSQL server was listening there. The connection string now comes from
// PGDESIGN_DB or from the ephemeral cluster TestMain boots, and from nowhere
// else -- this check is what keeps a fallback from growing back.
//
// The walk covers cmd/ as well as internal/: one of the deleted fallbacks
// lived in cmd/pgdesign, where an internal/-only walk could never see it.
func TestNoHardcodedTestConnStr(t *testing.T) {
	testenv.Isolate(t)
	patterns := []string{
		"postgres:///pgdesign_test",
		"localhost:5432",
		"PGDESIGN_TEST_DB",
		"canSetup()",
		"getTestConnStr()",
		"connectTestDB(",
	}

	// Find the project root by walking up from this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile is .../internal/testdb/no_hardcoded_test.go, so the project root
	// is three levels up.
	testdbDir := filepath.Dir(thisFile)
	projectRoot := filepath.Dir(filepath.Dir(testdbDir))

	var violations []string

	err := filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip testdb/ itself -- that package legitimately references these
			// concepts (this very pattern list is one of them) -- and the VCS
			// directory, which is large and holds no Go source.
			if path == testdbDir || info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// Only check test files.
		if !strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(data)

		for _, pat := range patterns {
			if strings.Contains(content, pat) {
				rel, _ := filepath.Rel(projectRoot, path)
				violations = append(violations, rel+": contains "+pat)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the project root: %v", err)
	}

	if len(violations) > 0 {
		t.Errorf("found hardcoded test database patterns (use testdb package instead):\n  %s",
			strings.Join(violations, "\n  "))
	}
}
