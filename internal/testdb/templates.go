package testdb

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed templates/*.tmpl
var TemplateFS embed.FS

// langTemplates maps language names to template filenames.
var langTemplates = map[string]string{
	"go":     "go.go.tmpl",
	"python": "python.py.tmpl",
	"ts":     "typescript.ts.tmpl",
	"java":   "java.java.tmpl",
	"kotlin": "kotlin.kt.tmpl",
	"zig":    "zig.zig.tmpl",
}

// langOutputPaths maps language names to conventional wrapper output paths.
var langOutputPaths = map[string]string{
	"go":     "internal/testdb/pgdesign_testdb.go",
	"python": "tests/pgdesign_testdb.py",
	"ts":     "test/pgdesign-testdb.ts",
	"java":   "src/test/java/pgdesign/TestDB.java",
	"kotlin": "src/test/kotlin/pgdesign/TestDB.kt",
	"zig":    "src/test/pgdesign_testdb.zig",
}

// SupportedLanguages returns the list of supported language names.
func SupportedLanguages() []string {
	return []string{"go", "python", "ts", "java", "kotlin", "zig"}
}

// RenderTemplate reads a template for the given language and substitutes placeholders.
func RenderTemplate(lang, ddlPath, baseURL, baseName string) ([]byte, error) {
	filename, ok := langTemplates[lang]
	if !ok {
		return nil, fmt.Errorf("unsupported language %q", lang)
	}

	data, err := TemplateFS.ReadFile("templates/" + filename)
	if err != nil {
		return nil, fmt.Errorf("read template for %s: %w", lang, err)
	}

	s := string(data)
	s = strings.ReplaceAll(s, "{{DDL_PATH}}", ddlPath)
	s = strings.ReplaceAll(s, "{{BASE_URL}}", baseURL)
	s = strings.ReplaceAll(s, "{{BASE_NAME}}", baseName)

	return []byte(s), nil
}

// WrapperOutputPath returns the conventional path for a language's test wrapper.
func WrapperOutputPath(lang string) string {
	return langOutputPaths[lang]
}
