package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/smm-h/pgdesign/internal/codegen"
)

func handleCodegen(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	lang := kwargs["lang"].(string)
	mode := kwargs["mode"].(string)
	quiet := kwargs["quiet"].(bool)

	gen, err := SelectGenerator(lang, mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// MultiFileGenerator: write files into output directory.
	if mfg, ok := gen.(codegen.MultiFileGenerator); ok {
		files, diags := mfg.GenerateFiles(schema)
		for _, d := range diags {
			fmt.Fprintf(os.Stderr, "%s: %s\n", d.Severity, d.Message)
		}
		outputPath, _ := kwargs["output"].(string)
		if outputPath == "" {
			// Without -o, print each file to stdout with a header.
			for relPath, data := range files {
				fmt.Printf("==> %s <==\n%s\n", relPath, data)
			}
			return 0
		}
		if err := os.MkdirAll(outputPath, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot create output directory: %v\n", err)
			return 1
		}
		for relPath, data := range files {
			fp := filepath.Join(outputPath, relPath)
			if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			if err := os.WriteFile(fp, data, 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "error: cannot write output file: %v\n", err)
				return 1
			}
			if !quiet {
				fmt.Fprintf(os.Stderr, "Generated %s (%d bytes)\n", fp, len(data))
			}
		}
		return 0
	}

	out, diags := gen.Generate(schema)
	for _, d := range diags {
		fmt.Fprintf(os.Stderr, "%s: %s\n", d.Severity, d.Message)
	}

	if outputPath, ok := kwargs["output"].(string); ok && outputPath != "" {
		if err := os.WriteFile(outputPath, out, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot write output file: %v\n", err)
			return 1
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "Generated %s (%d bytes)\n", outputPath, len(out))
		}
	} else {
		fmt.Print(string(out))
	}

	return 0
}
