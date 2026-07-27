package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/smm-h/pgdesign/internal/config"
)

// writeGroupedProject creates a temp project with two tables assigned to two
// groups plus a build [output] that emits Go constants filtered to the "core"
// group. Returns (projectRoot, buildOutputAbsPath).
func writeGroupedProject(t *testing.T) (string, string) {
	t.Helper()
	config.CodegenModes = SupportedModes()

	dir := t.TempDir()
	schema := `format_version = 1
[meta]
schema = "parity"

[tables.users]
comment = "Core users table"

[tables.users.columns.id]
type = "id"

[tables.users.columns.name]
type = "short_text"

[tables.audit_log]
comment = "Peripheral audit log"

[tables.audit_log.columns.id]
type = "id"

[tables.audit_log.columns.note]
type = "short_text"

[groups]
core = ["users"]
extra = ["audit_log"]
`
	if err := os.WriteFile(filepath.Join(dir, "schema.toml"), []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema.toml: %v", err)
	}

	buildOut := filepath.Join(dir, "build_out", "tables.go")
	cfg := fmt.Sprintf(`[project]
schemas = ["schema.toml"]

[database]
pg_version = 16

[output.consts]
format = "codegen"
path = %q
lang = "go"
mode = "constants"
groups = ["core"]
`, buildOut)
	if err := os.WriteFile(filepath.Join(dir, "pgdesign.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write pgdesign.toml: %v", err)
	}
	return dir, buildOut
}

// TestCodegenBuildParity_GroupFilter pins that the standalone `codegen` command
// and `build` produce byte-identical output for the same artifact, INCLUDING
// under a group filter. Before consolidation, standalone codegen ignored
// FilterByGroups/FilterBySource, so the same artifact had two contents
// depending on the entry point.
func TestCodegenBuildParity_GroupFilter(t *testing.T) {
	dir, buildOut := writeGroupedProject(t)
	schemaPath := filepath.Join(dir, "schema.toml")
	cfgPath := filepath.Join(dir, "pgdesign.toml")

	// Build (canonical): writes the group-filtered constants file.
	if code := runBuild(&cfgPath, true, false, false); code != 0 {
		t.Fatalf("runBuild exited %d", code)
	}
	buildContent, err := os.ReadFile(buildOut)
	if err != nil {
		t.Fatalf("read build output: %v", err)
	}

	// Standalone codegen with the same group filter, to a separate path whose
	// parent already exists (isolating the content divergence from directory
	// creation).
	codegenOut := filepath.Join(dir, "codegen_tables.go")
	kwargs := map[string]interface{}{
		"path":       []interface{}{schemaPath},
		"lang":       "go",
		"mode":       "constants",
		"check":      false,
		"split_mode": nil,
		"output":     codegenOut,
		"groups":     []interface{}{"core"},
		"source":     nil,
	}
	if code := runCodegen(&cfgPath, true, kwargs); code != 0 {
		t.Fatalf("runCodegen exited %d", code)
	}
	codegenContent, err := os.ReadFile(codegenOut)
	if err != nil {
		t.Fatalf("read codegen output: %v", err)
	}

	if string(buildContent) != string(codegenContent) {
		t.Errorf("standalone codegen and build must be byte-identical under a group filter.\nbuild:\n%s\ncodegen:\n%s", buildContent, codegenContent)
	}

	// Sanity: the filtered artifact must exclude the peripheral table.
	if wantExclude := "audit_log"; contains(string(buildContent), wantExclude) {
		t.Errorf("group-filtered build output should not mention %q", wantExclude)
	}
}

