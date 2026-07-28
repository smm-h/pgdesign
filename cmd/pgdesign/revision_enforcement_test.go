package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/format"
	"github.com/smm-h/pgdesign/internal/migrate"
	"github.com/smm-h/strictcli/go/strictcli"
)

// runCheckRevision calls checkRevision with a fresh reporter and returns the
// derived status plus the formatted result for detail assertions.
func runCheckRevision(ctx strictcli.CheckContext) (status, formatted string) {
	r := &strictcli.ErrorReporter{}
	outcome := checkRevision(ctx, r)
	res := strictcli.CheckRunResult{Name: "revision", Outcome: outcome}
	return res.Status(), strictcli.FormatCheckResults([]strictcli.CheckRunResult{res}, true)
}

// multiOutputSchema is a two-table schema with a "core" group holding only
// users, used to exercise full, group-filtered comment-stamped, and filtered-JSON
// outputs together.
const multiOutputSchema = `format_version = 1
[meta]
schema = "enf"

[tables.users]
comment = "Core users"

[tables.users.columns.id]
type = "id"

[tables.users.columns.name]
type = "short_text"

[tables.orders]
comment = "Order records"

[tables.orders.columns.id]
type = "id"

[tables.orders.columns.user_id]
type = "ref"

[tables.orders.fks.fk_orders_users]
columns = ["user_id"]
ref_table = "users"
ref_columns = ["id"]
on_delete = "CASCADE"

[groups]
core = ["users"]
`

// multiOutputConfig declares a full doc, a group-filtered doc, a group-filtered
// JSON envelope, and a single-file Go constants codegen output.
const multiOutputConfig = `[project]
schemas = ["schema.toml"]

[database]
pg_version = 16

[output.doc_all]
format = "doc"
path = "all.md"

[output.doc_core]
format = "doc"
path = "core.md"
groups = ["core"]

[output.json_core]
format = "json"
path = "core.json"
groups = ["core"]

[output.consts]
format = "codegen"
path = "tables.go"
lang = "go"
mode = "constants"
`

// writeMultiOutputProject materializes the multi-output fixture and returns the
// project root.
func writeMultiOutputProject(t *testing.T) string {
	t.Helper()
	config.CodegenModes = SupportedModes()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.toml"), []byte(multiOutputSchema), 0o644); err != nil {
		t.Fatalf("write schema.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pgdesign.toml"), []byte(multiOutputConfig), 0o644); err != nil {
		t.Fatalf("write pgdesign.toml: %v", err)
	}
	return dir
}

// TestRevision_EditThenBuildSucceedsThenPartialWriteRefuses covers the roadmap
// 6.2 Verify core: a TOML edit followed by `build` succeeds and passes the
// revision check; then editing the TOML again and running `codegen --output` of
// ONE output REFUSES, naming the now-stale siblings (including the group-filtered
// ones).
func TestRevision_EditThenBuildSucceedsThenPartialWriteRefuses(t *testing.T) {
	dir := writeMultiOutputProject(t)
	t.Chdir(dir)
	cfgPath := filepath.Join(dir, "pgdesign.toml")
	schemaPath := filepath.Join(dir, "schema.toml")

	// Build all outputs; the whole planner set lands at one revision.
	if code := runBuild(&cfgPath, true, false, false, ""); code != 0 {
		t.Fatalf("initial build exited %d", code)
	}
	if status, out := runCheckRevision(&pgdesignCheckContext{root: dir}); status != "pass" {
		t.Fatalf("expected fresh build to pass revision check, got %s:\n%s", status, out)
	}

	// Edit the schema: add a column to users (in the "core" group), which changes
	// both the full-project revision and the core-filtered revision.
	edited := multiOutputSchema + `
[tables.users.columns.email]
type = "short_text"
`
	if err := os.WriteFile(schemaPath, []byte(edited), 0o644); err != nil {
		t.Fatalf("edit schema: %v", err)
	}

	// Partial write: regenerate ONLY the codegen constants output. Its siblings
	// (all.md, core.md, core.json) are still at the previous revision -> refuse.
	constsPath := filepath.Join(dir, "tables.go")
	kwargs := map[string]interface{}{
		"path":       []interface{}{schemaPath},
		"lang":       "go",
		"mode":       "constants",
		"check":      false,
		"split_mode": nil,
		"output":     constsPath,
		"groups":     nil,
		"source":     nil,
	}
	code := runCodegenCapturingStderr(t, &cfgPath, kwargs)
	if code.exit != 1 {
		t.Fatalf("expected codegen --output to refuse (exit 1) on stale siblings, got %d\n%s", code.exit, code.stderr)
	}
	for _, sib := range []string{"all.md", "core.md", "core.json"} {
		if !strings.Contains(code.stderr, sib) {
			t.Errorf("refusal should name stale sibling %q:\n%s", sib, code.stderr)
		}
	}
	if !strings.Contains(code.stderr, "pgdesign build") {
		t.Errorf("refusal should point at the remedy (`pgdesign build`):\n%s", code.stderr)
	}

	// The full build restores the fixed point and clears the refusal.
	if bc := runBuild(&cfgPath, true, false, false, ""); bc != 0 {
		t.Fatalf("rebuild exited %d", bc)
	}
	if code2 := runCodegenCapturingStderr(t, &cfgPath, kwargs); code2.exit != 0 {
		t.Fatalf("codegen --output should succeed once siblings are fresh, got %d\n%s", code2.exit, code2.stderr)
	}
}

