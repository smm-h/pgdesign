package main

import (
	"fmt"
	"sort"

	"github.com/smm-h/pgdesign/internal/codegen"
	"github.com/smm-h/pgdesign/pkg/genkit"
	"github.com/smm-h/strictcli/go/strictcli"
)

// codegenMode is one row of the codegen mode registry: the mode's name, the
// one-line description the CLI publishes for it, and the languages it supports.
//
// The single table below is the one authority. The mode->languages map, the
// sorted name list and the `--mode` choices records are all derived from it, so
// a mode cannot exist in the CLI's vocabulary without a description, or be
// described without being generatable.
type codegenMode struct {
	name  string
	help  string
	langs []string
}

var allLangs = []string{"go", "java", "kotlin", "python", "ts", "zig"}

var codegenModeRegistry = []codegenMode{
	{"constants", "table and column name string constants", allLangs},
	{"constraints", "client-side validators derived from CHECK, NOT NULL and enum constraints", allLangs},
	{"ddl", "DDL definition tuples plus a section executor that applies them", []string{"python"}},
	{"drizzle", "a Drizzle ORM schema module", []string{"ts"}},
	{"enums", "standalone enum definitions for the schema's enum types", allLangs},
	{"gorm", "GORM struct tags for the schema's tables", []string{"go"}},
	{"jpa", "JPA entity classes for the schema's tables", []string{"java"}},
	{"query-layer", "Protocol definitions with an asyncpg backend and an in-memory backend", []string{"python"}},
	{"sqlalchemy", "SQLAlchemy 2.0 declarative models", []string{"python"}},
	{"types", "native struct, class or interface definitions with their enums", allLangs},
	{"validators", "row-level-security policy checkers", allLangs},
}

// SupportedModes returns a map of codegen mode to the languages it supports.
func SupportedModes() map[string][]string {
	modes := make(map[string][]string, len(codegenModeRegistry))
	for _, m := range codegenModeRegistry {
		modes[m.name] = m.langs
	}
	return modes
}

// SupportedModeNames returns a sorted list of valid codegen mode names.
func SupportedModeNames() []string {
	names := make([]string, 0, len(codegenModeRegistry))
	for _, m := range codegenModeRegistry {
		names = append(names, m.name)
	}
	sort.Strings(names)
	return names
}

// SupportedModeChoices returns the `--mode` choices records, in the sorted order
// SupportedModeNames publishes, each carrying its registry description.
func SupportedModeChoices() []strictcli.ChoiceValue {
	byName := make(map[string]string, len(codegenModeRegistry))
	for _, m := range codegenModeRegistry {
		byName[m.name] = m.help
	}
	names := SupportedModeNames()
	choices := make([]strictcli.ChoiceValue, 0, len(names))
	for _, n := range names {
		choices = append(choices, strictcli.Ch(n, byName[n]))
	}
	return choices
}

// SelectGenerator returns the genkit.Generator for the given language and mode.
// It returns a descriptive error if the combination is unsupported.
func SelectGenerator(lang, mode string) (genkit.Generator, error) {
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
