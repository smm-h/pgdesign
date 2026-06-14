package main

import (
	"fmt"
	"os"

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

	var gen codegen.Generator
	switch mode {
	case "validators":
		switch lang {
		case "python":
			gen = &codegen.PythonGenerator{}
		case "zig":
			gen = &codegen.ZigValidatorGenerator{}
		default:
			fmt.Fprintf(os.Stderr, "error: validators mode only supports --lang python or zig, got %s\n", lang)
			return 1
		}
	case "constants":
		switch lang {
		case "python":
			gen = &codegen.PythonConstantsGenerator{}
		case "zig":
			gen = &codegen.ZigConstantsGenerator{}
		default:
			fmt.Fprintf(os.Stderr, "error: unsupported language for constants mode: %s\n", lang)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "error: unsupported mode: %s\n", mode)
		return 1
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
		if !kwargs["quiet"].(bool) {
			fmt.Fprintf(os.Stderr, "Generated %s (%d bytes)\n", outputPath, len(out))
		}
	} else {
		fmt.Print(string(out))
	}

	return 0
}
