package main

import (
	"fmt"
	"sort"

	"github.com/smm-h/pgdesign/internal/codegen"
)

// SupportedModes returns a map of codegen mode to the languages it supports.
func SupportedModes() map[string][]string {
	return map[string][]string{
		"validators":   {"go", "java", "kotlin", "python", "ts", "zig"},
		"constants":    {"go", "java", "kotlin", "python", "ts", "zig"},
		"types":        {"go", "java", "kotlin", "python", "ts", "zig"},
		"constraints":  {"go", "java", "kotlin", "python", "ts", "zig"},
		"gorm":         {"go"},
		"drizzle":      {"ts"},
		"sqlalchemy":   {"python"},
		"jpa":          {"java"},
		"ddl":          {"python"},
		"query-layer":  {"python"},
		"enums":         {"go", "java", "kotlin", "python", "ts", "zig"},
	}
}

// SupportedModeNames returns a sorted list of valid codegen mode names.
func SupportedModeNames() []string {
	modes := SupportedModes()
	names := make([]string, 0, len(modes))
	for m := range modes {
		names = append(names, m)
	}
	sort.Strings(names)
	return names
}

// SelectGenerator returns the codegen.Generator for the given language and mode.
// It returns a descriptive error if the combination is unsupported.
func SelectGenerator(lang, mode string) (codegen.Generator, error) {
	switch mode {
	case "validators":
		switch lang {
		case "python":
			return &codegen.PythonGenerator{}, nil
		case "zig":
			return &codegen.ZigValidatorGenerator{}, nil
		case "go":
			return &codegen.GoValidatorGenerator{}, nil
		case "ts":
			return &codegen.TSValidatorGenerator{}, nil
		case "java":
			return &codegen.JavaValidatorGenerator{}, nil
		case "kotlin":
			return &codegen.KotlinValidatorGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s", mode, lang)
		}
	case "constants":
		switch lang {
		case "python":
			return &codegen.PythonConstantsGenerator{}, nil
		case "zig":
			return &codegen.ZigConstantsGenerator{}, nil
		case "go":
			return &codegen.GoConstantsGenerator{}, nil
		case "ts":
			return &codegen.TSConstantsGenerator{}, nil
		case "java":
			return &codegen.JavaConstantsGenerator{}, nil
		case "kotlin":
			return &codegen.KotlinConstantsGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s", mode, lang)
		}
	case "types":
		switch lang {
		case "go":
			return &codegen.GoTypesGenerator{}, nil
		case "ts":
			return &codegen.TSTypesGenerator{}, nil
		case "python":
			return &codegen.PythonTypesGenerator{}, nil
		case "java":
			return &codegen.JavaTypesGenerator{}, nil
		case "kotlin":
			return &codegen.KotlinTypesGenerator{}, nil
		case "zig":
			return &codegen.ZigTypesGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s", mode, lang)
		}
	case "constraints":
		switch lang {
		case "go":
			return &codegen.GoConstraintsGenerator{}, nil
		case "java":
			return &codegen.JavaConstraintsGenerator{}, nil
		case "kotlin":
			return &codegen.KotlinConstraintsGenerator{}, nil
		case "python":
			return &codegen.PythonConstraintsGenerator{}, nil
		case "ts":
			return &codegen.TSConstraintsGenerator{}, nil
		case "zig":
			return &codegen.ZigConstraintsGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s (supported: go, java, kotlin, python, ts, zig)", mode, lang)
		}
	case "gorm":
		switch lang {
		case "go":
			return &codegen.GoGormGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s (supported: go)", mode, lang)
		}
	case "drizzle":
		switch lang {
		case "ts":
			return &codegen.TSDrizzleGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s (supported: ts)", mode, lang)
		}
	case "sqlalchemy":
		switch lang {
		case "python":
			return &codegen.PythonSQLAlchemyGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s (supported: python)", mode, lang)
		}
	case "jpa":
		switch lang {
		case "java":
			return &codegen.JavaJPAGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s (supported: java)", mode, lang)
		}
	case "ddl":
		switch lang {
		case "python":
			return &codegen.PythonDDLGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s (supported: python)", mode, lang)
		}
	case "query-layer":
		switch lang {
		case "python":
			return &codegen.PythonQueryLayerGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s (supported: python)", mode, lang)
		}
	case "enums":
		switch lang {
		case "go", "java", "kotlin", "python", "ts", "zig":
			return &codegen.EnumsGenerator{Lang: codegen.Lang(lang)}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s (supported: go, java, kotlin, python, ts, zig)", mode, lang)
		}
	default:
		return nil, fmt.Errorf("unsupported codegen mode: %s", mode)
	}
}
