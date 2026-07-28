package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
)

// TestMigrateTestShadowChainReplaysEdges pins that `migrate test --shadow`
// against a CHAIN-format project replays the on-disk EDGES (via ApplyChain) into
// the shadow database and diffs the result against the TOML schema (roadmap 5.10).
// A genesis edge produced from the schema must replay cleanly and match, PASS.
func TestMigrateTestShadowChainReplaysEdges(t *testing.T) {
	ephDB := cmdEphemeralDB(t)

	const chainShadowSchema = `format_version = 1
[meta]
schema = "public"

[tables.items]
comment = "items"

[tables.items.columns.id]
type = "id"

[tables.items.columns.name]
type = "short_text"
`
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.toml")
	if err := os.WriteFile(schemaPath, []byte(chainShadowSchema), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build the chain project on disk: a genesis edge for the schema model.
	schema, _, code := parseAndBuild(nil, []string{schemaPath})
	if code != 0 {
		t.Fatalf("parseAndBuild failed: exit %d", code)
	}
	migrationsDir := filepath.Join(dir, "migrations")
	p, err := migrate.OpenChainProject(migrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	d := diff.Diff(schema, &model.Schema{Name: schema.Name, PGVersion: schema.PGVersion})
	m, _ := migrate.GenerateMigration(d, schema, "", extregistry.NewBuiltinRegistry())
	if _, err := migrate.GenerateEdge(p, m, schema, nil, rev.Revision{}, rev.RegistryPresent, "genesis"); err != nil {
		t.Fatalf("GenerateEdge: %v", err)
	}
	if !migrate.IsChainMode(migrationsDir) {
		t.Fatal("project should be chain-mode after GenerateEdge")
	}

	dirFlag := migrationsDir
	code = runMigrateTestShadow(ephDB.URL, &dirFlag, 60, []string{schemaPath}, nil, true)
	if code != 0 {
		t.Fatalf("chain shadow replay should PASS (exit 0), got exit %d", code)
	}
}
