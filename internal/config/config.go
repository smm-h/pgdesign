// Package config loads and validates pgdesign.toml project configuration files including output targets, suppression rules, and extension declarations.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tomledit "github.com/smm-h/go-toml-edit"
)

// RelativePath represents a path as declared in the TOML config file (relative to project root).
type RelativePath string

// AbsolutePath represents a fully resolved absolute filesystem path.
type AbsolutePath string

// PathKind constrains the Config generic to either relative or absolute paths.
type PathKind interface {
	RelativePath | AbsolutePath
}

// Config represents the parsed pgdesign.toml project configuration.
type Config[P PathKind] struct {
	Project    ProjectConfig[P]           `toml:"project"`
	Database   DatabaseConfig             `toml:"database"`
	Format     FormatConfig               `toml:"format"`
	Validate   ValidateConfig             `toml:"validate"`
	Migrate    MigrateConfig              `toml:"migrate"`
	Extensions []ExtensionConfig          `toml:"extensions"`
	Suppress   map[string]string          `toml:"suppress"`
	Output     map[string]OutputConfig[P] `toml:"-"`

	// SourcePath is the absolute path to the pgdesign.toml file that was loaded.
	// Not part of the TOML schema; set by LoadAndResolve.
	SourcePath string `toml:"-"`
}

// RawConfig is a Config with relative paths, as parsed directly from TOML.
type RawConfig = Config[RelativePath]

// ResolvedConfig is a Config with all paths resolved to absolute paths.
type ResolvedConfig = Config[AbsolutePath]

// ProjectConfig holds [project] section values.
type ProjectConfig[P PathKind] struct {
	Schemas       []P `toml:"schemas"`
	MigrationsDir P   `toml:"migrations_dir"`
}

// DatabaseConfig holds [database] section values.
type DatabaseConfig struct {
	URL          string `toml:"url"`
	PGVersion    int    `toml:"pg_version"`
	PoolMaxConns int32  `toml:"pool_max_conns"`
	PoolMinConns int32  `toml:"pool_min_conns"`
}

// FormatConfig holds [format] section values.
type FormatConfig struct {
	TableOrder  string `toml:"table_order"`
	ColumnOrder string `toml:"column_order"`
}

// ValidateConfig holds [validate] section values.
type ValidateConfig struct {
	Disable       []string `toml:"disable"`        // error codes OR rule names
	NamingPattern string   `toml:"naming_pattern"`
	MaxColumns    int      `toml:"max_columns"`
}

// MigrateConfig holds [migrate] section values.
type MigrateConfig struct {
	LockTimeout             string `toml:"lock_timeout"`
	AutoConcurrentThreshold int64  `toml:"auto_concurrent_threshold"`
	ExpandContractThreshold int64  `toml:"expand_contract_threshold"`
}

// OutputConfig holds an [output.<name>] section describing a build output target.
type OutputConfig[P PathKind] struct {
	Format     string   `toml:"format"`     // sql, d2, json, svg, doc, graphql, codegen
	Path       P        `toml:"path"`       // relative to project root (RawConfig) or absolute (ResolvedConfig)
	Lang       string   `toml:"lang"`       // for codegen: python, zig, go, ts, java, kotlin
	Mode       string   `toml:"mode"`       // for codegen: validators, constants
	Groups     []string `toml:"groups"`     // restrict output to tables in these groups
	Backends   []string `toml:"backends"`   // for query-layer: ["pg"], ["memory"], or both (default: both)
	Idempotent bool     `toml:"idempotent"` // for sql: add IF NOT EXISTS
	Comments   *bool    `toml:"comments"`   // for sql: include COMMENT ON (default true)
	SplitMode  string   `toml:"split_mode"` // for codegen ddl python: "faceted" or "self-contained"
}

// ExtensionConfig holds [[extensions]] array-of-tables entries.
type ExtensionConfig struct {
	Name         string   `toml:"name"`
	Types        []string `toml:"types"`
	Opclasses    []string `toml:"opclasses"`
	Functions    []string `toml:"functions"`
	IndexMethods []string `toml:"index_methods"`
}

// CodegenModes maps codegen mode names to the languages each mode supports.
// Set by the main package to enable mode and lang-mode pair validation.
// When nil, only hardcoded mode names are checked.
var CodegenModes map[string][]string

