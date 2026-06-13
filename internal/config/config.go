// Package config loads pgdesign.toml project configuration files.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	tomledit "github.com/smm-h/go-toml-edit"
)

// Config represents the parsed pgdesign.toml project configuration.
type Config struct {
	Project    ProjectConfig    `toml:"project"`
	Database   DatabaseConfig   `toml:"database"`
	Format     FormatConfig     `toml:"format"`
	Validate   ValidateConfig   `toml:"validate"`
	Migrate    MigrateConfig    `toml:"migrate"`
	Extensions []ExtensionConfig `toml:"extensions"`
	Suppress   map[string]string `toml:"suppress"`
}

// ProjectConfig holds [project] section values.
type ProjectConfig struct {
	Schemas       []string `toml:"schemas"`
	MigrationsDir string   `toml:"migrations_dir"`
}

// DatabaseConfig holds [database] section values.
type DatabaseConfig struct {
	PGVersion int `toml:"pg_version"`
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

// ExtensionConfig holds [[extensions]] array-of-tables entries.
type ExtensionConfig struct {
	Name         string   `toml:"name"`
	Types        []string `toml:"types"`
	Opclasses    []string `toml:"opclasses"`
	Functions    []string `toml:"functions"`
	IndexMethods []string `toml:"index_methods"`
}

// Load reads and parses a pgdesign.toml file at the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config: %w", err)
	}

	var cfg Config
	if err := tomledit.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", err)
	}

	return &cfg, nil
}

// LoadOrDefault attempts to load pgdesign.toml from dir. If the file does not
// exist, it returns a zero-valued Config (all defaults). Other errors are returned.
func LoadOrDefault(dir string) (*Config, error) {
	path, found := FindConfig(dir)
	if !found {
		return &Config{}, nil
	}
	return Load(path)
}

// MergeValidateFlags merges CLI flag values into the validate config.
// Non-zero flag values override config file values.
func (c *Config) MergeValidateFlags(namingPattern string, maxColumns int) {
	if namingPattern != "" {
		c.Validate.NamingPattern = namingPattern
	}
	if maxColumns != 0 {
		c.Validate.MaxColumns = maxColumns
	}
}

// SchemaFiles returns the absolute paths of all schema files listed in the
// config, resolved relative to the directory containing pgdesign.toml.
func (c *Config) SchemaFiles(configDir string) []string {
	paths := make([]string, len(c.Project.Schemas))
	for i, s := range c.Project.Schemas {
		if filepath.IsAbs(s) {
			paths[i] = s
		} else {
			paths[i] = filepath.Join(configDir, s)
		}
	}
	return paths
}

// FindConfig looks for pgdesign.toml in the given directory.
// Returns the path and true if found, or empty string and false if not.
func FindConfig(dir string) (string, bool) {
	path := filepath.Join(dir, "pgdesign.toml")
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	return "", false
}
