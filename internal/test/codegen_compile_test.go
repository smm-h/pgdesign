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
	goModWithUUID(t, dir)
	runCompile(t, dir, nil, "go", "mod", "tidy")
	runCompile(t, dir, nil, "go", "build", "./...")
}

// goModWithUUID writes a throwaway module whose only dependency is the uuid
// package the generated structs reference.
func goModWithUUID(t *testing.T, dir string) {
	t.Helper()
	goMod := "module pgdesigncompile\n\ngo 1.23\n\nrequire github.com/google/uuid v1.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

// TestCompileGoConstraints compiles the branded `constraints` mode alongside the
// `types` mode. go_constraints shares package schema with the branded row
// structs and enum types it validates (no cross-package import path is
// configurable), switches on the branded value's .String(), and uses
// !IsValid() for NOT NULL branded-enum columns, so it only compiles against the
// branded types (roadmap 4.1).
func TestCompileGoConstraints(t *testing.T) {
	requireTool(t, "go")
	dir := t.TempDir()
	writeFiles(t, dir, generateTypeFiles(t, &codegen.GoTypesGenerator{}, "types.go"))
	files, diags := (&codegen.GoConstraintsGenerator{}).Generate(loadCompileSchema(t))
	failOnCompileErrDiags(t, diags)
	writeFiles(t, dir, map[string][]byte{"constraints.go": files})
	goModWithUUID(t, dir)
	runCompile(t, dir, nil, "go", "mod", "tidy")
	runCompile(t, dir, nil, "go", "build", "./...")
	runCompile(t, dir, nil, "go", "vet", "./...")
}

// TestCompileGoGormConstraints compiles the branded `constraints` mode alongside
// the `gorm` mode in one package. gorm is the OTHER Go struct provider (besides
// types): it also emits the branded row structs and enum types into package
// schema that constraints references by bare name (row.Role.String()/.IsValid()).
// This pins that gorm+constraints co-location compiles, which is what the build
// guard now permits (and requires, when both are configured, in one directory).
func TestCompileGoGormConstraints(t *testing.T) {
	requireTool(t, "go")
	dir := t.TempDir()
	schema := loadCompileSchema(t)
	gormOut, gdiags := (&codegen.GoGormGenerator{}).Generate(schema)
	failOnCompileErrDiags(t, gdiags)
	writeFiles(t, dir, map[string][]byte{"gorm.go": gormOut})
	conOut, cdiags := (&codegen.GoConstraintsGenerator{}).Generate(schema)
	failOnCompileErrDiags(t, cdiags)
	writeFiles(t, dir, map[string][]byte{"constraints.go": conOut})
	goModWithUUID(t, dir)
	runCompile(t, dir, nil, "go", "mod", "tidy")
	runCompile(t, dir, nil, "go", "build", "./...")
	runCompile(t, dir, nil, "go", "vet", "./...")
}

// TestCompileGoEnumAllIngress compiles AND runs an all-ingress round-trip over
// the branded Go enum: literal member, Parse (accept + reject), zero-value
// invalidity, JSON marshal/unmarshal (validating), sql.Scan (string/bytes/nil/
// reject), and driver.Value. It is the behavioral witness that every ingress
// validates and round-trips (roadmap 4.1 "Go all-ingress round-trip").
func TestCompileGoEnumAllIngress(t *testing.T) {
	requireTool(t, "go")
	dir := t.TempDir()
	writeFiles(t, dir, generateTypeFiles(t, &codegen.GoTypesGenerator{}, "types.go"))
	roundtrip := []byte(`package schema

import (
	"database/sql/driver"
	"encoding/json"
	"testing"
)

func TestRoleAllIngress(t *testing.T) {
	// Literal member + Stringer.
	if RoleAdmin.String() != "admin" {
		t.Fatalf("String: %q", RoleAdmin.String())
	}
	// Parse: accept and reject.
	r, err := ParseRole("user")
	if err != nil || r != RoleUser {
		t.Fatalf("ParseRole(user): %v %v", r, err)
	}
	if _, err := ParseRole("nope"); err == nil {
		t.Fatal("ParseRole must reject unknown")
	}
	// Zero value is detectably invalid.
	var z Role
	if z.IsValid() {
		t.Fatal("zero value must be invalid")
	}
	// JSON round-trip, validating.
	b, err := json.Marshal(RoleGuest)
	if err != nil || string(b) != "\"guest\"" {
		t.Fatalf("MarshalJSON: %q %v", b, err)
	}
	var back Role
	if err := json.Unmarshal(b, &back); err != nil || back != RoleGuest {
		t.Fatalf("UnmarshalJSON: %v %v", back, err)
	}
	if err := json.Unmarshal([]byte("\"bad\""), &back); err == nil {
		t.Fatal("UnmarshalJSON must reject unknown")
	}
	// sql.Scan: string, bytes, reject, nil.
	var s Role
	if err := s.Scan("admin"); err != nil || s != RoleAdmin {
		t.Fatalf("Scan string: %v %v", s, err)
	}
	if err := s.Scan([]byte("user")); err != nil || s != RoleUser {
		t.Fatalf("Scan bytes: %v %v", s, err)
	}
	if err := s.Scan("bad"); err == nil {
		t.Fatal("Scan must reject unknown")
	}
	if err := s.Scan(nil); err == nil {
		t.Fatal("Scan must reject NULL")
	}
	// driver.Value.
	v, err := RoleAdmin.Value()
	if err != nil || v != driver.Value("admin") {
		t.Fatalf("Value: %v %v", v, err)
	}
}
`)
	if err := os.WriteFile(filepath.Join(dir, "roundtrip_test.go"), roundtrip, 0o644); err != nil {
		t.Fatalf("write roundtrip_test.go: %v", err)
	}
	goModWithUUID(t, dir)
	runCompile(t, dir, nil, "go", "mod", "tidy")
	runCompile(t, dir, nil, "go", "test", "./...")
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
	// Exhaustiveness witness: a switch over the kept union that assigns the
	// default case to `never` fails to compile if any member is unhandled, and
	// parseRole is the validating boundary. Both must type-check (roadmap 4.1).
	exhaustive := `import { Role, parseRole } from "./types";

export function label(r: Role): string {
  switch (r) {
    case "admin": return "a";
    case "user": return "u";
    case "guest": return "g";
    default: {
      const _exhaustive: never = r;
      return _exhaustive;
    }
  }
}

export const parsed: Role = parseRole("admin");
`
	if err := os.WriteFile(filepath.Join(dir, "exhaustive.ts"), []byte(exhaustive), 0o644); err != nil {
		t.Fatalf("write exhaustive.ts: %v", err)
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

// TestRunPythonQueryLayerEnums is the behavioral witness for the branded Python
// query-layer (roadmap 4.1): both backends build rows via OrdersRow(**row), so
// Row.__post_init__ coerces enum fields to the branded StrEnum. It generates the
// memory backend (no asyncpg dependency), then runs Python to prove str inputs
// yield enum-typed fields, the coercion is idempotent, invalid values raise, and
// enum-typed rows pickle round-trip.
func TestRunPythonQueryLayerEnums(t *testing.T) {
	requireTool(t, "python3")
	base := t.TempDir()
	pkgDir := filepath.Join(base, "qlpkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Memory-only so the package imports no asyncpg (pg backend would).
	gen := &codegen.PythonQueryLayerGenerator{Backends: []string{"memory"}}
	files, diags := gen.GenerateFiles(loadCompileSchema(t))
	failOnCompileErrDiags(t, diags)
	writeFiles(t, pkgDir, files)

	script := `import pickle
from uuid import uuid4
from qlpkg.protocols import OrdersRow, OrderStatus

# String input (as both backends supply) is coerced to the branded enum.
r = OrdersRow(id=uuid4(), account_id=uuid4(), status="pending", total_cents=5, note=None)
assert isinstance(r.status, OrderStatus), type(r.status)
assert r.status is OrderStatus.PENDING

# Idempotent: an already-branded value passes through unchanged.
r2 = OrdersRow(id=uuid4(), account_id=uuid4(), status=OrderStatus.SHIPPED, total_cents=1, note="x")
assert r2.status is OrderStatus.SHIPPED

# Invalid value is rejected at construction.
try:
    OrdersRow(id=uuid4(), account_id=uuid4(), status="bogus", total_cents=0, note=None)
    raise SystemExit("expected ValueError for invalid enum")
except ValueError:
    pass

# Enum-typed rows pickle round-trip.
back = pickle.loads(pickle.dumps(r))
assert isinstance(back.status, OrderStatus)
assert back.status == OrderStatus.PENDING
print("OK")
`
	if err := os.WriteFile(filepath.Join(base, "check.py"), []byte(script), 0o644); err != nil {
		t.Fatalf("write check.py: %v", err)
	}
	runCompile(t, base, nil, "python3", "check.py")
}

// pyModuleAvailable reports whether python3 can import the given module.
func pyModuleAvailable(mod string) bool {
	if _, err := exec.LookPath("python3"); err != nil {
		return false
	}
	return exec.Command("python3", "-c", "import "+mod).Run() == nil
}

// TestCompilePythonSQLAlchemy type-checks the branded sqlalchemy models: enum
// columns are sa.Enum(PyEnumClass) with the branded StrEnum both as column type
// and Mapped[...] annotation (roadmap 4.1). It needs mypy AND an importable
// sqlalchemy (a library, not a toolchain binary); it SKIPS when sqlalchemy is
// absent even under PGDESIGN_REQUIRE_COMPILE_TOOLCHAINS, since that variable
// gates toolchain binaries, not optional third-party libraries. CI must
// provision sqlalchemy for this check to run.
func TestCompilePythonSQLAlchemy(t *testing.T) {
	requireTool(t, "mypy")
	if !pyModuleAvailable("sqlalchemy") {
		t.Skip("sqlalchemy not importable (provision it in CI to run this check)")
	}
	dir := t.TempDir()
	out, diags := (&codegen.PythonSQLAlchemyGenerator{}).Generate(loadCompileSchema(t))
	failOnCompileErrDiags(t, diags)
	writeFiles(t, dir, map[string][]byte{"models.py": out})
	runCompile(t, dir, nil, "mypy", "--strict", "models.py")
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

// javaxPersistenceStubs are minimal source stubs for the javax.persistence
// annotations/types the JPA output uses. The real JPA jar is the only blocker
// to compiling generated JPA code (roadmap 4.1's stub-javax strategy); these
// stubs let javac verify the generated entities, converters, and enum classes
// type-check without pulling a dependency.
var javaxPersistenceStubs = map[string]string{
	"javax/persistence/Entity.java":     "package javax.persistence;\npublic @interface Entity {}\n",
	"javax/persistence/Table.java":      "package javax.persistence;\npublic @interface Table { String name() default \"\"; }\n",
	"javax/persistence/Id.java":         "package javax.persistence;\npublic @interface Id {}\n",
	"javax/persistence/Column.java":     "package javax.persistence;\npublic @interface Column { String name() default \"\"; boolean nullable() default true; String columnDefinition() default \"\"; }\n",
	"javax/persistence/JoinColumn.java": "package javax.persistence;\npublic @interface JoinColumn { String name() default \"\"; boolean nullable() default true; }\n",
	"javax/persistence/ManyToOne.java":  "package javax.persistence;\npublic @interface ManyToOne { FetchType fetch() default FetchType.EAGER; }\n",
	"javax/persistence/OneToMany.java":  "package javax.persistence;\npublic @interface OneToMany { String mappedBy() default \"\"; }\n",
	"javax/persistence/FetchType.java":  "package javax.persistence;\npublic enum FetchType { LAZY, EAGER }\n",
	"javax/persistence/Convert.java":    "package javax.persistence;\npublic @interface Convert { Class<?> converter() default void.class; }\n",
	"javax/persistence/Converter.java":  "package javax.persistence;\npublic @interface Converter { boolean autoApply() default false; }\n",
	"javax/persistence/AttributeConverter.java": "package javax.persistence;\npublic interface AttributeConverter<X, Y> { Y convertToDatabaseColumn(X attribute); X convertToEntityAttribute(Y dbData); }\n",
}

// TestCompileJavaJPA compiles the branded JPA output: entities using
// @Convert(converter = XxxConverter.class) on enum columns, generated enum
// classes with fromValue()/getValue(), and AttributeConverters backed by them
// (never @Enumerated(STRING)). Compiled against stub javax.persistence sources
// (roadmap 4.1).
func TestCompileJavaJPA(t *testing.T) {
	requireTool(t, "javac")
	dir := t.TempDir()
	files, diags := (&codegen.JavaJPAGenerator{}).GenerateFiles(loadCompileSchema(t))
	failOnCompileErrDiags(t, diags)
	names := writeFiles(t, dir, files)
	stubBytes := make(map[string][]byte, len(javaxPersistenceStubs))
	for rel, src := range javaxPersistenceStubs {
		stubBytes[rel] = []byte(src)
	}
	names = append(names, writeFiles(t, dir, stubBytes)...)

	// Runtime witness (roadmap 4.1 "Java persisted value == getValue()"): the
	// converter persists getValue() and reconstructs via fromValue(), and
	// fromValue rejects undefined values.
	main := `public class JpaCheck {
    public static void main(String[] args) {
        RoleConverter c = new RoleConverter();
        String persisted = c.convertToDatabaseColumn(Role.ADMIN);
        if (!persisted.equals(Role.ADMIN.getValue())) {
            throw new AssertionError("persisted != getValue: " + persisted);
        }
        if (c.convertToEntityAttribute("admin") != Role.ADMIN) {
            throw new AssertionError("fromValue round-trip failed");
        }
        boolean rejected = false;
        try {
            Role.fromValue("bogus");
        } catch (IllegalArgumentException e) {
            rejected = true;
        }
        if (!rejected) {
            throw new AssertionError("fromValue must reject undefined values");
        }
        System.out.println("OK");
    }
}
`
	names = append(names, writeFiles(t, dir, map[string][]byte{"JpaCheck.java": []byte(main)})...)
	runCompile(t, dir, nil, "javac", names...)
	if _, err := exec.LookPath("java"); err == nil {
		runCompile(t, dir, nil, "java", "JpaCheck")
	}
}

// TestCompileJavaConstraints compiles the branded Java constraints validators
// alongside the Java types they validate. Enum checks compare against the
// branded enum's getValue() (roadmap 4.1 "Java contains(getValue())"), so the
// validators only compile against the branded record types + enum classes.
func TestCompileJavaConstraints(t *testing.T) {
	requireTool(t, "javac")
	dir := t.TempDir()
	schema := loadCompileSchema(t)
	typeFiles, tdiags := (&codegen.JavaTypesGenerator{}).GenerateFiles(schema)
	failOnCompileErrDiags(t, tdiags)
	conFiles, cdiags := (&codegen.JavaConstraintsGenerator{}).GenerateFiles(schema)
	failOnCompileErrDiags(t, cdiags)
	names := writeFiles(t, dir, typeFiles)
	names = append(names, writeFiles(t, dir, conFiles)...)
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