type codegenResult struct {
	exit   int
	stderr string
}

func runCodegenCapturingStderr(t *testing.T, cfgPath *string, kwargs map[string]interface{}) codegenResult {
	t.Helper()
	var exit int
	stderr := captureStderr(t, func() {
		exit = runCodegen(cfgPath, false, kwargs)
	})
	return codegenResult{exit: exit, stderr: stderr}
}

// TestRevision_FmtPrintsNoticeAndCheckGoesStale covers the SOURCE-EDITING writer
// path: fmt rewrites the schema source (reordering columns), prints the follow-up
// notice, and the revision check then reports the outputs as stale.
func TestRevision_FmtPrintsNoticeAndCheckGoesStale(t *testing.T) {
	dir := t.TempDir()
	config.CodegenModes = SupportedModes()

	// Columns declared out of alphabetical order so `fmt --column-order
	// alphabetical` actually reorders them (a byte AND revision change).
	schema := `format_version = 1
[meta]
schema = "fmtenf"

[tables.t]
comment = "Table"

[tables.t.columns.id]
type = "id"

[tables.t.columns.zebra]
type = "short_text"

[tables.t.columns.apple]
type = "short_text"
`
	schemaPath := filepath.Join(dir, "schema.toml")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	cfg := `[project]
schemas = ["schema.toml"]

[database]
pg_version = 16

[output.doc]
format = "doc"
path = "schema.md"
`
	cfgPath := filepath.Join(dir, "pgdesign.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Chdir(dir)

	if code := runBuild(&cfgPath, true, false, false, ""); code != 0 {
		t.Fatalf("build exited %d", code)
	}
	if status, out := runCheckRevision(&pgdesignCheckContext{root: dir}); status != "pass" {
		t.Fatalf("fresh build should pass revision check, got %s:\n%s", status, out)
	}

	// fmt in place, reordering columns alphabetically.
	notice := captureStderr(t, func() {
		if code := fmtFile(schemaPath, &format.Config{TableOrder: "dependency", ColumnOrder: "alphabetical"}, false); code != 0 {
			t.Fatalf("fmt exited %d", code)
		}
	})
	if !strings.Contains(notice, "project revision changed") {
		t.Errorf("fmt should print the source-edit follow-up notice, got:\n%s", notice)
	}

	// The doc output is now stale relative to the reformatted source.
	status, out := runCheckRevision(&pgdesignCheckContext{root: dir})
	if status != "fail" {
		t.Fatalf("expected revision check to go stale after fmt, got %s:\n%s", status, out)
	}
	if !strings.Contains(out, revisionMismatchPrefix) {
		t.Errorf("stale check should carry the stamp-extractor signal %q:\n%s", revisionMismatchPrefix, out)
	}
}

// TestRevision_TamperedHeaderDistinguishesSignals covers the roadmap-6.2 dual
// signal: a tampered provenance header trips the stamp-extractor (`revision`
// check, [revision-mismatch]) AND the byte-compare (`build` check, [stale]), and
// the two read distinctly.
func TestRevision_TamperedHeaderDistinguishesSignals(t *testing.T) {
	dir := writeMultiOutputProject(t)
	t.Chdir(dir)
	cfgPath := filepath.Join(dir, "pgdesign.toml")

	if code := runBuild(&cfgPath, true, false, false, ""); code != 0 {
		t.Fatalf("build exited %d", code)
	}

	// Tamper the revision line of a comment-stamped output.
	docPath := filepath.Join(dir, "all.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read doc: %v", err)
	}
	tampered := replaceRevisionLine(t, string(data), "registry_present:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err := os.WriteFile(docPath, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered doc: %v", err)
	}

	// Stamp-extractor signal.
	status, revOut := runCheckRevision(&pgdesignCheckContext{root: dir})
	if status != "fail" {
		t.Fatalf("revision check should catch the tampered header, got %s:\n%s", status, revOut)
	}
	if !strings.Contains(revOut, revisionMismatchPrefix) {
		t.Errorf("revision check should emit %q for a tampered header:\n%s", revisionMismatchPrefix, revOut)
	}

	// Byte-compare signal (the existing build/freshness check).
	cbr := runCheckBuild(&pgdesignCheckContext{root: dir})
	if cbr.status != "fail" {
		t.Fatalf("build check should catch the tampered header via byte-compare, got %s", cbr.status)
	}
	buildOut := strictcli.FormatCheckResults([]strictcli.CheckRunResult{cbr.result}, true)
	if !strings.Contains(buildOut, "[stale]") {
		t.Errorf("build check should emit the [stale] byte-compare signal:\n%s", buildOut)
	}
	// The two signals are distinct wordings.
	if strings.Contains(buildOut, revisionMismatchPrefix) {
		t.Errorf("byte-compare check must not borrow the stamp-extractor prefix:\n%s", buildOut)
	}
}

