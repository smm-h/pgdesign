package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/codegen"
	"github.com/smm-h/pgdesign/internal/config"
)

// writeFreshnessProject creates a temp project directory containing a copy of
// the freshness fixture schema plus a pgdesign.toml with a single multi-file
// codegen output (Python DDL) using the given split mode. Returns the project
// root.
func writeFreshnessProject(t *testing.T, splitMode string) string {
	t.Helper()
	// main() normally initializes this; tests that exercise config loading
	// need it too so lang/mode pairs validate.
	config.CodegenModes = SupportedModes()

	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "freshness_schema.toml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.toml"), src, 0o644); err != nil {
		t.Fatalf("write schema.toml: %v", err)
	}
	cfg := fmt.Sprintf(`[project]
schemas = ["schema.toml"]

[database]
pg_version = 16

[output.pyddl]
format = "codegen"
path = "gen"
lang = "python"
mode = "ddl"
split_mode = %q
`, splitMode)
	if err := os.WriteFile(filepath.Join(dir, "pgdesign.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write pgdesign.toml: %v", err)
	}
	return dir
}

// testBuild runs the build flow quietly with auto-commit off (temp dirs are
// not git repos).
func testBuild(dryRun bool) int {
	return runBuild(true, dryRun, false)
}

// facetedOnlyFile generates both split modes for the project's schema and
// returns the relative path and content of a file that exists in faceted
// output but not in self-contained output — the exact leftover a split-mode
// switch strands on disk.
func facetedOnlyFile(t *testing.T, projectDir string) (string, []byte) {
	t.Helper()
	schema, _, exitCode := parseAndBuild([]string{filepath.Join(projectDir, "schema.toml")})
	if exitCode != 0 {
		t.Fatalf("parseAndBuild failed with exit code %d", exitCode)
	}
	faceted, _ := (&codegen.PythonDDLGenerator{SplitMode: codegen.SplitModeFaceted}).GenerateFiles(schema)
	selfContained, _ := (&codegen.PythonDDLGenerator{SplitMode: codegen.SplitModeSelfContained}).GenerateFiles(schema)
	for rel, data := range faceted {
		if _, ok := selfContained[rel]; !ok {
			return rel, data
		}
	}
	t.Fatal("fixture problem: faceted and self-contained modes produce identical file sets")
	return "", nil
}

