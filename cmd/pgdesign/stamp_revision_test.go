package main

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/typeinfo"
	"github.com/smm-h/pgdesign/pkg/genkit"
)

// These tests pin roadmap 4.2 (revision stamping, L6): every stampable build
// output carries the FULL-PROJECT revision as its provenance stamp; the stamp is
// deterministic across unchanged rebuilds; a schema edit flips it; filtered
// outputs carry the full-project stamp (provenance, not content); the sealed
// .sqlsplit companion stays stamp-free.

// twoTableGroupedSchema returns a two-table schema with a "core" group holding
// only users, for the filtered-output stamp test.
func twoTableGroupedSchema() *model.Schema {
	return &model.Schema{
		PGVersion: 16,
		Tables: []model.Table{
			{
				Name: "users", Schema: "public", Comment: "User accounts",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "name", PGType: typeinfo.T("text"), NotNull: true},
				},
				PK: []string{"id"},
			},
			{
				Name: "orders", Schema: "public", Comment: "Order records",
				Columns: []model.Column{
					{Name: "id", PGType: typeinfo.T("uuid"), NotNull: true},
					{Name: "total", PGType: typeinfo.T("numeric"), NotNull: true},
				},
				PK: []string{"id"},
			},
		},
		Groups: map[string][]string{"core": {"users"}},
	}
}

// fullProjectRevision returns the revision Plan stamps: rev.Compute over the
// unfiltered, registry-present model.
func fullProjectRevision(t *testing.T, schema *model.Schema) string {
	t.Helper()
	r, err := rev.Compute(schema, rev.RegistryPresent)
	if err != nil {
		t.Fatalf("rev.Compute: %v", err)
	}
	return r.String()
}

// TestStampRevision_DocStampedWithFullProjectRevision verifies the doc output is
// comment-stamped (roadmap 4.2 "doc stamped") and that the stamp is the
// full-project revision.
func TestStampRevision_DocStampedWithFullProjectRevision(t *testing.T) {
	schema := minimalSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"doc": {Format: "doc", Path: "/tmp/test-plan/schema.md"},
		},
	}
	result, err := Plan(schema, cfg, semtype.NewRegistry())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content, ok := result.Files["/tmp/test-plan/schema.md"]
	if !ok {
		t.Fatal("expected doc output in Files")
	}
	ps, ok := genkit.ParseStamp(content)
	if !ok {
		t.Fatalf("doc output is not stamped:\n%.120s", content)
	}
	if want := fullProjectRevision(t, schema); ps.Revision != want {
		t.Errorf("doc stamp revision = %q, want full-project %q", ps.Revision, want)
	}
}

// TestStampRevision_EveryStampableOutputCarriesRevision verifies sql, d2,
// graphql, doc, and codegen outputs each carry the full-project revision line.
func TestStampRevision_EveryStampableOutputCarriesRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("sql output generates .sqlsplit via WASM parser")
	}
	schema := minimalSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"sql":      {Format: "sql", Path: "/tmp/test-plan/s.sql"},
			"d2":       {Format: "d2", Path: "/tmp/test-plan/s.d2"},
			"graphql":  {Format: "graphql", Path: "/tmp/test-plan/s.graphql"},
			"doc":      {Format: "doc", Path: "/tmp/test-plan/s.md"},
			"go_const": {Format: "codegen", Path: "/tmp/test-plan/tables.go", Lang: "go", Mode: "constants"},
		},
	}
	result, err := Plan(schema, cfg, semtype.NewRegistry())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := fullProjectRevision(t, schema)
	for _, p := range []string{
		"/tmp/test-plan/s.sql", "/tmp/test-plan/s.d2", "/tmp/test-plan/s.graphql",
		"/tmp/test-plan/s.md", "/tmp/test-plan/tables.go",
	} {
		content, ok := result.Files[p]
		if !ok {
			t.Errorf("missing output %s", p)
			continue
		}
		ps, ok := genkit.ParseStamp(content)
		if !ok {
			t.Errorf("%s is not stamped:\n%.120s", p, content)
			continue
		}
		if ps.Revision != want {
			t.Errorf("%s stamp revision = %q, want %q", p, ps.Revision, want)
		}
	}
}

