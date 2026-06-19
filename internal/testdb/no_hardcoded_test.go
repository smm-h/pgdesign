package testdb

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNoHardcodedTestConnStr walks all *_test.go files under internal/
// (excluding testdb/ itself) and fails if any contain hardcoded test
// database connection strings or legacy helper references. This prevents
// re-introduction of patterns that were removed during the test
// infrastructure migration to the testdb package.
func TestNoHardcodedTestConnStr(t *testing.T) {
	patterns := []string{
		"postgres:///pgdesign_test",
		"postgres://localhost:5432/pgdesign_test",
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
	// thisFile is .../internal/testdb/no_hardcoded_test.go
	// project root is two levels up from internal/testdb/
	internalDir := filepath.Dir(filepath.Dir(thisFile))

	testdbDir := filepath.Dir(thisFile)

	var violations []string

	err := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		// Only check test files.
		if !strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		// Skip files inside testdb/ -- that package legitimately
		// references these concepts.
		if strings.HasPrefix(path, testdbDir+string(os.PathSeparator)) || path == testdbDir {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(data)

		for _, pat := range patterns {
			if strings.Contains(content, pat) {
				rel, _ := filepath.Rel(internalDir, path)
				violations = append(violations, rel+": contains "+pat)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	if len(violations) > 0 {
		t.Errorf("found hardcoded test database patterns (use testdb package instead):\n  %s",
			strings.Join(violations, "\n  "))
	}
}
