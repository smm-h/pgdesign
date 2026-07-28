package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/codegen"
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/generate"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/splitfmt"
	"github.com/smm-h/pgdesign/internal/sqlparse"
	"github.com/smm-h/pgdesign/pkg/genkit"
)

// PlanResult holds the output of Plan: a map of absolute file paths to their
// planned content, plus any diagnostics collected during generation.
type PlanResult struct {
	// Files maps absolute file path to planned content.
	Files map[string][]byte
	// OwnedDirs maps each output directory owned by a multi-file codegen
	// output to the set of slash-separated relative file paths planned
	// inside it. Files found on disk inside an owned directory that are
	// neither in this set nor on the orphan ignore list (see orphanIgnored)
	// are orphans -- a hard error during build and check. Single-file
	// outputs own nothing; only MultiFileGenerator outputs own their
	// directory. Two outputs sharing a directory union their sets.
	OwnedDirs map[string]map[string]bool
	// Diagnostics collected during generation (warnings, info).
	Diagnostics []diagnostic.Diagnostic
}

// Plan generates all configured build outputs in memory without writing to disk.
// It iterates cfg.Output, generates content for each output, and returns a map
// of absolute file path to file content. SVG outputs are excluded because d2
// rendering is non-deterministic (layout engine produces different coordinates
// across runs), making byte-for-byte comparison unreliable.
func Plan(schema *model.Schema, cfg *config.ResolvedConfig, registry *semtype.Registry) (*PlanResult, error) {
	if err := validateGoCodegenColocation(cfg); err != nil {
		return nil, err
	}

	result := &PlanResult{
		Files:     make(map[string][]byte),
		OwnedDirs: make(map[string]map[string]bool),
	}

	// Thread the full-project revision (roadmap 4.2, L6). It is computed once
	// over the UNFILTERED, TOML-built model (registry-present class, L7) so every
	// output — including group/source-filtered ones — carries the SAME
	// full-project stamp: provenance, not content (byte-compare owns content).
	// genkit.Header injects it into all generator banners; reset after so a
	// later generator call outside a funnel emits the bare banner.
	projectRev, err := rev.Compute(schema, rev.RegistryPresent)
	if err != nil {
		return nil, fmt.Errorf("build: compute project revision: %w", err)
	}
	if err := genkit.SetRevision(projectRev.String()); err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}
	defer genkit.SetRevision("")

	extReg := extregistry.NewBuiltinRegistry()
	extReg.LoadUserExtensions(configToUserExtensions(cfg.Extensions))

	// Sort output names for deterministic ordering.
	names := make([]string, 0, len(cfg.Output))
	for name := range cfg.Output {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		out := cfg.Output[name]
		outPath := string(out.Path)

		// SVG outputs are excluded: d2 rendering is non-deterministic (the
		// layout engine produces different coordinates across runs), so
		// byte-for-byte comparison is unreliable for freshness checks.
		if out.Format == "svg" {
			continue
		}

		// Filter schema tables by groups and/or source files when configured.
		outputSchema := applyOutputFilters(schema, out.Groups, out.Source)

		switch out.Format {
		case "sql":
			content, err := planSQL(name, outputSchema, out, outPath, registry, extReg, result)
			if err != nil {
				return nil, err
			}
			result.Files[outPath] = content

		case "d2":
			content, err := planGenerate(name, outputSchema, "d2", out, registry, extReg, result)
			if err != nil {
				return nil, err
			}
			result.Files[outPath] = []byte(genkit.Header(genkit.CommentHash) + "\n" + string(content))

		case "json":
			content, err := planGenerate(name, outputSchema, "json", out, registry, extReg, result)
			if err != nil {
				return nil, err
			}
			result.Files[outPath] = content

		case "doc":
			content, err := planGenerate(name, outputSchema, "doc", out, registry, extReg, result)
			if err != nil {
				return nil, err
			}
			// doc is a Markdown data dictionary; the hash-comment banner (a
			// Markdown heading) carries the full-project stamp (roadmap 4.2:
			// doc is comment-stamped).
			result.Files[outPath] = []byte(genkit.Header(genkit.CommentHash) + "\n" + string(content))

		case "graphql":
			content, err := planGenerate(name, outputSchema, "graphql", out, registry, extReg, result)
			if err != nil {
				return nil, err
			}
			result.Files[outPath] = []byte(genkit.Header(genkit.CommentHash) + "\n" + string(content))

		case "codegen":
			if err := planCodegen(name, outputSchema, out, outPath, result); err != nil {
				return nil, err
			}

		default:
			return nil, fmt.Errorf("build: output %q: unsupported format %q", name, out.Format)
		}
	}

	// Any configured output path (any format, including SVG outputs that are
	// skipped from planning because d2 rendering is non-deterministic) that
	// falls inside an owned directory is treated as owned, not orphaned. The
	// same applies to companion files already in the plan (e.g., .sqlsplit).
	if len(result.OwnedDirs) > 0 {
		for _, name := range names {
			result.markOwnedIfInside(string(cfg.Output[name].Path))
		}
		for fp := range result.Files {
			result.markOwnedIfInside(fp)
		}
	}

	return result, nil
}

