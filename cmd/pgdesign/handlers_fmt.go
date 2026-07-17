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

func registerFmtCmd(app *strictcli.App) {
	app.Command("fmt", "Format a pgdesign TOML schema file or directory in place",
		func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
			target := kwargs["path"].(string)

			cfg, cfgErr := loadProjectConfig(kwargsConfigOverride(kwargs), target)
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", cfgErr)
				return strictcli.Exit(1)
			}

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

			check := kwargs["check"].(bool)

			info, err := os.Stat(target)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: cannot stat %q: %v\n", target, err)
				return strictcli.Exit(1)
			}

			if info.IsDir() {
				return strictcli.Exit(fmtDir(target, fmtConfig, check))
			}
			return strictcli.Exit(fmtFile(target, fmtConfig, check))
		},
		strictcli.WithFlags(
			strictcli.BoolFlag("check", "Check if file is already formatted (exit 1 if not)", strictcli.Default(false)),
			strictcli.StringFlag("table-order", "Table ordering strategy: dependency-based or alphabetical", strictcli.Default("dependency"), strictcli.Choices("dependency", "alphabetical")),
			strictcli.StringFlag("column-order", "Column ordering: pk_fk_alpha, alphabetical, fk_last, or preserve", strictcli.Default("pk_fk_alpha"), strictcli.Choices("pk_fk_alpha", "alphabetical", "fk_last", "preserve")),
		),
		strictcli.WithArgs(
			strictcli.NewArg("path", "Path to the TOML schema file or directory to format"),
		),
	)
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
