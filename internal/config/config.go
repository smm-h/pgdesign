// Package config loads pgdesign.toml project configuration files.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config represents the parsed pgdesign.toml project configuration.
type Config struct {
	Project  ProjectConfig  `toml:"project"`
	Database DatabaseConfig `toml:"database"`
	Format   FormatConfig   `toml:"format"`
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

// Load reads and parses a pgdesign.toml file at the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", err)
	}

	return &cfg, nil
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
