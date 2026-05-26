package test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
)

var update = flag.Bool("update", false, "update golden files")

// projectRoot returns the absolute path to the project root by walking up from
// the test file's directory until we find go.mod.
func projectRoot(t *testing.T) string {
	t.Helper()
	// Start from the working directory (go test runs in the package dir).
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("cannot find project root (no go.mod found)")
		}
		dir = parent
	}
}

// normalize trims trailing whitespace from each line and ensures a single
// trailing newline, so minor whitespace differences don't cause false failures.
func normalize(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	// Remove trailing empty lines, then re-add exactly one trailing newline.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestGolden(t *testing.T) {
	root := projectRoot(t)
	schemasDir := filepath.Join(root, "testdata", "schemas")
	expectedDir := filepath.Join(root, "testdata", "expected")

	entries, err := os.ReadDir(schemasDir)
	if err != nil {
		t.Fatalf("cannot read schemas dir %s: %v", schemasDir, err)
	}

	var tomlFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			tomlFiles = append(tomlFiles, e)
		}
	}
	if len(tomlFiles) == 0 {
		t.Fatal("no .toml files found in testdata/schemas/")
	}

	for _, entry := range tomlFiles {
		name := strings.TrimSuffix(entry.Name(), ".toml")
		t.Run(name, func(t *testing.T) {
			inputPath := filepath.Join(schemasDir, entry.Name())
			expectedPath := filepath.Join(expectedDir, name+".sql")

			// Parse
			raw, diags := parse.File(inputPath)
			if raw == nil {
				t.Fatalf("parse returned nil: %v", diags)
			}
			for _, d := range diags {
				if d.Severity == 0 { // Error
					t.Fatalf("parse error: %s", d.Message)
				}
			}

			// Build
			reg := semtype.NewBuiltinRegistry()
			schema, buildDiags := model.Build(raw, reg)
			if buildDiags.HasErrors() {
				t.Fatalf("build errors: %v", buildDiags)
			}

			// Generate
			opts := generate.Options{IncludeComments: true, Format: "sql"}
			got := generate.Generate(schema, opts)

			// Update mode: overwrite the golden file and return.
			if *update {
				if err := os.MkdirAll(expectedDir, 0o755); err != nil {
					t.Fatalf("cannot create expected dir: %v", err)
				}
				if err := os.WriteFile(expectedPath, []byte(got), 0o644); err != nil {
					t.Fatalf("cannot write golden file: %v", err)
				}
				t.Logf("updated %s", expectedPath)
				return
			}

			// Read expected
			expectedBytes, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("cannot read expected file %s (run with -update to create): %v", expectedPath, err)
			}

			expected := string(expectedBytes)

			// Normalize and compare
			gotNorm := normalize(got)
			expectedNorm := normalize(expected)

			if gotNorm != expectedNorm {
				// Produce a line-by-line diff for clarity.
				gotLines := strings.Split(gotNorm, "\n")
				expLines := strings.Split(expectedNorm, "\n")

				var diff strings.Builder
				maxLines := len(gotLines)
				if len(expLines) > maxLines {
					maxLines = len(expLines)
				}
				for i := 0; i < maxLines; i++ {
					gl, el := "", ""
					if i < len(gotLines) {
						gl = gotLines[i]
					}
					if i < len(expLines) {
						el = expLines[i]
					}
					if gl != el {
						diff.WriteString("  line ")
						diff.WriteString(strings.Repeat(" ", 4-len(string(rune('0'+i%10)))))
						diff.WriteString(itoa(i + 1))
						diff.WriteString(":\n")
						diff.WriteString("    got:      ")
						diff.WriteString(gl)
						diff.WriteString("\n")
						diff.WriteString("    expected: ")
						diff.WriteString(el)
						diff.WriteString("\n")
					}
				}

				t.Errorf("golden file mismatch for %s:\n%s", name, diff.String())
			}
		})
	}
}

// itoa converts an int to a string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