// validateGoCodegenColocation enforces the Go `package schema` co-location
// rules. Two Go codegen modes emit the branded row structs and enum types into
// `package schema`: `types` and `gorm`. They are STRUCT PROVIDERS. The
// `constraints` mode emits `package schema` too, but defines no row structs or
// enums — it references the provider's structs (Accounts) and branded enums
// (Role, via .String()/.IsValid()) by bare name. Go has no configurable
// cross-package import path for the schema package, so all three only compile
// when co-located in ONE directory. Two rules follow:
//
//  1. At most one struct provider per directory. `types` and `gorm` define the
//     SAME row struct names, so two providers in one directory produce duplicate
//     definitions that do not compile (enum dedup never deduplicated row
//     structs). Hard error naming the directory and the offending modes.
//
//  2. A `constraints` output must sit in a directory that also contains a struct
//     provider (types or gorm) — whichever is configured. When a provider is
//     configured but in a DIFFERENT directory, the pair cannot compile: hard
//     error naming both directories. The check fires only when a provider is
//     configured somewhere; a lone constraints output (no provider anywhere) may
//     be completed by hand-written or externally-generated structs and is left
//     alone.
func validateGoCodegenColocation(cfg *config.ResolvedConfig) error {
	providerModesByDir := make(map[string][]string) // dir -> struct-provider modes (types/gorm)
	var constraintsDirs []string
	for _, out := range cfg.Output {
		if out.Format != "codegen" || out.Lang != "go" {
			continue
		}
		dir := filepath.Dir(string(out.Path))
		switch out.Mode {
		case "types", "gorm":
			providerModesByDir[dir] = append(providerModesByDir[dir], out.Mode)
		case "constraints":
			constraintsDirs = append(constraintsDirs, dir)
		}
	}

	// Rule 1: at most one struct provider per directory. Iterate dirs in sorted
	// order so the reported error is deterministic when several dirs offend.
	providerDirs := make([]string, 0, len(providerModesByDir))
	for d := range providerModesByDir {
		providerDirs = append(providerDirs, d)
	}
	sort.Strings(providerDirs)
	for _, dir := range providerDirs {
		modes := providerModesByDir[dir]
		if len(modes) > 1 {
			sorted := append([]string(nil), modes...)
			sort.Strings(sorted)
			return fmt.Errorf("build: Go codegen directory %q has multiple struct-providing outputs (%s): "+
				"the Go `types` and `gorm` modes each define the same branded row structs and enum types in `package schema`, "+
				"so at most one may target a given directory — duplicate definitions would not compile; "+
				"split them into separate directories (each is a self-contained package)",
				dir, strings.Join(sorted, ", "))
		}
	}

	// Rule 2: constraints must co-locate with a struct provider, when one is
	// configured anywhere.
	if len(providerDirs) == 0 || len(constraintsDirs) == 0 {
		return nil
	}
	for _, cd := range constraintsDirs {
		if _, ok := providerModesByDir[cd]; !ok {
			return fmt.Errorf("build: Go constraints output directory %q has no Go struct provider (types or gorm); "+
				"the configured provider directories are %q: "+
				"the constraints file emits `package schema` and references the row structs and branded enums a provider defines by bare name, "+
				"so it must be generated into the same directory as a `types` or `gorm` output to compile",
				cd, strings.Join(providerDirs, ", "))
		}
	}
	return nil
}

