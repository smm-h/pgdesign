package main

import (
	"fmt"
	"os"
)

func handleCodegen(kwargs map[string]interface{}) int {
	paths := extractPaths(kwargs)
	schema, exitCode := parseAndBuild(paths)
	if exitCode != 0 {
		return exitCode
	}

	lang := kwargs["lang"].(string)
	mode := kwargs["mode"].(string)

	gen, err := SelectGenerator(lang, mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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
