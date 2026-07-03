package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/introspect"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/strictcli/go/strictcli"
)

type diffHandler struct {
	JSON    bool     `cli:"json" help:"Output the schema diff in machine-readable JSON format" default:"false"`
	Live    *string  `cli:"live" help:"PostgreSQL connection URL for live database comparison"`
	Against *string  `cli:"against" help:"Path to TOML schema file or directory to compare against"`
	Base    *string  `cli:"base" help:"Git ref to compare the current schema against (e.g., main)"`
	Paths   []string `arg:"path" help:"Path to TOML schema file(s) or directory containing them" variadic:"true"`
}

func (h *diffHandler) Run(ctx *strictcli.Context) int {
	cfgOverride := configOverride(ctx)

	paths := h.Paths
	schema, _, exitCode := parseAndBuild(cfgOverride, paths)
	if exitCode != 0 {
		return exitCode
	}

	// Determine which mode we're in. Exactly one of --live, --against, --base.
	// Empty-string values are treated as absent, matching the pre-struct
	// handler behavior.
	var liveURL, againstPath, baseRef string
	if h.Live != nil {
		liveURL = *h.Live
	}
	if h.Against != nil {
		againstPath = *h.Against
	}
	if h.Base != nil {
		baseRef = *h.Base
	}

	modeCount := 0
	if liveURL != "" {
		modeCount++
	}
	if againstPath != "" {
		modeCount++
	}
	if baseRef != "" {
		modeCount++
	}

	if modeCount == 0 {
		fmt.Fprintln(os.Stderr, "error: specify one of --live <url>, --against <path>, or --base <ref>")
		return 1
	}
	if modeCount > 1 {
		fmt.Fprintln(os.Stderr, "error: --live, --against, and --base are mutually exclusive")
		return 1
	}

	var actual *model.Schema

	switch {
	case liveURL != "":
		var code int
		actual, code = diffLive(cfgOverride, paths, schema, liveURL)
		if code != 0 {
			return code
		}

	case againstPath != "":
		var code int
		actual, code = diffAgainst(cfgOverride, againstPath)
		if code != 0 {
			return code
		}

	case baseRef != "":
		var code int
		actual, code = diffBase(cfgOverride, paths, baseRef)
		if code != 0 {
			return code
		}
	}

	d := diff.Diff(schema, actual)

	if h.JSON {
		fmt.Println(diff.FormatJSON(d))
		return 0
	}

	fmt.Print(diff.FormatTerminal(d))
	return 0
}

// diffLive introspects a live database and returns the "actual" schema.
func diffLive(configOverride *string, paths []string, schema *model.Schema, dbURL string) (*model.Schema, int) {
	cfg, cfgErr := loadProjectConfig(configOverride, paths[0])
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
		return nil, 1
	}

	schemaNames := []string{"public"}
	if schema.Name != "" && schema.Name != "public" {
		schemaNames = []string{schema.Name}
	} else if cfgNames := configSchemaNames(cfg); len(cfgNames) > 0 {
		schemaNames = cfgNames
	}

	ctx := context.Background()
	actual, diags, err := introspect.Introspect(ctx, dbURL, schemaNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, 1
	}
	if len(diags) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(diags, true))
	}
	if diagnostic.Diagnostics(diags).HasErrors() {
		return nil, 1
	}

	return actual, 0
}

// diffAgainst parses a TOML schema from the --against path and returns the "actual" schema.
func diffAgainst(configOverride *string, againstPath string) (*model.Schema, int) {
	schema, _, exitCode := parseAndBuild(configOverride, []string{againstPath})
	return schema, exitCode
}

