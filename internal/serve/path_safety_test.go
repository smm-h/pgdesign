package serve

import (
	"path/filepath"
	"testing"
)

// TestMigrationVersionPath_Traversal verifies the migration-version endpoint's
// path resolution rejects traversal attempts and accepts normal versions.
func TestMigrationVersionPath_Traversal(t *testing.T) {
	dir := "/srv/app/migrations"

	rejected := []string{
		"../../etc/passwd",
		"..",
		"../secrets",
		"0.1.0/../../../etc/passwd",
		"foo/bar",
		`..\..\windows`,
		"",
	}
	for _, v := range rejected {
		if _, ok := migrationVersionPath(dir, v); ok {
			t.Errorf("version %q should be rejected as a traversal/invalid path", v)
		}
	}

	accepted := map[string]string{
		"0.1.0":  filepath.Join(dir, "0.1.0.toml"),
		"1.2.3":  filepath.Join(dir, "1.2.3.toml"),
		"0.10.0": filepath.Join(dir, "0.10.0.toml"),
	}
	for v, want := range accepted {
		got, ok := migrationVersionPath(dir, v)
		if !ok {
			t.Errorf("version %q should be accepted", v)
			continue
		}
		if got != want {
			t.Errorf("version %q -> %q, want %q", v, got, want)
		}
	}
}