// TestCoLocatedGoEnumDeclaredOnce verifies that in a VALID Go co-location — a
// struct provider (`types`) plus a `constraints` output in one package directory
// — the branded enum block is declared EXACTLY ONCE across the two files. This is
// now structural, not a runtime dedup: only the struct provider emits the enum
// block; `constraints` emits `package schema` but no row structs or enums, it
// only references them (Role.String()/.IsValid()). The two struct providers
// (`types` and `gorm`) can never share a directory (that is a hard error — they
// define the same row structs), so there is no configuration in which two
// generators both emit the enum block into one package.
//
// (This test was reworked from a former types+gorm "enum dedup" test; that pair
// is now a hard error because it produces duplicate row structs, so the dedup
// mechanism it exercised was removed. The surviving invariant — one enum
// declaration per valid co-located package — is what this test now pins.)
func TestCoLocatedGoEnumDeclaredOnce(t *testing.T) {
	config.CodegenModes = SupportedModes()
	dir := t.TempDir()
	schema := `format_version = 1
[meta]
schema = "cogen"

[types.role]
kind = "enum"
values = ["admin", "user", "guest"]
comment = "Account role"

[tables.accounts]
comment = "Accounts"

[tables.accounts.columns.id]
type = "id"

[tables.accounts.columns.role]
type = "role"
`
	if err := os.WriteFile(filepath.Join(dir, "schema.toml"), []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema.toml: %v", err)
	}
	pkgDir := filepath.Join(dir, "schemapkg")
	typesOut := filepath.Join(pkgDir, "types.go")
	constraintsOut := filepath.Join(pkgDir, "constraints.go")
	cfg := fmt.Sprintf(`[project]
schemas = ["schema.toml"]

[database]
pg_version = 16

[output.gotypes]
format = "codegen"
path = %q
lang = "go"
mode = "types"

[output.goconstraints]
format = "codegen"
path = %q
lang = "go"
mode = "constraints"
`, typesOut, constraintsOut)
	cfgPath := filepath.Join(dir, "pgdesign.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write pgdesign.toml: %v", err)
	}
	if code := runBuild(&cfgPath, true, false, false); code != 0 {
		t.Fatalf("runBuild exited %d", code)
	}
	typesContent, err := os.ReadFile(typesOut)
	if err != nil {
		t.Fatalf("read types output: %v", err)
	}
	constraintsContent, err := os.ReadFile(constraintsOut)
	if err != nil {
		t.Fatalf("read constraints output: %v", err)
	}
	combined := string(typesContent) + string(constraintsContent)
	if n := countSubstr(combined, "type Role struct"); n != 1 {
		t.Fatalf("branded enum Role must be declared exactly once across the co-located package, got %d\ntypes:\n%s\nconstraints:\n%s", n, typesContent, constraintsContent)
	}
	// Prove the co-location is genuine: the constraints file references the
	// branded enum the types file defines (so the single declaration is actually
	// consumed in-package, not merely present-once by accident).
	if !contains(string(constraintsContent), "row.Role.String()") {
		t.Fatalf("constraints file should reference the branded enum Role by bare name, got:\n%s", constraintsContent)
	}
}

// countSubstr counts non-overlapping occurrences of sub in s.
func countSubstr(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub)
		} else {
			i++
		}
	}
	return n
}

// writeSourcedProject creates a temp project with two schema files, each
// contributing one table, plus a build [output] that emits Go constants
// filtered to the first source file via `source`. Returns (projectRoot,
// buildOutputAbsPath, schemaFilePaths).
func writeSourcedProject(t *testing.T) (string, string, []string) {
	t.Helper()
	config.CodegenModes = SupportedModes()

	dir := t.TempDir()
	schemaA := `format_version = 1
[meta]
schema = "core"

[tables.users]
comment = "Core users table"

[tables.users.columns.id]
type = "id"

[tables.users.columns.name]
type = "short_text"
`
	schemaB := `format_version = 1
[meta]
schema = "extra"

[tables.audit_log]
comment = "Peripheral audit log"

[tables.audit_log.columns.id]
type = "id"

[tables.audit_log.columns.note]
type = "short_text"
`
	pathA := filepath.Join(dir, "schema_a.toml")
	pathB := filepath.Join(dir, "schema_b.toml")
	if err := os.WriteFile(pathA, []byte(schemaA), 0o644); err != nil {
		t.Fatalf("write schema_a.toml: %v", err)
	}
	if err := os.WriteFile(pathB, []byte(schemaB), 0o644); err != nil {
		t.Fatalf("write schema_b.toml: %v", err)
	}

	buildOut := filepath.Join(dir, "build_out", "tables.go")
	cfg := fmt.Sprintf(`[project]
schemas = ["schema_a.toml", "schema_b.toml"]

[database]
pg_version = 16

[output.consts]
format = "codegen"
path = %q
lang = "go"
mode = "constants"
source = ["schema_a.toml"]
`, buildOut)
	if err := os.WriteFile(filepath.Join(dir, "pgdesign.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write pgdesign.toml: %v", err)
	}
	return dir, buildOut, []string{pathA, pathB}
}