// diffBase extracts schema files from a git ref and returns the parsed/built "actual" schema.
func diffBase(configOverride *string, paths []string, ref string) (*model.Schema, int) {
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintln(os.Stderr, "error: git is not available")
		return nil, 1
	}

	repoRoot, err := gitRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, 1
	}

	// Resolve the paths to determine what schema files we need from the ref.
	resolvedPaths, err := resolveSchemaPaths(configOverride, paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, 1
	}

	// Make all resolved paths absolute so filepath.Rel against repoRoot works.
	for i, p := range resolvedPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot resolve absolute path for %s: %v\n", p, err)
			return nil, 1
		}
		resolvedPaths[i] = abs
	}

	// Try to get pgdesign.toml from the ref to discover schema files.
	schemaDir := filepath.Dir(resolvedPaths[0])
	configRelPath, err := filepath.Rel(repoRoot, filepath.Join(schemaDir, "pgdesign.toml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot compute relative path: %v\n", err)
		return nil, 1
	}

	configBytes, configErr := gitShow(ref, configRelPath)

	var filesToExtract []string

	if configErr == nil {
		// pgdesign.toml exists at this ref -- parse it to find schema files.
		refSchemaPaths, err := parseSchemasFromConfigBytes(configBytes, schemaDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: parsing pgdesign.toml from %s: %v\n", ref, err)
			return nil, 1
		}
		filesToExtract = refSchemaPaths
	} else {
		// No pgdesign.toml at this ref -- use the same file paths as the working tree.
		filesToExtract = resolvedPaths
	}

	// Extract and parse each schema file from the git ref.
	var raws []*parse.RawSchema
	var allDiags diagnostic.Diagnostics

	for _, filePath := range filesToExtract {
		relPath, err := filepath.Rel(repoRoot, filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot compute relative path for %s: %v\n", filePath, err)
			return nil, 1
		}

		data, err := gitShow(ref, relPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot extract %s from %s: %v\n", relPath, ref, err)
			return nil, 1
		}

		raw, diags := parse.Bytes(data)
		allDiags = append(allDiags, diags...)
		if raw != nil {
			raws = append(raws, raw)
		}
	}

	if len(raws) == 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(allDiags, true))
		return nil, 1
	}

	parseWarnings := allDiags.Warnings()
	if len(parseWarnings) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(parseWarnings, true))
	}

	reg := semtype.NewBuiltinRegistry()

	// Register extension-provided types so they pass the base type allowlist.
	// Prefer the config from the git ref; fall back to the working tree config.
	if configErr == nil {
		refCfg, err := config.LoadBytes(configBytes)
		if err == nil {
			for _, ext := range refCfg.Extensions {
				reg.AddExtensionTypes(ext.Types)
			}
		}
	} else {
		cfg, cfgErr := loadProjectConfig(configOverride, resolvedPaths[0])
		if cfgErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
			return nil, 1
		}
		for _, ext := range cfg.Extensions {
			reg.AddExtensionTypes(ext.Types)
		}
	}

	for _, raw := range raws {
		userTypes := parse.CollectUserTypes(raw)
		if len(userTypes) > 0 {
			loadDiags := reg.LoadUserTypes(userTypes)
			if loadDiags.HasErrors() {
				fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(loadDiags, true))
				return nil, 1
			}
		}
	}

	var schema *model.Schema
	var buildDiags diagnostic.Diagnostics

	if len(raws) == 1 {
		schema, buildDiags = model.Build(raws[0], reg)
	} else {
		schema, buildDiags = model.BuildMulti(raws, reg)
	}

	if buildDiags.HasErrors() {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(buildDiags, true))
		return nil, 1
	}

	warnings := buildDiags.Warnings()
	if len(warnings) > 0 {
		fmt.Fprint(os.Stderr, diagnostic.RenderTerminal(warnings, true))
	}

	return schema, 0
}

// gitRepoRoot returns the root directory of the current git repository.
func gitRepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitShow extracts a file from a git ref using "git show ref:path".
func gitShow(ref, path string) ([]byte, error) {
	cmd := exec.Command("git", "show", ref+":"+path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s (%s)", strings.TrimSpace(stderr.String()), err)
	}
	return stdout.Bytes(), nil
}

// parseSchemasFromConfigBytes extracts the project.schemas list from pgdesign.toml
// bytes and resolves the paths relative to schemaDir.
func parseSchemasFromConfigBytes(data []byte, schemaDir string) ([]string, error) {
	raw, err := config.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	if len(raw.Project.Schemas) == 0 {
		return nil, fmt.Errorf("pgdesign.toml at this ref has no project.schemas entries")
	}
	resolved, err := config.Resolve(raw, schemaDir)
	if err != nil {
		return nil, err
	}
	return resolved.SchemaFiles(), nil
}
