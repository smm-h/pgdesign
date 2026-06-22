package testdb

import (
	"strings"
	"testing"
)

func TestRenderTemplate(t *testing.T) {
	for _, lang := range []string{"go", "python", "ts", "java", "kotlin", "zig"} {
		t.Run(lang, func(t *testing.T) {
			out, err := RenderTemplate(lang, "schema.sql.split.json", "postgres://localhost/mydb", "mydb")
			if err != nil {
				t.Fatalf("RenderTemplate(%q): %v", lang, err)
			}
			s := string(out)
			// Verify placeholders are replaced.
			if strings.Contains(s, "{{DDL_PATH}}") {
				t.Error("unreplaced {{DDL_PATH}}")
			}
			if strings.Contains(s, "{{BASE_URL}}") {
				t.Error("unreplaced {{BASE_URL}}")
			}
			if strings.Contains(s, "{{BASE_NAME}}") {
				t.Error("unreplaced {{BASE_NAME}}")
			}
			// Verify substituted values are present.
			if !strings.Contains(s, "schema.sql.split.json") {
				t.Error("DDL path not in output")
			}
			if !strings.Contains(s, "postgres://localhost/mydb") {
				t.Error("base URL not in output")
			}
			if !strings.Contains(s, "mydb") {
				t.Error("base name not in output")
			}
			// Verify protocol version.
			if !strings.Contains(s, "protocol v1") {
				t.Error("protocol version not in output")
			}
		})
	}
}

func TestRenderTemplateGoNameAssertion(t *testing.T) {
	out, err := RenderTemplate("go", "schema.sql.split.json", "postgres://localhost/mydb", "mydb")
	if err != nil {
		t.Fatalf("RenderTemplate(go): %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "panic(") {
		t.Error("Go template missing panic call for name length assertion")
	}
	if !strings.Contains(s, "exceeds 63 bytes") {
		t.Error("Go template missing 'exceeds 63 bytes' assertion message")
	}
}

func TestRenderTemplateUnsupportedLang(t *testing.T) {
	_, err := RenderTemplate("ruby", "x.sql", "postgres://localhost/db", "db")
	if err == nil {
		t.Fatal("expected error for unsupported language")
	}
	if !strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWrapperOutputPath(t *testing.T) {
	tests := map[string]string{
		"go":     "internal/testdb/pgdesign_testdb.go",
		"python": "tests/pgdesign_testdb.py",
		"ts":     "test/pgdesign-testdb.ts",
		"java":   "src/test/java/pgdesign/TestDB.java",
		"kotlin": "src/test/kotlin/pgdesign/TestDB.kt",
		"zig":    "src/test/pgdesign_testdb.zig",
	}
	for lang, expected := range tests {
		if got := WrapperOutputPath(lang); got != expected {
			t.Errorf("WrapperOutputPath(%q) = %q, want %q", lang, got, expected)
		}
	}
}

func TestWrapperOutputPathUnknown(t *testing.T) {
	if got := WrapperOutputPath("ruby"); got != "" {
		t.Errorf("WrapperOutputPath(%q) = %q, want empty", "ruby", got)
	}
}

func TestSupportedLanguages(t *testing.T) {
	langs := SupportedLanguages()
	if len(langs) != 6 {
		t.Errorf("expected 6 supported languages, got %d", len(langs))
	}
	// Each supported language should have a template and an output path.
	for _, lang := range langs {
		if _, ok := langTemplates[lang]; !ok {
			t.Errorf("language %q missing from langTemplates", lang)
		}
		if _, ok := langOutputPaths[lang]; !ok {
			t.Errorf("language %q missing from langOutputPaths", lang)
		}
	}
}