// Check checks the Config for semantic errors that TOML parsing alone
// cannot catch (e.g., cross-field constraints).
func (c *Config[P]) Check() error {
	var errs []error
	if c.Database.PoolMaxConns < 0 {
		errs = append(errs, fmt.Errorf("pool_max_conns must be non-negative"))
	}
	if c.Database.PoolMinConns < 0 {
		errs = append(errs, fmt.Errorf("pool_min_conns must be non-negative"))
	}
	if c.Database.PoolMinConns > 0 && c.Database.PoolMaxConns > 0 && c.Database.PoolMinConns > c.Database.PoolMaxConns {
		errs = append(errs, fmt.Errorf("pool_min_conns cannot exceed pool_max_conns"))
	}

	validFormats := map[string]bool{
		"sql": true, "d2": true, "json": true, "svg": true, "doc": true, "graphql": true, "codegen": true,
	}
	validLangs := map[string]bool{
		"python": true, "zig": true, "go": true, "ts": true, "java": true, "kotlin": true,
	}
	// Build validModes from CodegenModes if available, otherwise hardcoded fallback.
	validModes := map[string]bool{}
	if CodegenModes != nil {
		for mode := range CodegenModes {
			validModes[mode] = true
		}
	} else {
		for _, m := range []string{"validators", "constants", "types", "constraints", "gorm", "drizzle", "sqlalchemy", "jpa"} {
			validModes[m] = true
		}
	}
	for name, out := range c.Output {
		if out.Path == P("") {
			errs = append(errs, fmt.Errorf("output.%s: path is required", name))
		}
		if !validFormats[out.Format] {
			errs = append(errs, fmt.Errorf("output.%s: invalid format %q (must be one of: sql, d2, json, svg, doc, graphql, codegen)", name, out.Format))
		}
		if out.Format == "codegen" {
			if out.Lang == "" {
				errs = append(errs, fmt.Errorf("output.%s: lang is required when format is codegen", name))
			}
			if out.Mode == "" {
				errs = append(errs, fmt.Errorf("output.%s: mode is required when format is codegen", name))
			}
		}
		if out.Lang != "" && !validLangs[out.Lang] {
			errs = append(errs, fmt.Errorf("output.%s: invalid lang %q (must be one of: python, zig, go, ts, java, kotlin)", name, out.Lang))
		}
		if out.Mode != "" && !validModes[out.Mode] {
			modeNames := make([]string, 0, len(validModes))
			for m := range validModes {
				modeNames = append(modeNames, m)
			}
			sort.Strings(modeNames)
			errs = append(errs, fmt.Errorf("output.%s: invalid mode %q (must be one of: %s)", name, out.Mode, strings.Join(modeNames, ", ")))
		}
		// Validate lang-mode pair when CodegenModes is available.
		if CodegenModes != nil && out.Mode != "" && out.Lang != "" && validModes[out.Mode] && validLangs[out.Lang] {
			supportedLangs := CodegenModes[out.Mode]
			found := false
			for _, l := range supportedLangs {
				if l == out.Lang {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, fmt.Errorf("output.%s: language %q is not supported for mode %q (supported: %s)", name, out.Lang, out.Mode, strings.Join(supportedLangs, ", ")))
			}
		}
		if out.SplitMode != "" {
			validSplitModes := map[string]bool{"faceted": true, "self-contained": true}
			if !validSplitModes[out.SplitMode] {
				errs = append(errs, fmt.Errorf("output.%s: invalid split_mode %q (must be one of: faceted, self-contained)", name, out.SplitMode))
			} else if out.Format != "codegen" || out.Mode != "ddl" || out.Lang != "python" {
				errs = append(errs, fmt.Errorf("output.%s: split_mode is only supported for codegen mode=ddl lang=python", name))
			}
		}
	}

	return errors.Join(errs...)
}

// decodeOutput converts a raw map[string]any (from TOML decoding) into a typed
// map[string]OutputConfig[RelativePath]. Each value is expected to be a map[string]any
// representing the fields of an OutputConfig.
func decodeOutput(raw map[string]any) (map[string]OutputConfig[RelativePath], error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]OutputConfig[RelativePath], len(raw))
	for name, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("output.%s: expected table, got %T", name, v)
		}
		var oc OutputConfig[RelativePath]
		if s, ok := m["format"].(string); ok {
			oc.Format = s
		}
		if s, ok := m["path"].(string); ok {
			oc.Path = RelativePath(s)
		}
		if s, ok := m["lang"].(string); ok {
			oc.Lang = s
		}
		if s, ok := m["mode"].(string); ok {
			oc.Mode = s
		}
		if arr, ok := m["groups"].([]any); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					oc.Groups = append(oc.Groups, s)
				}
			}
		}
		if arr, ok := m["backends"].([]any); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					oc.Backends = append(oc.Backends, s)
				}
			}
		}
		if b, ok := m["idempotent"].(bool); ok {
			oc.Idempotent = b
		}
		if b, ok := m["comments"].(bool); ok {
			oc.Comments = &b
		}
		if s, ok := m["split_mode"].(string); ok {
			oc.SplitMode = s
		}
		out[name] = oc
	}
	return out, nil
}

// Load reads and parses a pgdesign.toml file at the given path.
func Load(path string) (*RawConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config: %w", err)
	}
	return LoadBytes(data)
}