// TestStampRevision_RebuildWithoutChangeStaysGreen verifies the stamp is
// deterministic: two Plans of the same schema produce byte-identical files, so
// freshness/byte-compare is unaffected by an unchanged rebuild.
func TestStampRevision_RebuildWithoutChangeStaysGreen(t *testing.T) {
	schema := minimalSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"doc":      {Format: "doc", Path: "/tmp/test-plan/s.md"},
			"go_const": {Format: "codegen", Path: "/tmp/test-plan/tables.go", Lang: "go", Mode: "constants"},
		},
	}
	first, err := Plan(schema, cfg, semtype.NewRegistry())
	if err != nil {
		t.Fatalf("Plan #1: %v", err)
	}
	second, err := Plan(schema, cfg, semtype.NewRegistry())
	if err != nil {
		t.Fatalf("Plan #2: %v", err)
	}
	if len(first.Files) != len(second.Files) {
		t.Fatalf("file count differs: %d vs %d", len(first.Files), len(second.Files))
	}
	for p, a := range first.Files {
		b, ok := second.Files[p]
		if !ok {
			t.Errorf("%s missing on rebuild", p)
			continue
		}
		if string(a) != string(b) {
			t.Errorf("%s differs across unchanged rebuild (stamp non-deterministic)", p)
		}
	}
}

// TestStampRevision_SchemaEditFlipsStamp verifies that a schema edit changes the
// full-project revision, so every stamped output flips exactly once (the stamp
// line changes even where filtered content would not).
func TestStampRevision_SchemaEditFlipsStamp(t *testing.T) {
	base := minimalSchema()
	edited := minimalSchema()
	edited.Tables[0].Columns = append(edited.Tables[0].Columns,
		model.Column{Name: "email", PGType: typeinfo.T("text"), NotNull: true})

	rBase := fullProjectRevision(t, base)
	rEdited := fullProjectRevision(t, edited)
	if rBase == rEdited {
		t.Fatal("schema edit did not change the full-project revision")
	}

	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"doc": {Format: "doc", Path: "/tmp/test-plan/s.md"},
		},
	}
	planBase, err := Plan(base, cfg, semtype.NewRegistry())
	if err != nil {
		t.Fatalf("Plan base: %v", err)
	}
	planEdited, err := Plan(edited, cfg, semtype.NewRegistry())
	if err != nil {
		t.Fatalf("Plan edited: %v", err)
	}
	psBase, _ := genkit.ParseStamp(planBase.Files["/tmp/test-plan/s.md"])
	psEdited, _ := genkit.ParseStamp(planEdited.Files["/tmp/test-plan/s.md"])
	if psBase.Revision != rBase {
		t.Errorf("base doc stamp = %q, want %q", psBase.Revision, rBase)
	}
	if psEdited.Revision != rEdited {
		t.Errorf("edited doc stamp = %q, want %q", psEdited.Revision, rEdited)
	}
}

// TestStampRevision_FilteredOutputCarriesFullProjectStamp verifies a
// group-filtered output whose CONTENT excludes a table nonetheless carries the
// FULL-PROJECT revision as its stamp (provenance, not content).
func TestStampRevision_FilteredOutputCarriesFullProjectStamp(t *testing.T) {
	schema := twoTableGroupedSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			// doc avoids the WASM .sqlsplit dependency so this runs under -short.
			"core_doc": {Format: "doc", Path: "/tmp/test-plan/core.md", Groups: []string{"core"}},
		},
	}
	result, err := Plan(schema, cfg, semtype.NewRegistry())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content, ok := result.Files["/tmp/test-plan/core.md"]
	if !ok {
		t.Fatal("expected core_doc output")
	}
	s := string(content)
	// Content is filtered: orders excluded, users included.
	if strings.Contains(s, "orders") {
		t.Error("filtered doc should not mention 'orders'")
	}
	if !strings.Contains(s, "users") {
		t.Error("filtered doc should mention 'users'")
	}
	// Stamp is the FULL-PROJECT revision (computed over the unfiltered schema).
	ps, ok := genkit.ParseStamp(content)
	if !ok {
		t.Fatalf("filtered doc is not stamped:\n%.120s", content)
	}
	if want := fullProjectRevision(t, schema); ps.Revision != want {
		t.Errorf("filtered output stamp = %q, want full-project %q", ps.Revision, want)
	}
}

