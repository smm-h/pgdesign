package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/imports"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/strictcli/go/strictcli"
)

// registerImportCmds registers the `import` command group: `lock` and `update`
// pin and vendor another pgdesign project's schema surface (roadmap 7.2).
func registerImportCmds(app *strictcli.App) {
	g := app.Group("import", "Pin and vendor imported schema surfaces from other pgdesign projects")
	registerImportLockCmd(g)
	registerImportUpdateCmd(g)
}

func registerImportLockCmd(g *strictcli.Group) {
	g.Command("lock", "Resolve each [imports] alias's git pin, vendor the referenced surface (tables + type closure) into imports/<alias>/, and write the lockfile. Refuses to overwrite an existing lockfile — use `import update` to re-pin.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			return runImportPin(kwargs, false)
		},
		strictcli.WithArgs(
			strictcli.NewArg("alias", "Name of the single [imports] alias to lock; omit to lock every alias declared in pgdesign.toml", strictcli.ArgRequired(false)),
		),
	)
}

func registerImportUpdateCmd(g *strictcli.Group) {
	g.Command("update", "Re-resolve each [imports] alias's git ref and re-vendor its surface, updating the lockfile. Requires an existing lockfile — use `import lock` for the first pin.",
		func(_ *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			return runImportPin(kwargs, true)
		},
		strictcli.WithArgs(
			strictcli.NewArg("alias", "Name of the single [imports] alias to re-resolve and update; omit to update every declared alias", strictcli.ArgRequired(false)),
		),
	)
}

// runImportPin is the shared core of `import lock` (update=false) and
// `import update` (update=true). It builds the consumer model to learn which
// tables each alias references, clones each pinned framework, extracts and
// vendors the surface, and writes the lockfile.
func runImportPin(kwargs map[string]interface{}, update bool) strictcli.Outcome {
	cfgOverride := kwargsConfigOverride(kwargs)

	projectRoot, cfg, err := resolveProjectRootAndConfig(cfgOverride)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return strictcli.Exit(1)
	}
	if len(cfg.Imports) == 0 {
		fmt.Fprintln(os.Stderr, "error: no [imports] declared in pgdesign.toml")
		return strictcli.Exit(1)
	}

	if err := imports.CheckGitAvailable(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return strictcli.Exit(1)
	}

	// Which aliases to pin.
	aliasArg := kwargsOptString(kwargs, "alias")
	var aliases []string
	if aliasArg != nil {
		if _, ok := cfg.Imports[*aliasArg]; !ok {
			fmt.Fprintf(os.Stderr, "error: alias %q is not declared in [imports]\n", *aliasArg)
			return strictcli.Exit(1)
		}
		aliases = []string{*aliasArg}
	} else {
		for a := range cfg.Imports {
			aliases = append(aliases, a)
		}
		sort.Strings(aliases)
	}

	// Build the consumer model to learn referenced tables per alias.
	consumer, _, exitCode := parseAndBuild(cfgOverride, []string{projectRoot})
	if exitCode != 0 {
		return strictcli.Exit(exitCode)
	}
	refByAlias := referencedTablesByAlias(consumer)

	for _, alias := range aliases {
		decl := cfg.Imports[alias]
		exists := imports.LockfileExists(projectRoot, alias)
		if update && !exists {
			fmt.Fprintf(os.Stderr, "error: alias %q has no lockfile; run `import lock` for the first pin\n", alias)
			return strictcli.Exit(1)
		}
		if !update && exists {
			fmt.Fprintf(os.Stderr, "error: alias %q already has a lockfile; run `import update` to re-pin\n", alias)
			return strictcli.Exit(1)
		}

		if err := pinAlias(projectRoot, alias, decl, refByAlias[alias]); err != nil {
			fmt.Fprintf(os.Stderr, "error: import %q: %v\n", alias, err)
			return strictcli.Exit(1)
		}
		verb := "locked"
		if update {
			verb = "updated"
		}
		if !kwargsQuiet(kwargs) {
			fmt.Printf("%s import %q -> %s (%d surface objects)\n", verb, alias, decl.Ref, len(refByAlias[alias]))
		}
	}
	return strictcli.Exit(0)
}

// pinAlias resolves one alias's git pin, extracts and vendors its surface, and
// writes the lockfile. The clone lands in a system temp dir that is removed on
// return — the temp cache is never left behind.
func pinAlias(projectRoot, alias string, decl config.ImportDecl, refTables []string) error {
	tmp, err := os.MkdirTemp("", "pgdesign-import-"+alias+"-")
	if err != nil {
		return fmt.Errorf("creating temp clone dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	cloneDir := filepath.Join(tmp, "repo")
	commit, err := imports.CloneAt(decl.Git, decl.Ref, cloneDir)
	if err != nil {
		return err
	}

	framework, _, exitCode := parseAndBuild(nil, []string{cloneDir})
	if exitCode != 0 {
		return fmt.Errorf("the framework project at %s (%s@%s) failed to parse/build", decl.Git, decl.Ref, commit[:min(12, len(commit))])
	}

	surface, err := imports.ExtractSurface(framework, refTables, decl.Schema)
	if err != nil {
		return err
	}

	entries, surfaceHash, semanticHash, err := imports.Vendor(surface, imports.AliasDir(projectRoot, alias))
	if err != nil {
		return err
	}
	exts, pgv := imports.InferRequirements(framework)

	lf := &imports.Lockfile{
		Alias:        alias,
		URL:          decl.Git,
		Ref:          decl.Ref,
		Commit:       commit,
		Schema:       decl.Schema,
		SurfaceHash:  surfaceHash,
		SemanticHash: semanticHash,
		PGVersion:    pgv,
		Extensions:   exts,
		Objects:      entries,
	}
	return imports.WriteLockfile(projectRoot, lf)
}

// referencedTablesByAlias collects, per import alias, the distinct table names
// the consumer model's foreign keys reference through that alias.
func referencedTablesByAlias(consumer *model.Schema) map[string][]string {
	out := map[string]map[string]bool{}
	for _, t := range consumer.Tables {
		for _, fk := range t.FKs {
			if fk.RefAlias == "" {
				continue
			}
			if out[fk.RefAlias] == nil {
				out[fk.RefAlias] = map[string]bool{}
			}
			out[fk.RefAlias][fk.RefTable] = true
		}
	}
	res := map[string][]string{}
	for alias, set := range out {
		var names []string
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		res[alias] = names
	}
	return res
}

// resolveProjectRootAndConfig discovers the project's pgdesign.toml, returning
// the directory containing it and the loaded raw config.
func resolveProjectRootAndConfig(cfgOverride *string) (string, *config.RawConfig, error) {
	configPath, found, err := resolveConfigPath(cfgOverride, ".")
	if err != nil {
		return "", nil, err
	}
	if !found {
		return "", nil, fmt.Errorf("no pgdesign.toml found (required for imports)")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", nil, err
	}
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return "", nil, err
	}
	return filepath.Dir(absPath), cfg, nil
}
