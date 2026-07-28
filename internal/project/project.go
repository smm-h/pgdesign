// Package project is the shared project-loading core: it reconciles a parsed
// schema set, the project's pgdesign.toml config, and the vendored import surface
// into ONE resolved model — the semtype registry (builtin + config [[extensions]]
// + user types + imported enums), the vendored import reference tables, and the
// config/toml pg_version tiers folded into schema.PGVersion.
//
// It is the single reconciliation point shared by build, codegen, revise, and
// serve. Before this package, serve had its own loader that used the builtin
// registry only, applied no config extensions, resolved no pg_version, and loaded
// no import surface; routing every consumer through Build removes that drift.
//
// Build takes ALREADY-PARSED raw schemas plus the loaded config and project root,
// so path resolution and TOML parsing stay a CLI-input concern in package main.
// It returns all diagnostics (errors, warnings, info) and never writes to stderr
// or exits — the caller decides how to render and gate.
package project

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/imports"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
)

// Project is a fully resolved project: the built model, the semtype registry that
// produced it (registry-present type information), and the config it was built
// from. ProjectRoot is the directory holding pgdesign.toml (empty when no config
// root was resolved, which also disables import-surface loading).
type Project struct {
	Schema      *model.Schema
	Registry    *semtype.Registry
	Config      *config.RawConfig
	ProjectRoot string
}

// Build reconciles the parsed raw schemas into a resolved Project. It performs, in
// order: registry construction (builtin + config [[extensions]] types), user-type
// loading from every schema, vendored import-surface loading (when projectRoot is
// set and the config declares imports), owned/imported name-collision enforcement,
// the model build, and pg_version tier folding (config over toml). cfg may be nil
// (treated as an empty config); projectRoot "" skips import-surface loading.
//
// On any hard error the returned Project is nil and diags carries the reason.
func Build(raws []*parse.RawSchema, cfg *config.RawConfig, projectRoot string) (*Project, diagnostic.Diagnostics) {
	var diags diagnostic.Diagnostics

	reg := semtype.NewBuiltinRegistry()

	// Register extension-provided types so they pass the base type allowlist.
	if cfg != nil {
		for _, ext := range cfg.Extensions {
			reg.AddExtensionTypes(ext.Types)
		}
	}

	// Load user-defined types from all schemas into the registry.
	for _, raw := range raws {
		userTypes := parse.CollectUserTypes(raw)
		if len(userTypes) > 0 {
			loadDiags := reg.LoadUserTypes(userTypes)
			diags = append(diags, loadDiags...)
			if loadDiags.HasErrors() {
				return nil, diags
			}
		}
	}

	buildOpts := []model.BuildOption{model.WithImports(ImportAliasSchemas(cfg))}

	// Load the vendored import surface (imports/<alias>/) as REFERENCE tables so
	// imported-FK targets resolve through the union (roadmap 7.3). Aliases without
	// a committed lockfile are skipped — the unresolved FK then surfaces as a normal
	// diagnostic rather than being silently satisfied. A corrupt/undecodable
	// vendored surface is a hard error.
	var surface *model.Schema
	if cfg != nil && len(cfg.Imports) > 0 && projectRoot != "" {
		declared := make([]string, 0, len(cfg.Imports))
		for a := range cfg.Imports {
			declared = append(declared, a)
		}
		s, err := imports.LoadAllSurfaces(projectRoot, declared)
		if err != nil {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error,
				Message:  fmt.Sprintf("loading vendored import surface: %v", err),
			})
			return nil, diags
		}
		surface = s
		// Registry enforcement (roadmap 7.3): imported type names must not collide
		// with local ones (hard error naming both), and imported enums are
		// registered so local columns can reference them.
		regDiags := RegisterImportedTypes(reg, surface)
		diags = append(diags, regDiags...)
		if regDiags.HasErrors() {
			return nil, diags
		}
		if len(surface.Tables) > 0 {
			buildOpts = append(buildOpts, model.WithImportedTables(surface.Tables))
		}
	}

	var schema *model.Schema
	var buildDiags diagnostic.Diagnostics
	if len(raws) == 1 {
		schema, buildDiags = model.Build(raws[0], reg, buildOpts...)
	} else {
		schema, buildDiags = model.BuildMulti(raws, reg, buildOpts...)
	}
	diags = append(diags, buildDiags...)
	if buildDiags.HasErrors() {
		return nil, diags
	}
	_ = surface

	// Resolve the config and toml PG-version tiers into schema.PGVersion here, at
	// the shared build entry point. model.Build sets the toml tier ([meta].version);
	// the config tier (pgdesign.toml [database].pg_version) wins over it. The live
	// tier is applied later, only where a database connection is available.
	if cfg != nil && cfg.Database.PGVersion != 0 {
		schema.PGVersion = cfg.Database.PGVersion
	}

	return &Project{Schema: schema, Registry: reg, Config: cfg, ProjectRoot: projectRoot}, diags
}

// ImportAliasSchemas projects the project's [imports] declarations into the
// alias -> target-PG-schema map model.WithImports consumes, so `alias:table` FK
// references resolve at build time (roadmap 7.1). A nil config yields nil.
func ImportAliasSchemas(cfg *config.RawConfig) map[string]string {
	if cfg == nil || len(cfg.Imports) == 0 {
		return nil
	}
	m := make(map[string]string, len(cfg.Imports))
	for alias, d := range cfg.Imports {
		m[alias] = d.Schema
	}
	return m
}

// RegisterImportedTypes enforces registry rules for the vendored import surface
// (roadmap 7.3): an imported type name that collides with a local (non-builtin)
// type is a hard error naming both sources (E243), and imported enums are
// registered so local columns can reference them. Collision detection covers every
// imported type kind; only enums are registered (the roadmap's "imported enums
// usable in local columns" — other imported types are referenced only through FK
// targets).
func RegisterImportedTypes(reg *semtype.Registry, surface *model.Schema) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics
	type named struct{ name, schema string }
	var all []named
	for _, e := range surface.Enums {
		all = append(all, named{e.Name, e.Schema})
	}
	for _, d := range surface.Domains {
		all = append(all, named{d.Name, d.Schema})
	}
	for _, c := range surface.CompositeTypes {
		all = append(all, named{c.Name, c.Schema})
	}
	for _, sm := range surface.StateMachines {
		all = append(all, named{sm.Name, sm.Schema})
	}
	for _, n := range all {
		if existing, err := reg.Resolve(n.name); err == nil && existing.Source != "builtin" {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error, Code: "E243",
				Message: fmt.Sprintf("imported type %q (from schema %q) collides with a local type of the same name; both cannot own %q — rename one", n.name, n.schema, n.name),
			})
		}
	}
	if diags.HasErrors() {
		return diags
	}
	var defs []semtype.UserTypeDef
	for _, e := range surface.Enums {
		defs = append(defs, semtype.UserTypeDef{Name: e.Name, Kind: "enum", Values: e.Values, Comment: e.Comment})
	}
	if len(defs) > 0 {
		diags = append(diags, reg.LoadUserTypes(defs)...)
	}
	return diags
}
