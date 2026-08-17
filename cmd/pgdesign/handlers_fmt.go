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

			// Precedence is flag > pgdesign.toml [format] > the fallback each
			// flag's help names. The flags declare Optional() rather than a
			// default (fmt is mutating; contract §27.1 forbids a value default),
			// which also removes the sentinel this used to read: the old code
			// compared the resolved value against the default string, so an
			// explicit `--table-order dependency` was indistinguishable from an
			// absent flag and was overridden by the config.
			fmtConfig := &format.Config{
				TableOrder:  optStr(kwargs["table_order"], firstNonEmpty(cfg.Format.TableOrder, "dependency")),
				ColumnOrder: optStr(kwargs["column_order"], firstNonEmpty(cfg.Format.ColumnOrder, "pk_fk_alpha")),
			}

			check := optBool(kwargs["check"], false)

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
		strictcli.WithEffect(strictcli.EffectMutating),
		strictcli.WithFlags(
			strictcli.BoolFlag("check", "Check if file is already formatted (exit 1 if not); omitted means the file is rewritten in place", strictcli.Optional()),
			strictcli.StringFlag("table-order", "Table ordering strategy; omitted means [format].table_order from pgdesign.toml, else dependency", strictcli.Optional(), strictcli.Choices(
				strictcli.Ch("dependency", "order tables so a table follows the tables it depends on"),
				strictcli.Ch("alphabetical", "order tables by name"),
			)),
			strictcli.StringFlag("column-order", "Column ordering; omitted means [format].column_order from pgdesign.toml, else pk_fk_alpha", strictcli.Optional(), strictcli.Choices(
				strictcli.Ch("pk_fk_alpha", "primary key first, then foreign keys, then the rest alphabetically"),
				strictcli.Ch("alphabetical", "order every column by name"),
				strictcli.Ch("fk_last", "order columns alphabetically with the foreign keys moved to the end"),
				strictcli.Ch("preserve", "leave the declared column order untouched"),
			)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("path", "Path to the TOML schema file or directory to format", strictcli.ArgRequired()),
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
	// fmt is a SOURCE-EDITING writer (roadmap 6.2): a rewrite that actually
	// changed the file changed the schema source, hence the project revision, so
	// every derived output is now stale. Print the follow-up notice; the revision
	// check catches the staleness.
	if !bytes.Equal(input, formatted) {
		fmt.Fprintf(os.Stderr, "%s: %s\n", filePath, fmtRevisionNotice)
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
