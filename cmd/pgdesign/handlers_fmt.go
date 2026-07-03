package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smm-h/pgdesign/internal/format"
	"github.com/smm-h/strictcli/go/strictcli"
)

type fmtHandler struct {
	Path        string `arg:"path" help:"Path to the TOML schema file or directory to format"`
	Check       bool   `cli:"check" help:"Check if file is already formatted (exit 1 if not)" default:"false"`
	TableOrder  string `cli:"table-order" help:"Table ordering strategy: dependency-based or alphabetical" default:"dependency" choices:"dependency,alphabetical"`
	ColumnOrder string `cli:"column-order" help:"Column ordering: pk_fk_alpha, alphabetical, fk_last, or preserve" default:"pk_fk_alpha" choices:"pk_fk_alpha,alphabetical,fk_last,preserve"`
}

func (h *fmtHandler) Run(ctx *strictcli.Context) int {
	target := h.Path

	// Load config for format defaults.
	cfg, cfgErr := loadProjectConfig(configOverride(ctx), target)
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
		return 1
	}

	// CLI flags override config; config overrides strictcli defaults.
	tableOrder := h.TableOrder
	if tableOrder == "dependency" && cfg.Format.TableOrder != "" {
		tableOrder = cfg.Format.TableOrder
	}
	columnOrder := h.ColumnOrder
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
		return fmtDir(target, fmtConfig, h.Check)
	}
	return fmtFile(target, fmtConfig, h.Check)
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