// TestStampRevision_SqlsplitStampFree verifies the sealed .sqlsplit companion
// carries no stamp (line 1 is the statement count) and stays byte-stable across
// rebuilds, while its sibling .sql IS stamped.
func TestStampRevision_SqlsplitStampFree(t *testing.T) {
	if testing.Short() {
		t.Skip("Plan() generates .sqlsplit via WASM parser")
	}
	schema := minimalSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"main": {Format: "sql", Path: "/tmp/test-plan/t.sql"},
		},
	}
	first, err := Plan(schema, cfg, semtype.NewRegistry())
	if err != nil {
		t.Fatalf("Plan #1: %v", err)
	}
	split, ok := first.Files["/tmp/test-plan/t.sql.sqlsplit"]
	if !ok {
		t.Fatal("expected .sqlsplit companion")
	}
	if genkit.HasStamp(split) {
		t.Errorf(".sqlsplit must be stamp-free (sealed format), got:\n%.120s", split)
	}
	// Sibling .sql IS stamped with the full-project revision.
	sqlPS, ok := genkit.ParseStamp(first.Files["/tmp/test-plan/t.sql"])
	if !ok || sqlPS.Revision != fullProjectRevision(t, schema) {
		t.Errorf(".sql sibling should carry the full-project stamp, got revision %q", sqlPS.Revision)
	}
	// Byte-stable across an unchanged rebuild.
	second, err := Plan(schema, cfg, semtype.NewRegistry())
	if err != nil {
		t.Fatalf("Plan #2: %v", err)
	}
	if string(split) != string(second.Files["/tmp/test-plan/t.sql.sqlsplit"]) {
		t.Error(".sqlsplit differs across unchanged rebuild")
	}
}

// TestValidateGoCodegenColocation_MismatchIsHardError verifies rider 2: a build
// configuring both a Go types output and a Go constraints output in DIFFERENT
// directories is a hard error naming both directories.
func TestValidateGoCodegenColocation_MismatchIsHardError(t *testing.T) {
	schema := minimalSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"types":       {Format: "codegen", Path: "/tmp/a/types.go", Lang: "go", Mode: "types"},
			"constraints": {Format: "codegen", Path: "/tmp/b/constraints.go", Lang: "go", Mode: "constraints"},
		},
	}
	_, err := Plan(schema, cfg, semtype.NewRegistry())
	if err == nil {
		t.Fatal("expected hard error for split-directory Go types/constraints")
	}
	msg := err.Error()
	if !strings.Contains(msg, "/tmp/a") || !strings.Contains(msg, "/tmp/b") {
		t.Errorf("error should name both directories, got: %s", msg)
	}
}

// TestValidateGoCodegenColocation_SameDirOK verifies co-located Go
// types+constraints outputs pass, and a lone constraints output (no types) is
// not gated.
func TestValidateGoCodegenColocation_SameDirOK(t *testing.T) {
	schema := minimalSchema()
	same := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"types":       {Format: "codegen", Path: "/tmp/schema/types.go", Lang: "go", Mode: "types"},
			"constraints": {Format: "codegen", Path: "/tmp/schema/constraints.go", Lang: "go", Mode: "constraints"},
		},
	}
	if _, err := Plan(schema, same, semtype.NewRegistry()); err != nil {
		t.Fatalf("co-located Go types+constraints should be allowed: %v", err)
	}
	loneConstraints := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"constraints": {Format: "codegen", Path: "/tmp/b/constraints.go", Lang: "go", Mode: "constraints"},
		},
	}
	if _, err := Plan(schema, loneConstraints, semtype.NewRegistry()); err != nil {
		t.Fatalf("lone Go constraints output should not be gated: %v", err)
	}
}

