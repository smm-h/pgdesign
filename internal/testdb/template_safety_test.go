package testdb

import (
	"regexp"
	"strings"
	"testing"
)

// TestTemplateNoSQLInterpolation scans all wrapper templates for SQL string
// interpolation patterns that should use parameterized queries. This catches
// the class of bug where a database name is interpolated into SQL instead of
// using $1, ?, or %s placeholders.
func TestTemplateNoSQLInterpolation(t *testing.T) {
	// Direct check: datname comparisons must use parameterized queries.
	// This is the specific pattern from the bug: WHERE datname = '<interpolated>'.
	t.Run("datname_parameterized", func(t *testing.T) {
		// Parameterized patterns by language.
		safePatterns := map[string]*regexp.Regexp{
			"go":     regexp.MustCompile(`datname\s*=\s*\$1`),
			"python": regexp.MustCompile(`datname\s*=\s*%s`),
			"ts":     regexp.MustCompile(`datname\s*=\s*\$1`),
			"java":   regexp.MustCompile(`datname\s*=\s*\?`),
			"kotlin": regexp.MustCompile(`datname\s*=\s*\?`),
			"zig":    regexp.MustCompile(`datname\s*=\s*\$1`),
		}

		// Dangerous: datname followed by string interpolation instead of a placeholder.
		dangerousPatterns := []*regexp.Regexp{
			// datname = '{interpolation}' (Python f-string, Zig, etc.)
			regexp.MustCompile(`datname\s*=\s*'\{`),
			// datname = '${...}' (TypeScript/Kotlin template)
			regexp.MustCompile(`datname\s*=\s*'\$\{`),
			// datname = '" + (Java/Kotlin concat)
			regexp.MustCompile(`datname\s*=\s*'"\s*\+`),
			// datname = ' + (string concat)
			regexp.MustCompile(`datname\s*=\s*'\s*\+`),
		}

		for lang, file := range langTemplates {
			t.Run(lang, func(t *testing.T) {
				data, err := TemplateFS.ReadFile("templates/" + file)
				if err != nil {
					t.Fatalf("read template %s: %v", file, err)
				}
				content := string(data)

				// Verify the template contains a datname reference at all (it should,
				// for pg_terminate_backend).
				if !strings.Contains(content, "datname") {
					t.Fatal("template does not contain 'datname' -- expected pg_terminate_backend query")
				}

				// Check that datname uses the language-appropriate parameterized pattern.
				safePat := safePatterns[lang]
				if !safePat.MatchString(content) {
					t.Errorf("datname comparison does not use parameterized query (expected %s)", safePat.String())
				}

				// Check that no dangerous interpolation pattern is present near datname.
				for _, dp := range dangerousPatterns {
					if dp.MatchString(content) {
						t.Errorf("datname comparison uses dangerous interpolation: matched %s", dp.String())
					}
				}
			})
		}
	})

	// Broader check: every line containing a SQL query keyword must not contain
	// raw string interpolation that bypasses parameterization.
	// Safe identifier-escaping patterns are explicitly exempted.
	t.Run("no_query_keyword_interpolation", func(t *testing.T) {
		queryKeywords := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "WHERE", "HAVING"}

		// Per-language: interpolation pattern to flag, and safe patterns to exempt.
		type interpCheck struct {
			lang       string
			pattern    *regexp.Regexp
			safeWords  []string // lines containing any of these words are exempt
		}

		checks := []interpCheck{
			{
				lang:      "zig",
				pattern:   regexp.MustCompile(`\{s\}`),
				safeWords: []string{"escapeIdentifierFmt"},
			},
			{
				lang:      "python",
				pattern:   regexp.MustCompile(`\{[^}]+\}`),
				safeWords: []string{"sql.Identifier", "sql.SQL"},
			},
			{
				lang:      "ts",
				pattern:   regexp.MustCompile(`\$\{`),
				safeWords: []string{"escapeIdentifier"},
			},
			{
				lang:      "java",
				pattern:   regexp.MustCompile(`"\s*\+\s*[a-zA-Z]`),
				safeWords: []string{"escapeIdentifier"},
			},
			{
				lang:      "kotlin",
				pattern:   regexp.MustCompile(`\$\{`),
				safeWords: []string{"escapeIdentifier"},
			},
			{
				lang:      "go",
				pattern:   regexp.MustCompile(`"\s*\+\s*[a-zA-Z]`),
				safeWords: []string{"Sanitize()"},
			},
		}

		for _, ic := range checks {
			t.Run(ic.lang, func(t *testing.T) {
				file := langTemplates[ic.lang]
				data, err := TemplateFS.ReadFile("templates/" + file)
				if err != nil {
					t.Fatalf("read template %s: %v", file, err)
				}
				lines := strings.Split(string(data), "\n")

				for lineNum, line := range lines {
					upper := strings.ToUpper(line)

					// Only check lines containing SQL query keywords.
					hasQueryKeyword := false
					for _, kw := range queryKeywords {
						if strings.Contains(upper, kw) {
							hasQueryKeyword = true
							break
						}
					}
					if !hasQueryKeyword {
						continue
					}

					// Check if the line has language-specific interpolation.
					if !ic.pattern.MatchString(line) {
						continue
					}

					// Exempt if the line contains a safe identifier-escaping call.
					safe := false
					for _, sw := range ic.safeWords {
						if strings.Contains(line, sw) {
							safe = true
							break
						}
					}
					if safe {
						continue
					}

					t.Errorf("line %d: SQL query keyword with unsafe interpolation: %s",
						lineNum+1, strings.TrimSpace(line))
				}
			})
		}
	})
}

