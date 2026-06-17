package main

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/codegen"
)

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
		case "ts":
			return &codegen.TSConstraintsGenerator{}, nil
		default:
			return nil, fmt.Errorf("unsupported language for %s mode: %s (supported: go, ts)", mode, lang)
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
	default:
		return nil, fmt.Errorf("unsupported codegen mode: %s", mode)
	}
}
