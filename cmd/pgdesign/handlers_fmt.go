package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smm-h/pgdesign/internal/format"
)

func handleFmt(kwargs map[string]interface{}) int {
	target := kwargs["path"].(string)

	// Load config for format defaults.
	cfg := loadProjectConfig(target)

	// CLI flags override config; config overrides strictcli defaults.
	tableOrder := kwargs["table_order"].(string)
	if tableOrder == "dependency" && cfg.Format.TableOrder != "" {
		tableOrder = cfg.Format.TableOrder
	}
	columnOrder := kwargs["column_order"].(string)
	if columnOrder == "pk_fk_alpha" && cfg.Format.ColumnOrder != "" {
		columnOrder = cfg.Format.ColumnOrder
	}

	fmtConfig := &format.Config{
		TableOrder:  tableOrder,
		ColumnOrder: columnOrder,
	}

	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot stat %q: %v\n", target, err)
		return 1
	}

	if info.IsDir() {
		return fmtDir(target, fmtConfig, kwargs["check"].(bool))
	}
	return fmtFile(target, fmtConfig, kwargs["check"].(bool))
}

func fmtFile(filePath string, cfg *format.Config, checkOnly bool) int {
	input, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read file: %v\n", err)
		return 1
	}

	formatted, err := format.Format(input, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if checkOnly {
		if bytes.Equal(input, formatted) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "%s: not formatted\n", filePath)
		return 1
	}

	if err := os.WriteFile(filePath, formatted, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot write file: %v\n", err)
		return 1
	}
	return 0
}

func fmtDir(dirPath string, cfg *format.Config, checkOnly bool) int {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read directory: %v\n", err)
		return 1
	}

	exitCode := 0
	found := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".toml") || name == "pgdesign.toml" {
			continue
		}
		found = true
		code := fmtFile(filepath.Join(dirPath, name), cfg, checkOnly)
		if code != 0 {
			exitCode = code
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "error: no .toml schema files found in %q\n", dirPath)
		return 1
	}
	return exitCode
}
