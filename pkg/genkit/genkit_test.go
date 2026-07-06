package genkit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompareFreshness_AllStates(t *testing.T) {
	dir := t.TempDir()
	freshPath := filepath.Join(dir, "fresh.go")
	stalePath := filepath.Join(dir, "stale.go")
	missingPath := filepath.Join(dir, "missing.go")

	freshContent := []byte("// fresh\n")
	staleContent := []byte("// planned\n")
	missingContent := []byte("// new\n")

	if err := os.WriteFile(freshPath, freshContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stalePath, []byte("// old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		freshPath:   freshContent,
		stalePath:   staleContent,
		missingPath: missingContent,
	}
	r := CompareFreshness(files)

	if len(r.Fresh) != 1 || r.Fresh[0] != freshPath {
		t.Errorf("expected 1 fresh file, got %v", r.Fresh)
	}
	if len(r.Stale) != 1 || r.Stale[0] != stalePath {
		t.Errorf("expected 1 stale file, got %v", r.Stale)
	}
	if len(r.Missing) != 1 || r.Missing[0] != missingPath {
		t.Errorf("expected 1 missing file, got %v", r.Missing)
	}
	if !r.HasProblems() {
		t.Error("expected HasProblems() = true")
	}
}

func TestCompareFreshness_AllFresh(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ok.go")
	data := []byte("// ok\n")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}

	r := CompareFreshness(map[string][]byte{p: data})
	if r.HasProblems() {
		t.Error("expected no problems")
	}
	if len(r.Fresh) != 1 {
		t.Errorf("expected 1 fresh, got %d", len(r.Fresh))
	}
}

func TestOrphanIgnored(t *testing.T) {
	tests := []struct {
		rel  string
		want bool
	}{
		{"__pycache__/foo.pyc", true},
		{"sub/__pycache__/x.pyc", true},
		{"compiled.pyc", true},
		{"main.py", false},
		{"__pycache__", true}, // the directory name itself as a path component
		{"data.json", false},
	}
	for _, tt := range tests {
		if got := OrphanIgnored(tt.rel); got != tt.want {
			t.Errorf("OrphanIgnored(%q) = %v, want %v", tt.rel, got, tt.want)
		}
	}
}

func TestScanOrphans(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel string, data string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("owned.py", "# owned")
	mustWrite("orphan.py", "# orphan")
	mustWrite("compiled.pyc", "bytecode")
	mustWrite("__pycache__/foo.cpython-312.pyc", "bytecode")

	orphans, err := ScanOrphans(dir, map[string]bool{"owned.py": true})
	if err != nil {
		t.Fatalf("ScanOrphans: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != filepath.Join(dir, "orphan.py") {
		t.Errorf("expected [orphan.py], got %v", orphans)
	}
}

func TestScanOrphans_MissingDirectory(t *testing.T) {
	orphans, err := ScanOrphans(filepath.Join(t.TempDir(), "nonexistent"), nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("expected no orphans, got %v", orphans)
	}
}

func TestScanAllOrphans(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir1, "owned.py"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir1, "orphan1.py"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "orphan2.py"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}

	ownedDirs := map[string]map[string]bool{
		dir1: {"owned.py": true},
		dir2: {},
	}
	orphans, err := ScanAllOrphans(ownedDirs)
	if err != nil {
		t.Fatalf("ScanAllOrphans: %v", err)
	}
	if len(orphans) != 2 {
		t.Errorf("expected 2 orphans, got %v", orphans)
	}
}