// TestCheckBuild_DetectsOrphanAfterSplitModeSwitch reproduces the split-mode
// switch scenario: a project built with split_mode = "faceted" is switched to
// "self-contained". The faceted-only files left on disk inside the owned
// output directory must be reported as orphans and fail the build check —
// otherwise they stay committed and green forever.
func TestCheckBuild_DetectsOrphanAfterSplitModeSwitch(t *testing.T) {
	dir := writeFreshnessProject(t, "self-contained")
	t.Chdir(dir)

	// Materialize the current (self-contained) outputs so the tree is fresh.
	if code := testBuild(false); code != 0 {
		t.Fatalf("initial build failed with exit code %d", code)
	}

	res := checkBuild(&pgdesignCheckContext{root: dir})
	if res.Status != "pass" {
		t.Fatalf("sanity: expected fresh tree to pass, got %s: %s %v", res.Status, res.Message, res.Details)
	}

	// Simulate the leftover from an earlier faceted build.
	leftover, data := facetedOnlyFile(t, dir)
	leftoverPath := filepath.Join(dir, "gen", leftover)
	if err := os.MkdirAll(filepath.Dir(leftoverPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(leftoverPath, data, 0o644); err != nil {
		t.Fatalf("write leftover: %v", err)
	}

	res = checkBuild(&pgdesignCheckContext{root: dir})
	if res.Status != "fail" {
		t.Fatalf("expected orphan %q to fail the build check, got %s: %s", leftover, res.Status, res.Message)
	}
	found := false
	for _, d := range res.Details {
		if strings.Contains(d, "[orphan]") && strings.Contains(d, leftover) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an [orphan] detail naming %q, got details: %v", leftover, res.Details)
	}
}

// TestHandleBuild_OrphanBlocksBuildBeforeWriting verifies that when an owned
// output directory contains an orphan, the build exits 1 and writes NOTHING —
// not the planned files, and never a deletion of the orphan.
func TestHandleBuild_OrphanBlocksBuildBeforeWriting(t *testing.T) {
	dir := writeFreshnessProject(t, "self-contained")
	t.Chdir(dir)

	genDir := filepath.Join(dir, "gen")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	orphanPath := filepath.Join(genDir, "stale_leftover.py")
	if err := os.WriteFile(orphanPath, []byte("# stale\n"), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}

	if code := testBuild(false); code != 1 {
		t.Fatalf("expected exit code 1 with orphan present, got %d", code)
	}

	// Nothing may have been written: gen/ must contain only the orphan.
	entries, err := os.ReadDir(genDir)
	if err != nil {
		t.Fatalf("read gen dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "stale_leftover.py" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected gen/ to contain only the orphan, got %v", names)
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Errorf("orphan must never be deleted: %v", err)
	}

	// Resolving the orphan unblocks the build.
	if err := os.Remove(orphanPath); err != nil {
		t.Fatalf("remove orphan: %v", err)
	}
	if code := testBuild(false); code != 0 {
		t.Fatalf("expected build to succeed after orphan removal, got exit code %d", code)
	}
}

// TestHandleBuild_DryRunReportsOrphans verifies --dry-run exits 1 when an
// orphan exists (a real build would refuse to run) while writing nothing.
func TestHandleBuild_DryRunReportsOrphans(t *testing.T) {
	dir := writeFreshnessProject(t, "self-contained")
	t.Chdir(dir)

	if code := testBuild(false); code != 0 {
		t.Fatalf("initial build failed")
	}

	if code := testBuild(true); code != 0 {
		t.Fatalf("dry-run on fresh tree should exit 0, got %d", code)
	}

	if err := os.WriteFile(filepath.Join(dir, "gen", "stale_leftover.py"), []byte("# stale\n"), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	if code := testBuild(true); code != 1 {
		t.Fatalf("dry-run with orphan should exit 1, got %d", code)
	}
}

// TestScanOrphans_IgnoreList verifies that __pycache__ directories (and their
// contents) and *.pyc files are the only exemptions from orphan detection.
func TestScanOrphans_IgnoreList(t *testing.T) {
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
	mustWrite("__pycache__/owned.cpython-312.pyc", "bytecode")
	mustWrite("__pycache__/anything.txt", "even non-pyc files inside __pycache__ are ignored")
	mustWrite("sub/__pycache__/x.pyc", "bytecode")

	orphans, err := scanOrphans(dir, map[string]bool{"owned.py": true})
	if err != nil {
		t.Fatalf("scanOrphans: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != filepath.Join(dir, "orphan.py") {
		t.Errorf("expected exactly [orphan.py], got %v", orphans)
	}
}

// TestScanOrphans_MissingDirectory verifies a not-yet-created output
// directory yields no orphans and no error.
func TestScanOrphans_MissingDirectory(t *testing.T) {
	orphans, err := scanOrphans(filepath.Join(t.TempDir(), "does-not-exist"), nil)
	if err != nil {
		t.Fatalf("expected no error for missing directory, got %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("expected no orphans for missing directory, got %v", orphans)
	}
}

// TestPlan_OwnedDirs_SingleFileOutputsOwnNothing verifies that single-file
// outputs (a plain file path, not a directory) get no ownership scanning.
func TestPlan_OwnedDirs_SingleFileOutputsOwnNothing(t *testing.T) {
	schema := minimalSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"constants": {
				Format: "codegen",
				Path:   config.AbsolutePath(filepath.Join(t.TempDir(), "tables.go")),
				Lang:   "go",
				Mode:   "constants",
			},
		},
	}
	result, err := Plan(schema, cfg, nil, 16)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(result.OwnedDirs) != 0 {
		t.Errorf("single-file outputs must not own directories, got %v", result.OwnedDirs)
	}
}

// TestPlan_OwnedDirs_SharedDirectoryUnion verifies that two multi-file
// outputs writing into the same directory union their owned sets, so neither
// output's files are flagged as orphans of the other.
func TestPlan_OwnedDirs_SharedDirectoryUnion(t *testing.T) {
	dir := writeFreshnessProject(t, "")
	schema, _, exitCode := parseAndBuild([]string{filepath.Join(dir, "schema.toml")})
	if exitCode != 0 {
		t.Fatalf("parseAndBuild failed")
	}

	outDir := config.AbsolutePath(filepath.Join(t.TempDir(), "gen"))
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"pyddl": {
				Format:    "codegen",
				Path:      outDir,
				Lang:      "python",
				Mode:      "ddl",
				SplitMode: "faceted",
			},
			"queries": {
				Format: "codegen",
				Path:   outDir,
				Lang:   "python",
				Mode:   "query-layer",
			},
		},
	}
	result, err := Plan(schema, cfg, nil, 16)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(result.OwnedDirs) != 1 {
		t.Fatalf("expected exactly one owned directory, got %v", result.OwnedDirs)
	}
	owned := result.OwnedDirs[string(outDir)]
	if owned == nil {
		t.Fatalf("expected %s to be owned, got %v", outDir, result.OwnedDirs)
	}

	// Write every planned file to disk; the shared directory must then scan
	// clean — no false-positive orphans from either output's file set.
	for p, data := range result.Files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orphans, err := scanAllOrphans(result.OwnedDirs)
	if err != nil {
		t.Fatalf("scanAllOrphans: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("shared directory must not produce false-positive orphans, got %v", orphans)
	}
}

// TestPlan_OwnedDirs_ConfiguredSVGInsideOwnedDirIsNotOrphan verifies that a
// configured output path of any format (here SVG, which Plan skips because d2
// rendering is non-deterministic) falling inside an owned codegen directory
// is treated as owned, not orphaned.
func TestPlan_OwnedDirs_ConfiguredSVGInsideOwnedDirIsNotOrphan(t *testing.T) {
	dir := writeFreshnessProject(t, "")
	schema, _, exitCode := parseAndBuild([]string{filepath.Join(dir, "schema.toml")})
	if exitCode != 0 {
		t.Fatalf("parseAndBuild failed")
	}

	outDir := filepath.Join(t.TempDir(), "gen")
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"pyddl": {
				Format:    "codegen",
				Path:      config.AbsolutePath(outDir),
				Lang:      "python",
				Mode:      "ddl",
				SplitMode: "faceted",
			},
			"diagram": {
				Format: "svg",
				Path:   config.AbsolutePath(filepath.Join(outDir, "schema.svg")),
			},
		},
	}
	result, err := Plan(schema, cfg, nil, 16)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	owned := result.OwnedDirs[outDir]
	if owned == nil {
		t.Fatalf("expected %s to be owned", outDir)
	}
	if !owned["schema.svg"] {
		t.Errorf("configured SVG path inside owned dir must be owned, owned set: %v", owned)
	}

	// A rendered SVG on disk must not be flagged as an orphan.
	for p, data := range result.Files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outDir, "schema.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphans, err := scanAllOrphans(result.OwnedDirs)
	if err != nil {
		t.Fatalf("scanAllOrphans: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("rendered SVG inside owned dir must not be an orphan, got %v", orphans)
	}
}

// codegenKwargs returns the kwargs handleCodegen expects. End-to-end CLI
// invocation is exercised at the handler level here (the --split-mode flag
// currently breaks CLI parsing pending a strictcli upgrade).
func codegenKwargs(schemaPath, lang, mode, output string, check bool) map[string]interface{} {
	kwargs := map[string]interface{}{
		"path":  []interface{}{schemaPath},
		"lang":  lang,
		"mode":  mode,
		"quiet": true,
		"check": check,
	}
	if output != "" {
		kwargs["output"] = output
	}
	return kwargs
}

func TestHandleCodegenCheck_RequiresOutput(t *testing.T) {
	dir := writeFreshnessProject(t, "")
	schemaPath := filepath.Join(dir, "schema.toml")
	if code := handleCodegen(codegenKwargs(schemaPath, "go", "constants", "", true)); code != 1 {
		t.Fatalf("--check without --output must exit 1, got %d", code)
	}
}

// TestHandleCodegenCheck_MultiFile covers the full --check lifecycle for a
// multi-file mode: missing before generation, fresh after, stale after edit,
// orphan after planting an unowned file, and __pycache__ exemption.
func TestHandleCodegenCheck_MultiFile(t *testing.T) {
	dir := writeFreshnessProject(t, "")
	schemaPath := filepath.Join(dir, "schema.toml")
	outDir := filepath.Join(dir, "out")
	kwCheck := func() map[string]interface{} {
		kw := codegenKwargs(schemaPath, "python", "ddl", outDir, true)
		kw["split_mode"] = "faceted"
		return kw
	}
	kwWrite := func() map[string]interface{} {
		kw := codegenKwargs(schemaPath, "python", "ddl", outDir, false)
		kw["split_mode"] = "faceted"
		return kw
	}

	// Before generation: everything is missing.
	if code := handleCodegen(kwCheck()); code != 1 {
		t.Fatalf("--check before generation must exit 1, got %d", code)
	}

	// Generate, then check: clean.
	if code := handleCodegen(kwWrite()); code != 0 {
		t.Fatalf("codegen write failed with exit code %d", code)
	}
	if code := handleCodegen(kwCheck()); code != 0 {
		t.Fatalf("--check on fresh output must exit 0, got %d", code)
	}

	// Corrupt one generated file: stale.
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("read out dir: %v (%d entries)", err, len(entries))
	}
	victim := filepath.Join(outDir, entries[0].Name())
	original, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, append(original, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := handleCodegen(kwCheck()); code != 1 {
		t.Fatalf("--check with stale file must exit 1, got %d", code)
	}
	if err := os.WriteFile(victim, original, 0o644); err != nil {
		t.Fatal(err)
	}

	// Plant an orphan: fail.
	orphanPath := filepath.Join(outDir, "leftover.py")
	if err := os.WriteFile(orphanPath, []byte("# leftover\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := handleCodegen(kwCheck()); code != 1 {
		t.Fatalf("--check with orphan must exit 1, got %d", code)
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("--check must never delete anything: %v", err)
	}
	if err := os.Remove(orphanPath); err != nil {
		t.Fatal(err)
	}

	// __pycache__ and *.pyc are exempt.
	if err := os.MkdirAll(filepath.Join(outDir, "__pycache__"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "__pycache__", "x.cpython-312.pyc"), []byte("bc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "stray.pyc"), []byte("bc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := handleCodegen(kwCheck()); code != 0 {
		t.Fatalf("--check must ignore __pycache__ and *.pyc, got exit %d", code)
	}
}

// TestHandleCodegenCheck_SingleFile covers --check for a single-file mode:
// byte-exact comparison, no directory ownership.
func TestHandleCodegenCheck_SingleFile(t *testing.T) {
	dir := writeFreshnessProject(t, "")
	schemaPath := filepath.Join(dir, "schema.toml")
	outFile := filepath.Join(dir, "out", "constants.go")

	if code := handleCodegen(codegenKwargs(schemaPath, "go", "constants", outFile, true)); code != 1 {
		t.Fatalf("--check with missing file must exit 1, got %d", code)
	}

	if err := os.MkdirAll(filepath.Dir(outFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := handleCodegen(codegenKwargs(schemaPath, "go", "constants", outFile, false)); code != 0 {
		t.Fatalf("codegen write failed")
	}
	if code := handleCodegen(codegenKwargs(schemaPath, "go", "constants", outFile, true)); code != 0 {
		t.Fatalf("--check on fresh single file must exit 0, got %d", code)
	}

	// A sibling file next to a single-file output is NOT an orphan: plain
	// file paths get no directory ownership.
	if err := os.WriteFile(filepath.Join(dir, "out", "unrelated.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := handleCodegen(codegenKwargs(schemaPath, "go", "constants", outFile, true)); code != 0 {
		t.Fatalf("sibling files must not affect single-file --check, got exit %d", code)
	}

	if err := os.WriteFile(outFile, []byte("// tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := handleCodegen(codegenKwargs(schemaPath, "go", "constants", outFile, true)); code != 1 {
		t.Fatalf("--check on stale single file must exit 1, got %d", code)
	}
}