// LoadBytes parses config from in-memory bytes.
func LoadBytes(data []byte) (*RawConfig, error) {
	var cfg RawConfig
	if err := tomledit.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", err)
	}

	// go-toml-edit cannot decode map[string]Struct from nested table syntax
	// ([output.<name>]) into a struct field. Work around this by doing a
	// second decode into map[string]any and extracting the output section.
	var raw map[string]any
	if err := tomledit.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", err)
	}
	if rawOutput, ok := raw["output"]; ok {
		if outputMap, ok := rawOutput.(map[string]any); ok {
			output, err := decodeOutput(outputMap)
			if err != nil {
				return nil, fmt.Errorf("cannot parse config: %w", err)
			}
			cfg.Output = output
		}
	}

	if err := cfg.Check(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// LoadOrDefault attempts to load pgdesign.toml from dir. If the file does not
// exist, it returns a zero-valued Config (all defaults). Other errors are returned.
func LoadOrDefault(dir string) (*RawConfig, error) {
	path, found := FindConfig(dir)
	if !found {
		return &RawConfig{}, nil
	}
	return Load(path)
}

// LoadAndResolve loads pgdesign.toml at the given path and resolves all relative
// paths to absolute paths using the directory containing the config file as the
// project root.
func LoadAndResolve(path string) (*ResolvedConfig, error) {
	raw, err := Load(path)
	if err != nil {
		return nil, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve config path: %w", err)
	}
	resolved, err := Resolve(raw, filepath.Dir(absPath))
	if err != nil {
		return nil, err
	}
	resolved.SourcePath = absPath
	return resolved, nil
}

// Resolve converts a RawConfig (relative paths) into a ResolvedConfig (absolute paths)
// by resolving each path relative to projectRoot.
func Resolve(raw *RawConfig, projectRoot string) (*ResolvedConfig, error) {
	resolved := &ResolvedConfig{
		Database:   raw.Database,
		Format:     raw.Format,
		Validate:   raw.Validate,
		Migrate:    raw.Migrate,
		Extensions: raw.Extensions,
		Suppress:   raw.Suppress,
	}

	// Resolve Project.Schemas
	resolved.Project.Schemas = make([]AbsolutePath, len(raw.Project.Schemas))
	for i, s := range raw.Project.Schemas {
		resolved.Project.Schemas[i] = resolvePath(projectRoot, s)
	}

	// Resolve Project.MigrationsDir
	resolved.Project.MigrationsDir = resolvePath(projectRoot, raw.Project.MigrationsDir)

	// Resolve Output map
	if len(raw.Output) > 0 {
		resolved.Output = make(map[string]OutputConfig[AbsolutePath], len(raw.Output))
		for name, out := range raw.Output {
			resolved.Output[name] = OutputConfig[AbsolutePath]{
				Format:     out.Format,
				Path:       resolvePath(projectRoot, out.Path),
				Lang:       out.Lang,
				Mode:       out.Mode,
				Groups:     out.Groups,
				Backends:   out.Backends,
				Idempotent: out.Idempotent,
				Comments:   out.Comments,
				SplitMode:  out.SplitMode,
			}
		}
	}

	return resolved, nil
}

// resolvePath resolves a RelativePath to an AbsolutePath relative to projectRoot.
// If the path is empty, returns an empty AbsolutePath.
// If the path is already absolute, returns it as-is.
func resolvePath(projectRoot string, p RelativePath) AbsolutePath {
	s := string(p)
	if s == "" {
		return AbsolutePath("")
	}
	if filepath.IsAbs(s) {
		return AbsolutePath(s)
	}
	return AbsolutePath(filepath.Join(projectRoot, s))
}

// MergeValidateFlags merges CLI flag values into the validate config.
// Non-zero flag values override config file values.
func (c *Config[P]) MergeValidateFlags(namingPattern string, maxColumns int) {
	if namingPattern != "" {
		c.Validate.NamingPattern = namingPattern
	}
	if maxColumns != 0 {
		c.Validate.MaxColumns = maxColumns
	}
}

// SchemaFiles returns the resolved schema file paths as strings.
// On a ResolvedConfig, the paths are already absolute.
// On a RawConfig, the paths are returned as-is (relative).
func (c *Config[P]) SchemaFiles() []string {
	paths := make([]string, len(c.Project.Schemas))
	for i, s := range c.Project.Schemas {
		paths[i] = string(s)
	}
	return paths
}

// FindConfig looks for pgdesign.toml by walking from the given directory toward
// the filesystem root. Returns the path and true if found, or empty string and
// false if not found in any ancestor.
func FindConfig(dir string) (string, bool) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		path := filepath.Join(absDir, "pgdesign.toml")
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
		parent := filepath.Dir(absDir)
		if parent == absDir {
			// Reached filesystem root.
			return "", false
		}
		absDir = parent
	}
}