// applyOutputFilters narrows schema for a single output per its group and
// source configuration. Both filters compose via AND: groups narrows first,
// source narrows further. This is the single filtering rule shared by `build`
// and the standalone `codegen` command so the same artifact never has two
// contents depending on the entry point.
func applyOutputFilters(schema *model.Schema, groups, source []string) *model.Schema {
	out := schema
	if len(groups) > 0 {
		out = out.FilterByGroups(groups)
	}
	if len(source) > 0 {
		out = out.FilterBySource(source)
	}
	return out
}

// PlanStandaloneCodegen plans a single codegen output through the exact planner
// path `build` uses: group/source filtering, header handling, and owned-dir
// bookkeeping all match build. The standalone `codegen` command is a thin
// caller of this, so its written artifacts and orphan behavior are identical to
// build's for the same output configuration.
func PlanStandaloneCodegen(schema *model.Schema, out config.OutputConfig[config.AbsolutePath]) (*PlanResult, error) {
	result := &PlanResult{
		Files:     make(map[string][]byte),
		OwnedDirs: make(map[string]map[string]bool),
	}
	// Full-project revision from the UNFILTERED model, so a group/source-filtered
	// standalone codegen output carries the same full-project stamp build would
	// give it (roadmap 4.2). schema is the caller's unfiltered model; the filter
	// below narrows content only.
	projectRev, err := rev.Compute(schema, rev.RegistryPresent)
	if err != nil {
		return nil, fmt.Errorf("codegen: compute project revision: %w", err)
	}
	if err := genkit.SetRevision(projectRev.String()); err != nil {
		return nil, fmt.Errorf("codegen: %w", err)
	}
	defer genkit.SetRevision("")

	outputSchema := applyOutputFilters(schema, out.Groups, out.Source)
	if err := planCodegen("codegen", outputSchema, out, string(out.Path), result); err != nil {
		return nil, err
	}
	// Mirror Plan's post-pass: configured/produced paths inside an owned
	// directory are owned, never orphans.
	if len(result.OwnedDirs) > 0 {
		result.markOwnedIfInside(string(out.Path))
		for fp := range result.Files {
			result.markOwnedIfInside(fp)
		}
	}
	return result, nil
}

// markOwnedIfInside adds path to the owned set of any owned directory that
// contains it, so planned or configured outputs inside an owned directory are
// never classified as orphans.
func (r *PlanResult) markOwnedIfInside(path string) {
	for dir, owned := range r.OwnedDirs {
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		owned[rel] = true
	}
}

// planGenerate handles generation via generate.Generate for a single format.
func planGenerate(name string, schema *model.Schema, format string, out config.OutputConfig[config.AbsolutePath], registry *semtype.Registry, extReg *extregistry.Registry, result *PlanResult) ([]byte, error) {
	opts := generate.Options{
		Format:       format,
		TypeRegistry: registry,
		ExtRegistry:  extReg,
		// build operates on a TOML-built model: registry-present class (L7).
		// Ignored for every format except json.
		ModelClass: rev.RegistryPresent,
	}
	if format == "d2" || format == "svg" {
		d2opts := d2OptionsFromConfig(out.D2)
		opts.D2 = &d2opts
	}

	content, genDiags, err := generate.Generate(schema, opts)
	if err != nil {
		return nil, fmt.Errorf("build: output %q: %w", name, err)
	}
	result.Diagnostics = append(result.Diagnostics, genDiags...)
	return []byte(content), nil
}