// TestValidateGoCodegenColocation_GormConstraintsMismatchIsHardError verifies the
// guard treats Go `gorm` as a struct provider for `constraints` exactly like Go
// `types`: gorm also emits the branded row structs and enums into package schema
// that constraints references by bare name. A gorm+constraints pair in DIFFERENT
// directories cannot compile and is a hard error naming both directories.
func TestValidateGoCodegenColocation_GormConstraintsMismatchIsHardError(t *testing.T) {
	schema := minimalSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"gorm":        {Format: "codegen", Path: "/tmp/a/gorm.go", Lang: "go", Mode: "gorm"},
			"constraints": {Format: "codegen", Path: "/tmp/b/constraints.go", Lang: "go", Mode: "constraints"},
		},
	}
	_, err := Plan(schema, cfg, semtype.NewRegistry())
	if err == nil {
		t.Fatal("expected hard error for split-directory Go gorm/constraints")
	}
	msg := err.Error()
	if !strings.Contains(msg, "/tmp/a") || !strings.Contains(msg, "/tmp/b") {
		t.Errorf("error should name both directories, got: %s", msg)
	}
}

// TestValidateGoCodegenColocation_GormConstraintsSameDirOK verifies co-located Go
// gorm+constraints outputs pass: gorm supplies the row structs and branded enums
// the constraints file validates, so one directory compiles.
func TestValidateGoCodegenColocation_GormConstraintsSameDirOK(t *testing.T) {
	schema := minimalSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"gorm":        {Format: "codegen", Path: "/tmp/schema/gorm.go", Lang: "go", Mode: "gorm"},
			"constraints": {Format: "codegen", Path: "/tmp/schema/constraints.go", Lang: "go", Mode: "constraints"},
		},
	}
	if _, err := Plan(schema, cfg, semtype.NewRegistry()); err != nil {
		t.Fatalf("co-located Go gorm+constraints should be allowed: %v", err)
	}
}

// TestValidateGoCodegenColocation_TypesGormSameDirIsHardError verifies that Go
// `types` and `gorm` are mutually exclusive struct providers per directory: both
// define the SAME branded row structs (type Accounts struct) in package schema,
// so two providers in one directory produce duplicate definitions that do not
// compile (enum dedup never deduplicated row structs). This is a hard error
// naming the directory.
func TestValidateGoCodegenColocation_TypesGormSameDirIsHardError(t *testing.T) {
	schema := minimalSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"types": {Format: "codegen", Path: "/tmp/schema/types.go", Lang: "go", Mode: "types"},
			"gorm":  {Format: "codegen", Path: "/tmp/schema/gorm.go", Lang: "go", Mode: "gorm"},
		},
	}
	_, err := Plan(schema, cfg, semtype.NewRegistry())
	if err == nil {
		t.Fatal("expected hard error for Go types+gorm sharing one directory")
	}
	msg := err.Error()
	if !strings.Contains(msg, "/tmp/schema") {
		t.Errorf("error should name the shared directory, got: %s", msg)
	}
	if !strings.Contains(msg, "types") || !strings.Contains(msg, "gorm") {
		t.Errorf("error should name both struct-providing modes, got: %s", msg)
	}
}

// TestValidateGoCodegenColocation_TypesGormSeparateDirsOK verifies that Go
// `types` and `gorm` in SEPARATE directories are allowed: each is a
// self-contained package (each emits its own row structs and enum block), so
// they form two valid, independent packages.
func TestValidateGoCodegenColocation_TypesGormSeparateDirsOK(t *testing.T) {
	schema := minimalSchema()
	cfg := &config.ResolvedConfig{
		Output: map[string]config.OutputConfig[config.AbsolutePath]{
			"types": {Format: "codegen", Path: "/tmp/a/types.go", Lang: "go", Mode: "types"},
			"gorm":  {Format: "codegen", Path: "/tmp/b/gorm.go", Lang: "go", Mode: "gorm"},
		},
	}
	if _, err := Plan(schema, cfg, semtype.NewRegistry()); err != nil {
		t.Fatalf("Go types and gorm in separate directories should be allowed: %v", err)
	}
}
