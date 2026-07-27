package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/smm-h/pgdesign/internal/codegen"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/pkg/genkit"
)

// DB-free compile checks (roadmap 4.0, boundary item 5). Each test freshly
// GENERATES the `types` mode output for one target language and invokes that
// language's real toolchain (go build, tsc --noEmit, mypy, javac, kotlinc, zig
// build-obj) over the generated fixture. `types` is the richest self-contained
// unit every language emits (structs/records/classes + enums + transition maps
// + FK/nullable/array-derived fields), so it is the compile unit here; other
// modes reference cross-mode row types, need third-party libraries, or await
// 4.1 branding (imports/qualification/dedup) and are not yet standalone-
// compilable (see the report accompanying this change).
//
// Toolchain-missing behavior: SKIP locally with a visible message. CI provisions
// all six toolchains and sets PGDESIGN_REQUIRE_COMPILE_TOOLCHAINS=1, which turns
// a missing toolchain into a hard FAILURE so no language is silently skipped in
// CI. Python uses mypy (the repo/CI provisions mypy, not pyright).

// requireTool resolves a toolchain binary. When it is absent the test skips
// locally, but fails hard under PGDESIGN_REQUIRE_COMPILE_TOOLCHAINS=1 so CI can
// never silently skip a language.
func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		msg := name + " not found in PATH"
		if os.Getenv("PGDESIGN_REQUIRE_COMPILE_TOOLCHAINS") != "" {
			t.Fatalf("%s (PGDESIGN_REQUIRE_COMPILE_TOOLCHAINS is set: CI must provision every compile toolchain)", msg)
		}
		t.Skip(msg + " (set PGDESIGN_REQUIRE_COMPILE_TOOLCHAINS=1 to make this a hard failure)")
	}
}

// loadCompileSchema parses and builds the codegen-compile fixture (enums, a
// state machine, scalar domains, FKs, CHECKs, uniques, indexes).
func loadCompileSchema(t *testing.T) *model.Schema {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "codegen_compile_schema.toml"))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	raw, diags := parse.File(path)
	if raw == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	for _, d := range diags {
		if d.Severity == diagnostic.Error {
			t.Fatalf("parse error: %s", d.Message)
		}
	}
	reg := semtype.NewBuiltinRegistry()
	if userTypes := parse.CollectUserTypes(raw); len(userTypes) > 0 {
		if loadDiags := reg.LoadUserTypes(userTypes); loadDiags.HasErrors() {
			t.Fatalf("user type load errors: %v", loadDiags)
		}
	}
	schema, buildDiags := model.Build(raw, reg)
	if buildDiags.HasErrors() {
		t.Fatalf("build errors: %v", buildDiags)
	}
	return schema
}

// generateTypeFiles renders the `types` output for a generator, returning a
// map of relative filename -> contents. Single-file generators yield one entry
// under the provided defaultName; MultiFileGenerators yield their own file map.
func generateTypeFiles(t *testing.T, gen genkit.Generator, defaultName string) map[string][]byte {
	t.Helper()
	schema := loadCompileSchema(t)
	if mfg, ok := gen.(genkit.MultiFileGenerator); ok {
		files, diags := mfg.GenerateFiles(schema)
		failOnCompileErrDiags(t, diags)
		if len(files) == 0 {
			t.Fatal("multi-file generator produced no files")
		}
		return files
	}
	out, diags := gen.Generate(schema)
	failOnCompileErrDiags(t, diags)
	return map[string][]byte{defaultName: out}
}

func failOnCompileErrDiags(t *testing.T, diags []diagnostic.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == diagnostic.Error {
			t.Fatalf("generator emitted an error diagnostic: %s %s", d.Code, d.Message)
		}
	}
}

// writeFiles writes each relative filename in files under dir.
func writeFiles(t *testing.T, dir string, files map[string][]byte) []string {
	t.Helper()
	names := make([]string, 0, len(files))
	for rel, data := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		names = append(names, rel)
	}
	sort.Strings(names)
	return names
}

// runCompile runs a toolchain command in dir with a generous timeout and fails
// the test with the combined output on non-zero exit.
func runCompile(t *testing.T, dir string, extraEnv []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(300 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("%s %v timed out after 300s", name, args)
	}
	if err != nil {
		t.Fatalf("%s %v failed:\n%s\nerror: %v", name, args, out, err)
	}
}

func TestCompileGoTypes(t *testing.T) {
	requireTool(t, "go")
	dir := t.TempDir()
	files := generateTypeFiles(t, &codegen.GoTypesGenerator{}, "types.go")
	writeFiles(t, dir, files)

	// A throwaway module providing the uuid dependency the generated structs
	// reference. `go mod tidy` resolves it; CI has module network access.
	goMod := "module pgdesigncompile\n\ngo 1.23\n\nrequire github.com/google/uuid v1.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	runCompile(t, dir, nil, "go", "mod", "tidy")
	runCompile(t, dir, nil, "go", "build", "./...")
}

func TestCompileTSTypes(t *testing.T) {
	requireTool(t, "tsc")
	dir := t.TempDir()
	files := generateTypeFiles(t, &codegen.TSTypesGenerator{}, "types.ts")
	writeFiles(t, dir, files)
	tsconfig := `{"compilerOptions":{"target":"ES2020","module":"ES2020","moduleResolution":"bundler","strict":true,"noEmit":true,"skipLibCheck":true}}`
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644); err != nil {
		t.Fatalf("write tsconfig: %v", err)
	}
	runCompile(t, dir, nil, "tsc", "--noEmit", "-p", "tsconfig.json")
}

func TestCompilePythonTypes(t *testing.T) {
	// Python type-check via mypy (the toolchain the repo/CI provisions).
	requireTool(t, "mypy")
	dir := t.TempDir()
	// Not "types.py": that shadows the stdlib `types` module and mypy refuses it.
	files := generateTypeFiles(t, &codegen.PythonTypesGenerator{}, "schema_types.py")
	writeFiles(t, dir, files)
	runCompile(t, dir, nil, "mypy", "--strict", "schema_types.py")
}

func TestCompileJavaTypes(t *testing.T) {
	requireTool(t, "javac")
	dir := t.TempDir()
	// Java types is a MultiFileGenerator: one public type per file. javac
	// compiles the whole directory together (default package).
	files := generateTypeFiles(t, &codegen.JavaTypesGenerator{}, "Types.java")
	names := writeFiles(t, dir, files)
	runCompile(t, dir, nil, "javac", names...)
}

func TestCompileKotlinTypes(t *testing.T) {
	requireTool(t, "kotlinc")
	dir := t.TempDir()
	files := generateTypeFiles(t, &codegen.KotlinTypesGenerator{}, "SchemaTypes.kt")
	names := writeFiles(t, dir, files)
	args := append([]string{}, names...)
	args = append(args, "-d", filepath.Join(dir, "out"))
	runCompile(t, dir, nil, "kotlinc", args...)
}

func TestCompileZigTypes(t *testing.T) {
	requireTool(t, "zig")
	dir := t.TempDir()
	files := generateTypeFiles(t, &codegen.ZigTypesGenerator{}, "types.zig")
	writeFiles(t, dir, files)
	// build-obj type-checks and compiles to an object without needing a main.
	runCompile(t, dir, []string{"ZIG_LOCAL_CACHE_DIR=" + filepath.Join(dir, ".zig-cache"), "ZIG_GLOBAL_CACHE_DIR=" + filepath.Join(dir, ".zig-global-cache")}, "zig", "build-obj", "types.zig")
}