func replaceRevisionLine(t *testing.T, content, newRev string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		if idx := strings.Index(ln, "pgdesign-revision: "); idx >= 0 {
			lines[i] = ln[:idx+len("pgdesign-revision: ")] + newRev
			return strings.Join(lines, "\n")
		}
	}
	t.Fatalf("no revision line found to tamper in:\n%s", content)
	return content
}

// TestRevision_ScaffoldingAndSeedNeverFlagged covers roadmap-6.2's outside-the-
// invariant classes: testdb-init wrappers, introspect --output candidate sources,
// and seed output are NOT [output] artifacts, so the revision check never flags
// them even when they carry a stale/foreign stamp.
func TestRevision_ScaffoldingAndSeedNeverFlagged(t *testing.T) {
	dir := writeMultiOutputProject(t)
	t.Chdir(dir)
	cfgPath := filepath.Join(dir, "pgdesign.toml")
	if code := runBuild(&cfgPath, true, false, false, ""); code != 0 {
		t.Fatalf("build exited %d", code)
	}

	staleStamp := "// Code generated by pgdesign. DO NOT EDIT.\n// pgdesign-revision: registry_present:000000000000\n// stale\n"
	// A scaffolding wrapper (testdb init style), a candidate source (introspect
	// --output style), and a seed file — none are configured [output] artifacts.
	for _, name := range []string{"testdb_wrapper.go", "introspected.toml", "seed.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(staleStamp), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if status, out := runCheckRevision(&pgdesignCheckContext{root: dir}); status != "pass" {
		t.Fatalf("revision check must ignore scaffolding/seed files, got %s:\n%s", status, out)
	}
}

// TestRevision_ChainViolationCaught covers the roadmap-6.2 chain integrity leg:
// the revision check invokes migrate.VerifyChainConsistency, so a corrupted chain
// is reported with the [chain] signal.
func TestRevision_ChainViolationCaught(t *testing.T) {
	dir := setupReviseRepo(t, "freshness_schema.toml", true)
	t.Chdir(dir)

	// Genesis revise builds outputs and creates the chain (DB tier skipped).
	if code := runRevise(nil, true, nil, ""); code != reviseExitDBSkipped {
		t.Fatalf("genesis revise exited %d, want %d", code, reviseExitDBSkipped)
	}

	// Sanity: a consistent chain + fresh outputs pass the revision check.
	if status, out := runCheckRevision(&pgdesignCheckContext{root: dir}); status != "pass" {
		t.Fatalf("consistent chain should pass revision check, got %s:\n%s", status, out)
	}

	// Corrupt a revision manifest: point one entry at a non-existent object id so
	// the store<->chain closure check fails.
	corruptOneRevisionManifest(t, filepath.Join(dir, "migrations"))

	status, out := runCheckRevision(&pgdesignCheckContext{root: dir})
	if status != "fail" {
		t.Fatalf("revision check should catch the chain violation, got %s:\n%s", status, out)
	}
	if !strings.Contains(out, "[chain]") {
		t.Errorf("chain violation should carry the [chain] signal:\n%s", out)
	}
}

// corruptOneRevisionManifest rewrites one migrations/revisions/*.json manifest so
// an entry references a bogus object id, breaking Merkle closure while keeping the
// file itself well-formed (revision/class/codec intact).
func corruptOneRevisionManifest(t *testing.T, migrationsDir string) {
	t.Helper()
	revDir := filepath.Join(migrationsDir, "revisions")
	entries, err := os.ReadDir(revDir)
	if err != nil {
		t.Fatalf("read revisions dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(revDir, e.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal manifest: %v", err)
		}
		ents, ok := m["entries"].(map[string]any)
		if !ok || len(ents) == 0 {
			continue // empty (genesis-empty) manifest: try the next
		}
		for k := range ents {
			ents[k] = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
		}
		m["entries"] = ents
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}
		if err := os.WriteFile(p, out, 0o644); err != nil {
			t.Fatalf("write corrupted manifest: %v", err)
		}
		return
	}
	t.Fatal("no non-empty revision manifest found to corrupt")
}

// ensure the migrate import is used even if the chain test is skipped under
// short mode in the future.
var _ = migrate.IsChainMode
