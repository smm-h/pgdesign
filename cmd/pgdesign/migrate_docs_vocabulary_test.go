package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The hand-authored migration guides (644, not selfdoc-generated). The generated
// cli-migrate.md mirrors the live CLI registration verbatim and is intentionally
// excluded — it is not "retired vocabulary", it is the current flag surface.
var migrateGuideDocs = []string{
	"../../docs/migration-guide.md",
	"../../docs/migration-patterns.md",
}

// retiredVocabulary are the pre-chain patterns that must not survive in the
// hand-authored migration guides (roadmap 5.10 MECHANIZED verify): semver
// migration filenames, file-trusting rollback claims, and --version flag usage.
var retiredVocabulary = []*regexp.Regexp{
	// Semver migration filenames: X.Y.Z.toml, migrations/<version>.toml.
	regexp.MustCompile(`\d+\.\d+\.\d+\.toml`),
	regexp.MustCompile(`migrations/<version>\.toml`),
	// The --version flag (retired: generation is pure; adoption is baseline).
	regexp.MustCompile(`--version`),
	// File-trusting rollback claims (rollback reads the JOURNAL, not files).
	regexp.MustCompile(`(?i)loads?\s+(?:and parses\s+)?the migration file`),
	regexp.MustCompile(`(?i)migration file from disk`),
	// Applying in semver order (retired: the path-finder orders by the chain).
	regexp.MustCompile(`(?i)in semver order`),
}

// TestMigrateGuidesHaveNoRetiredVocabulary asserts the chain migration model is
// the documented reality: no semver filenames, no file-trusting rollback, no
// --version flag references in the hand-authored migration guides.
func TestMigrateGuidesHaveNoRetiredVocabulary(t *testing.T) {
	for _, doc := range migrateGuideDocs {
		data, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("read %s: %v", doc, err)
		}
		text := string(data)
		for _, re := range retiredVocabulary {
			if loc := re.FindString(text); loc != "" {
				t.Errorf("%s contains retired vocabulary %q (pattern %s); update it to the chain reality",
					filepath.Base(doc), loc, re.String())
			}
		}
	}
}

// TestMigrateGuideCommandsAreRegistered asserts every `migrate <subcommand>` the
// guides reference exists in the CLI registration (roadmap 5.10 doc greps).
func TestMigrateGuideCommandsAreRegistered(t *testing.T) {
	handlers, err := os.ReadFile("handlers_migrate.go")
	if err != nil {
		t.Fatalf("read handlers_migrate.go: %v", err)
	}
	registered := handlers // g.Command("<name>", ...) registrations live here

	cmdRe := regexp.MustCompile(`migrate ([a-z]+)`)
	// Words that follow "migrate " but are not subcommands.
	notCommands := map[string]bool{
		"command": true, "commands": true, "operations": true, "operation": true,
		"tracking": true, "file": true, "files": true, "system": true,
		"phase": true, "phases": true, "chain": true, "edge": true, "edges": true,
	}
	seen := map[string]bool{}
	for _, doc := range migrateGuideDocs {
		data, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("read %s: %v", doc, err)
		}
		for _, m := range cmdRe.FindAllStringSubmatch(string(data), -1) {
			sub := m[1]
			if notCommands[sub] || seen[sub] {
				continue
			}
			seen[sub] = true
			needle := `g.Command("` + sub + `"`
			if !strings.Contains(string(registered), needle) {
				t.Errorf("migration guide references `migrate %s` but no %s registration exists in handlers_migrate.go", sub, needle)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("expected the guides to reference at least one migrate subcommand")
	}
}
