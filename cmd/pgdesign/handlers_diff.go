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
	"github.com/smm-h/pgdesign/internal/livenorm"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerDiffCmd(app *strictcli.App) {
	app.Command("diff", "Compare schema file(s) or directory against another target",
		func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			cfgOverride := kwargsConfigOverride(kwargs)

			paths := kwargsStrSlice(kwargs["path"])
			schema, _, exitCode := parseAndBuild(cfgOverride, paths)
			if exitCode != 0 {
				return strictcli.Exit(exitCode)
			}

			var liveURL, againstPath, baseRef string
			if v := kwargsOptString(kwargs, "live"); v != nil {
				liveURL = *v
			}
			if v := kwargsOptString(kwargs, "against"); v != nil {
				againstPath = *v
			}
			if v := kwargsOptString(kwargs, "base"); v != nil {
				baseRef = *v
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
				return strictcli.Exit(1)
			}
			if modeCount > 1 {
				fmt.Fprintln(os.Stderr, "error: --live, --against, and --base are mutually exclusive")
				return strictcli.Exit(1)
			}

			var actual *model.Schema
			var ln diff.LiveNormalizer

			switch {
			case liveURL != "":
				var code int
				actual, code = diffLive(schema, liveURL)
				if code != 0 {
					return strictcli.Exit(code)
				}
				// LIVE ROUND-TRIP NORMALIZATION: resolve the catalog-dependent
				// cast residue by round-tripping the desired side through the
				// target DB. Best-effort: if the normalizer connection fails,
				// fall back to pure N (the diff still runs, just without the
				// residue resolution).
				if n, err := livenorm.New(context.Background(), liveURL); err == nil {
					defer n.Close()
					ln = n
				}

			case againstPath != "":
				var code int
				actual, code = diffAgainst(cfgOverride, againstPath)
				if code != 0 {
					return strictcli.Exit(code)
				}

			case baseRef != "":
				var code int
				actual, code = diffBase(cfgOverride, paths, baseRef)
				if code != 0 {
					return strictcli.Exit(code)
				}
			}

			if collErr := diff.CheckTruncationCollisions(schema); collErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", collErr)
				return strictcli.Exit(1)
			}
			// --live compares against an introspected (registry-absent) schema, so
			// class-aware fields (semantic type names) are suppressed. --against and
			// --base compare two registry-present models, so those fields ARE
			// compared (Diff).
			var d *diff.SchemaDiff
			if liveURL != "" {
				d = diff.DiffLive(schema, actual, ln)
			} else {
				d = diff.Diff(schema, actual)
			}

			if kwargs["json"].(bool) {
				fmt.Println(diff.FormatJSON(d))
				return strictcli.Exit(0)
			}

			fmt.Print(diff.FormatTerminal(d))
			return strictcli.Exit(0)
		},
		strictcli.WithFlags(
			strictcli.BoolFlag("json", "Output the schema diff in machine-readable JSON format", strictcli.Default(false)),
			strictcli.StringFlag("live", "PostgreSQL connection URL for live database comparison", strictcli.Default(nil), strictcli.ConnectionURLFlag("PGDESIGN_DB")),
			strictcli.StringFlag("against", "Path to TOML schema file or directory to compare against", strictcli.Default(nil)),
			strictcli.StringFlag("base", "Git ref to compare the current schema against (e.g., main)", strictcli.Default(nil)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("path", "Path to TOML schema file(s) or directory containing them", strictcli.Variadic()),
		),
	)
}

// diffLive introspects a live database and returns the "actual" schema.
func diffLive(schema *model.Schema, dbURL string) (*model.Schema, int) {
	schemaNames := modelSchemaNames(schema)

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

	resolvedPaths, err := resolveSchemaPaths(configOverride, paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, 1
	}

	for i, p := range resolvedPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot resolve absolute path for %s: %v\n", p, err)
			return nil, 1
		}
		resolvedPaths[i] = abs
	}

	schemaDir := filepath.Dir(resolvedPaths[0])
	configRelPath, err := filepath.Rel(repoRoot, filepath.Join(schemaDir, "pgdesign.toml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot compute relative path: %v\n", err)
		return nil, 1
	}

	configBytes, configErr := gitShow(ref, configRelPath)

	var filesToExtract []string

	if configErr == nil {
		refSchemaPaths, err := parseSchemasFromConfigBytes(configBytes, schemaDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: parsing pgdesign.toml from %s: %v\n", ref, err)
			return nil, 1
		}
		filesToExtract = refSchemaPaths
	} else {
		filesToExtract = resolvedPaths
	}

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