// d2OptionsFromConfig maps the [output.<name>.d2] config subsection onto
// generate.D2Options. It starts from the intended defaults so an absent
// subsection (nil) yields DefaultD2Options, and each opt-out layer overrides
// only when explicitly set in TOML.
func d2OptionsFromConfig(c *config.D2Config) generate.D2Options {
	opts := generate.DefaultD2Options()
	if c == nil {
		return opts
	}
	if c.Layout != "" {
		opts.Layout = c.Layout
	}
	if c.Theme != 0 {
		opts.Theme = c.Theme
	}
	if c.Direction != "" {
		opts.Direction = c.Direction
	}
	setBool := func(p *bool, target *bool) {
		if p != nil {
			*target = *p
		}
	}
	setBool(c.IndexMarkers, &opts.IndexMarkers)
	setBool(c.Nullable, &opts.Nullable)
	setBool(c.Comments, &opts.Comments)
	setBool(c.Checks, &opts.Checks)
	setBool(c.RLSMarkers, &opts.RLSMarkers)
	setBool(c.Enums, &opts.Enums)
	setBool(c.Cardinality, &opts.Cardinality)
	opts.Include = c.Include
	opts.Exclude = c.Exclude
	opts.IncludeDependencies = c.IncludeDependencies
	opts.Summary = c.Summary
	opts.HeatMap = c.HeatMap
	return opts
}

// planSQL generates the SQL output plus its .sqlsplit companion file.
func planSQL(name string, schema *model.Schema, out config.OutputConfig[config.AbsolutePath], outPath string, registry *semtype.Registry, extReg *extregistry.Registry, result *PlanResult) ([]byte, error) {
	comments := true
	if out.Comments != nil {
		comments = *out.Comments
	}

	sqlResult, genDiags, err := generate.Generate(schema, generate.Options{
		Idempotent:      out.Idempotent,
		IncludeComments: comments,
		Format:          "sql",
		TypeRegistry:    registry,
		ExtRegistry:     extReg,
	})
	if err != nil {
		return nil, fmt.Errorf("build: output %q: %w", name, err)
	}
	result.Diagnostics = append(result.Diagnostics, genDiags...)

	header := genkit.Header(genkit.CommentDash) + "\n"
	content := []byte(header + sqlResult)

	// Generate .sqlsplit companion file using the splitfmt format.
	splitPath := outPath + ".sqlsplit"
	stmts, splitErr := sqlparse.SplitStatements(sqlResult)
	if splitErr != nil {
		result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
			Severity: diagnostic.Warning,
			Message:  fmt.Sprintf("sqlsplit for %q: %v", name, splitErr),
		})
	} else {
		result.Files[splitPath] = splitfmt.Encode(stmts)
	}

	return content, nil
}

// planCodegen handles codegen outputs, including both single-file and multi-file generators.
func planCodegen(name string, schema *model.Schema, out config.OutputConfig[config.AbsolutePath], outPath string, result *PlanResult) error {
	gen, ok := selectCodegenGenerator(name, out.Lang, out.Mode)
	if !ok {
		return fmt.Errorf("build: output %q: unsupported codegen lang=%q mode=%q", name, out.Lang, out.Mode)
	}

	// Configure backend selection for query-layer generators.
	if qlg, ok := gen.(*codegen.PythonQueryLayerGenerator); ok && len(out.Backends) > 0 {
		qlg.Backends = out.Backends
	}

	// Configure split mode for Python DDL generators.
	if ddlGen, ok := gen.(*codegen.PythonDDLGenerator); ok && out.SplitMode != "" {
		ddlGen.SplitMode = codegen.SplitMode(out.SplitMode)
	}

	// MultiFileGenerator: collect all files into the plan. The output path is
	// a directory that the output now owns (see PlanResult.OwnedDirs); when
	// two outputs share a directory, their owned sets are unioned.
	if mfg, ok := gen.(genkit.MultiFileGenerator); ok {
		files, diags := mfg.GenerateFiles(schema)
		result.Diagnostics = append(result.Diagnostics, diags...)
		owned := result.OwnedDirs[outPath]
		if owned == nil {
			owned = make(map[string]bool, len(files))
			result.OwnedDirs[outPath] = owned
		}
		for relPath, data := range files {
			fp := filepath.Join(outPath, relPath)
			result.Files[fp] = data
			owned[filepath.ToSlash(filepath.Clean(relPath))] = true
		}
		return nil
	}

	// Single-file codegen.
	genResult, diags := gen.Generate(schema)
	result.Diagnostics = append(result.Diagnostics, diags...)

	header := codegenHeader(out.Lang)
	if hasCommentHeader(genResult) {
		result.Files[outPath] = genResult
	} else {
		content := make([]byte, len(header)+len(genResult))
		copy(content, header)
		copy(content[len(header):], genResult)
		result.Files[outPath] = content
	}

	return nil
}
