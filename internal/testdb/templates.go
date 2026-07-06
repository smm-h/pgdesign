package testdb

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed templates/*.tmpl
var TemplateFS embed.FS

//go:embed templates/ci/*.tmpl
var CITemplateFS embed.FS

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

// ciTemplates maps CI provider names to template filenames.
var ciTemplates = map[string]string{
	"github-actions": "github-actions.yml.tmpl",
}

// CITemplateOptions configures CI template rendering beyond basic placeholders.
type CITemplateOptions struct {
	// Partman adds pg_partman installation steps to the CI workflow.
	// When true, a docker exec step installs postgresql-<version>-partman
	// inside the postgres service container.
	Partman bool
}

// RenderCITemplate reads a CI workflow template for the given provider and
// substitutes placeholders. Only "github-actions" is supported.
func RenderCITemplate(provider, pgVersion string, languages []string, opts CITemplateOptions) ([]byte, error) {
	filename, ok := ciTemplates[provider]
	if !ok {
		return nil, fmt.Errorf("unsupported CI provider %q (supported: github-actions)", provider)
	}

	data, err := CITemplateFS.ReadFile("templates/ci/" + filename)
	if err != nil {
		return nil, fmt.Errorf("read CI template for %s: %w", provider, err)
	}

	s := string(data)
	s = strings.ReplaceAll(s, "{{PG_VERSION}}", pgVersion)
	s = strings.ReplaceAll(s, "{{LANGUAGES}}", strings.Join(languages, ", "))

	if opts.Partman {
		partmanBlock := fmt.Sprintf(`
      - name: Install pg_partman extension
        run: |
          docker exec ${{ job.services.postgres.id }} bash -c \
            "apt-get update && apt-get install -y --no-install-recommends postgresql-%s-partman && rm -rf /var/lib/apt/lists/*"
`, pgVersion)
		s = strings.ReplaceAll(s, "{{PARTMAN_INSTALL}}", partmanBlock)
	} else {
		s = strings.ReplaceAll(s, "{{PARTMAN_INSTALL}}", "")
	}

	return []byte(s), nil
}