// TestCodegenBuildParity_SourceFilter is the source-filter sibling of
// TestCodegenBuildParity_GroupFilter: it pins that standalone `codegen` and
// `build` produce byte-identical output for the same artifact under a `source`
// filter. Like the group case, standalone codegen once ignored
// FilterByGroups/FilterBySource, so the same artifact had two contents
// depending on the entry point.
func TestCodegenBuildParity_SourceFilter(t *testing.T) {
	dir, buildOut, schemaPaths := writeSourcedProject(t)
	cfgPath := filepath.Join(dir, "pgdesign.toml")

	// Build (canonical): writes the source-filtered constants file.
	if code := runBuild(&cfgPath, true, false, false); code != 0 {
		t.Fatalf("runBuild exited %d", code)
	}
	buildContent, err := os.ReadFile(buildOut)
	if err != nil {
		t.Fatalf("read build output: %v", err)
	}

	// Standalone codegen with the same source filter, over both schema files,
	// to a separate path whose parent already exists.
	codegenOut := filepath.Join(dir, "codegen_tables.go")
	kwargs := map[string]interface{}{
		"path":       []interface{}{schemaPaths[0], schemaPaths[1]},
		"lang":       "go",
		"mode":       "constants",
		"check":      false,
		"split_mode": nil,
		"output":     codegenOut,
		"groups":     nil,
		"source":     []interface{}{"schema_a.toml"},
	}
	if code := runCodegen(&cfgPath, true, kwargs); code != 0 {
		t.Fatalf("runCodegen exited %d", code)
	}
	codegenContent, err := os.ReadFile(codegenOut)
	if err != nil {
		t.Fatalf("read codegen output: %v", err)
	}

	if string(buildContent) != string(codegenContent) {
		t.Errorf("standalone codegen and build must be byte-identical under a source filter.\nbuild:\n%s\ncodegen:\n%s", buildContent, codegenContent)
	}

	// Sanity: the filtered artifact must include the kept source's table and
	// exclude the table from the other source (guards against a trivially-empty
	// output making the parity check vacuous).
	if wantInclude := "users"; !contains(string(buildContent), wantInclude) {
		t.Errorf("source-filtered build output should mention the kept table %q", wantInclude)
	}
	if wantExclude := "audit_log"; contains(string(buildContent), wantExclude) {
		t.Errorf("source-filtered build output should not mention %q", wantExclude)
	}
}

// TestCodegenWriteRefusesOrphans pins that a standalone codegen WRITE (not just
// --check) refuses when an orphan file exists in an owned multi-file output
// directory and never deletes it -- the same hard-error orphan behavior build
// enforces. Before write-path consolidation the standalone write path did no
// orphan detection at all.
func TestCodegenWriteRefusesOrphans(t *testing.T) {
	dir := writeFreshnessProject(t, "faceted")
	schemaPath := filepath.Join(dir, "schema.toml")
	outDir := filepath.Join(dir, "gen")

	write := func() int {
		return runCodegen(nil, true, map[string]interface{}{
			"path":       []interface{}{schemaPath},
			"lang":       "python",
			"mode":       "ddl",
			"check":      false,
			"split_mode": "faceted",
			"output":     outDir,
			"groups":     nil,
			"source":     nil,
		})
	}

	// First write is clean.
	if code := write(); code != 0 {
		t.Fatalf("initial codegen write exited %d", code)
	}

	// Plant an orphan inside the owned directory.
	orphan := filepath.Join(outDir, "leftover.py")
	if err := os.WriteFile(orphan, []byte("# leftover\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A subsequent write must refuse (orphan is a hard error) and must not
	// delete the orphan.
	if code := write(); code != 1 {
		t.Fatalf("codegen write with orphan must exit 1, got %d", code)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("codegen write must never delete the orphan: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