// TestTemplateNoPublicName verifies that no template exposes the database name
// as a public/exported field. The name must be private to enforce the opaque
// handle pattern.
func TestTemplateNoPublicName(t *testing.T) {
	type check struct {
		lang    string
		pattern *regexp.Regexp
		desc    string
	}

	checks := []check{
		{"go", regexp.MustCompile(`\bName\s+string\b`), "exported Name field"},
		{"python", regexp.MustCompile(`self\.name\b`), "public self.name (should be self._name)"},
		{"ts", regexp.MustCompile(`(?m)^\s+name\s*:|public\s+name\b`), "public name property"},
		{"java", regexp.MustCompile(`\bgetName\s*\(`), "getName() getter"},
		{"kotlin", regexp.MustCompile(`(?m)^\s+va[lr]\s+name\s*:`), "public name property"},
		{"zig", regexp.MustCompile(`pub\s+(const\s+)?name\b`), "pub name field"},
	}

	for _, c := range checks {
		t.Run(c.lang, func(t *testing.T) {
			file := langTemplates[c.lang]
			data, err := TemplateFS.ReadFile("templates/" + file)
			if err != nil {
				t.Fatalf("read template %s: %v", file, err)
			}
			content := string(data)
			if c.pattern.MatchString(content) {
				t.Errorf("template exposes %s -- database name must be private", c.desc)
			}
		})
	}
}

// TestTemplateNoJSONParsing verifies that no template uses JSON parsing.
// All templates should use the .sqlsplit format instead.
func TestTemplateNoJSONParsing(t *testing.T) {
	jsonPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)json\.parse\b`),
		regexp.MustCompile(`(?i)json\.unmarshal\b`),
		regexp.MustCompile(`(?i)json\.loads\b`),
		regexp.MustCompile(`(?i)parseJsonStringArray\b`),
		regexp.MustCompile(`(?i)extractStatementsArray\b`),
		regexp.MustCompile(`(?i)json\.parseFromSlice\b`),
		regexp.MustCompile(`(?i)"encoding/json"`),
		regexp.MustCompile(`(?i)import\s+json\b`),
		regexp.MustCompile(`\.split\.json\b`),
	}

	for lang, file := range langTemplates {
		t.Run(lang, func(t *testing.T) {
			data, err := TemplateFS.ReadFile("templates/" + file)
			if err != nil {
				t.Fatalf("read template %s: %v", file, err)
			}
			content := string(data)
			for _, pat := range jsonPatterns {
				if pat.MatchString(content) {
					t.Errorf("template contains JSON parsing pattern %s -- should use .sqlsplit format", pat.String())
				}
			}
		})
	}
}

// TestTemplateHasNameGuard verifies that every template validates the ephemeral
// database name against the expected pattern before use.
func TestTemplateHasNameGuard(t *testing.T) {
	for lang, file := range langTemplates {
		t.Run(lang, func(t *testing.T) {
			data, err := TemplateFS.ReadFile("templates/" + file)
			if err != nil {
				t.Fatalf("read template %s: %v", file, err)
			}
			content := string(data)
			if !strings.Contains(content, "refusing to use database") {
				t.Error("template missing name validation guard (expected 'refusing to use database' error message)")
			}
		})
	}
}

// TestTemplateRejectionSampling verifies that templates using raw byte-based
// random generation implement rejection sampling with the correct threshold.
// The threshold 252 = floor(256/36) * 36 eliminates modulo bias when mapping
// random bytes to a 36-character charset (a-z0-9).
//
// Not all templates need rejection sampling: Go uses crypto/rand.Int (no bias),
// Python uses secrets.choice (no bias), and Java/Kotlin use SecureRandom.nextInt
// (no bias). Only Zig and TypeScript generate raw random bytes and must reject
// values >= 252.
func TestTemplateRejectionSampling(t *testing.T) {
	needsRejectionSampling := map[string]bool{
		"zig": true,
		"ts":  true,
	}

	for lang, file := range langTemplates {
		if !needsRejectionSampling[lang] {
			continue
		}
		t.Run(lang, func(t *testing.T) {
			data, err := TemplateFS.ReadFile("templates/" + file)
			if err != nil {
				t.Fatalf("read template %s: %v", file, err)
			}
			content := string(data)

			if !strings.Contains(content, "252") {
				t.Error("template uses raw byte generation but does not contain rejection sampling threshold 252")
			}

			// Also verify the comment explains why 252.
			if !strings.Contains(strings.ToLower(content), "rejection") {
				t.Error("template missing comment explaining rejection sampling")
			}
		})
	}
}
