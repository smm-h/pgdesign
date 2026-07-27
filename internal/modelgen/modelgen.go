// Package modelgen is a pure random generator of VALID pgdesign models,
// built on pgregory.net/rapid so shrinking is structural and comes for free
// with the combinators. It is L9's input source: the property tests that check
// the kernel's laws consume generated models rather than a handful of hand-built
// fixtures (the seed package generates row DATA and cannot serve this role).
//
// This is increment A of the staged generator (roadmap 1.6): flat models only —
// multiple schemas, tables with snake_case names, typed builtin columns drawn
// from the real semtype builtin registry, mandatory table comments, and a
// surrogate primary key per table. It deliberately generates NO foreign keys,
// NO custom types, NO views or functions, and NO expressions; those fragments
// arrive in later increments alongside the consumers that need them.
//
// Oracle doctrine: well-formedness invariants (snake_case names, comments,
// resolvable types, a valid PK) are constructed by design; broader policy
// invariants are left to generate-then-validate-reject so their distributions
// stay wide. The oracle itself (modelgen_test.go) is validate — generated
// models must Build + Canonicalize cleanly AND pass validate with zero errors,
// with the extension and type registries populated.
//
// modelgen is test-support infrastructure: it is imported only by property
// tests, never by the production CLI, which keeps the rapid dependency out of
// shipped binaries.
package modelgen

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"pgregory.net/rapid"
)

// Config controls the shape of generated models. Increment A covers flat
// tables; every field is additive so later increments (FKs, type closures,
// state machines, injective / bridge-proven fragments) extend this struct
// rather than reshape it.
type Config struct {
	// MinSchemas and MaxSchemas bound the number of schemas per model
	// (inclusive). A model with more than one schema is a multi-schema layout.
	MinSchemas, MaxSchemas int

	// MinTables and MaxTables bound the number of tables per schema (inclusive).
	MinTables, MaxTables int

	// MinExtraColumns and MaxExtraColumns bound the number of columns per table
	// BEYOND the mandatory surrogate primary-key column (inclusive).
	MinExtraColumns, MaxExtraColumns int

	// PGVersion is written into every generated schema's [meta].version. It
	// gates version-conditional features in later increments; increment A
	// generates no version-gated features, so any recent version is safe.
	PGVersion int

	// ColumnTypes is the pool of semantic type names that non-PK columns are
	// drawn from. When empty, DefaultConfig / withDefaults fills it from the
	// real builtin registry (minus the identity/surrogate types reserved for
	// the primary key), so the pool tracks the registry instead of a hardcoded
	// copy that could drift.
	ColumnTypes []string

	// MinGroups and MaxGroups bound the number of [groups] entries per model
	// (inclusive). Each group references a random non-empty subset of the
	// model's tables. Groups are schema-global identity (they participate in the
	// revision and, since the reverse-conformance work, in diff); generating
	// them lets identity/diff property tests exercise the group collection
	// rather than always seeing it empty. Zero groups is a valid model, so the
	// range is NOT defaulted away — DefaultConfig leaves it 0..2 explicitly.
	MinGroups, MaxGroups int
}

// pkColumnName is the fixed name of the surrogate primary-key column every
// generated table carries.
const pkColumnName = "id"

// pkColumnType is the semantic type of the surrogate primary-key column: the
// builtin "id" (a NOT NULL uuid defaulting to gen_random_uuid()).
const pkColumnType = "id"

// DefaultConfig returns the increment-A defaults: small multi-schema models
// with a handful of tables and columns each.
func DefaultConfig() Config {
	return Config{
		MinSchemas: 1, MaxSchemas: 3,
		MinTables: 1, MaxTables: 4,
		MinExtraColumns: 0, MaxExtraColumns: 5,
		PGVersion:   16,
		ColumnTypes: defaultColumnTypes(),
		MinGroups:   0, MaxGroups: 2,
	}
}

// withDefaults fills zero-valued knobs where zero is unambiguously "unset". A
// model needs at least one schema and one table, so a zero max there means the
// caller left it blank. PGVersion and ColumnTypes are likewise filled when
// blank. The extra-column range is NOT defaulted: zero extra columns (a bare
// surrogate-PK table) is a valid explicit choice, so honoring it literally
// beats guessing — DefaultConfig sets the 0..5 spread explicitly.
func withDefaults(cfg Config) Config {
	if cfg.MaxSchemas == 0 {
		cfg.MinSchemas, cfg.MaxSchemas = 1, 3
	}
	if cfg.MaxTables == 0 {
		cfg.MinTables, cfg.MaxTables = 1, 4
	}
	if cfg.PGVersion == 0 {
		cfg.PGVersion = 16
	}
	if len(cfg.ColumnTypes) == 0 {
		cfg.ColumnTypes = defaultColumnTypes()
	}
	return cfg
}

// defaultColumnTypes draws the non-PK column-type pool from the real builtin
// registry, excluding the identity/surrogate types ("id", "auto_id"): those are
// reserved for the primary key, and scattering identity columns through a table
// would generate models that are only accidentally valid.
func defaultColumnTypes() []string {
	var out []string
	for _, name := range semtype.NewBuiltinRegistry().BuiltinNames() {
		if name == "id" || name == "auto_id" {
			continue
		}
		out = append(out, name)
	}
	return out
}

// Generator returns a rapid generator of valid flat models as a slice of
// per-schema RawSchema values, ready for model.BuildMulti. Because it is a
// rapid.Generator, it composes into larger generators and shrinks structurally.
func Generator(cfg Config) *rapid.Generator[[]*parse.RawSchema] {
	cfg = withDefaults(cfg)
	return rapid.Custom(func(t *rapid.T) []*parse.RawSchema {
		nSchemas := rapid.IntRange(cfg.MinSchemas, cfg.MaxSchemas).Draw(t, "schema_count")
		raws := make([]*parse.RawSchema, 0, nSchemas)
		for s := 0; s < nSchemas; s++ {
			raws = append(raws, genSchema(t, cfg, s))
		}
		genGroups(t, cfg, raws)
		return raws
	})
}

// Draw is a convenience for property tests: it draws one model from the
// generator built for cfg.
func Draw(t *rapid.T, cfg Config) []*parse.RawSchema {
	return Generator(cfg).Draw(t, "model")
}

// genGroups attaches [groups] entries to the model, drawn from the full set of
// generated (globally-unique) table names. Groups are model-level: they are
// merged across raw schemas by BuildMulti, so all groups are attached to the
// first raw schema. Each group references a random non-empty subset of tables,
// so resolveGroups always validates cleanly (every referenced table exists).
func genGroups(t *rapid.T, cfg Config, raws []*parse.RawSchema) {
	if cfg.MaxGroups == 0 || len(raws) == 0 {
		return
	}
	var allTables []string
	for _, raw := range raws {
		for _, tbl := range raw.Tables {
			allTables = append(allTables, tbl.Name)
		}
	}
	if len(allTables) == 0 {
		return
	}
	nGroups := rapid.IntRange(cfg.MinGroups, cfg.MaxGroups).Draw(t, "group_count")
	if nGroups == 0 {
		return
	}
	groups := make(map[string][]string, nGroups)
	for g := 0; g < nGroups; g++ {
		var members []string
		for i, tbl := range allTables {
			if rapid.Bool().Draw(t, fmt.Sprintf("group_%d_has_%d", g, i)) {
				members = append(members, tbl)
			}
		}
		if len(members) == 0 {
			// Guarantee a non-empty group so the entry is meaningful.
			members = []string{allTables[rapid.IntRange(0, len(allTables)-1).Draw(t, fmt.Sprintf("group_%d_fallback", g))]}
		}
		groups[fmt.Sprintf("group_%d", g)] = members
	}
	raws[0].Groups = groups
}

func genSchema(t *rapid.T, cfg Config, s int) *parse.RawSchema {
	schemaName := fmt.Sprintf("schema_%d", s)
	nTables := rapid.IntRange(cfg.MinTables, cfg.MaxTables).Draw(t, fmt.Sprintf("table_count_%d", s))
	raw := &parse.RawSchema{
		Meta: parse.RawMeta{
			Version: cfg.PGVersion,
			Schema:  schemaName,
		},
	}
	for i := 0; i < nTables; i++ {
		raw.Tables = append(raw.Tables, genTable(t, cfg, s, i))
	}
	return raw
}

func genTable(t *rapid.T, cfg Config, s, i int) parse.RawTable {
	// Globally-unique, snake_case table name (schema-qualified by index so two
	// schemas never share a table name in the merged model).
	name := fmt.Sprintf("t_%d_%d", s, i)
	comment := fmt.Sprintf("Generated table %s", name)

	columns := []parse.RawColumn{{Name: pkColumnName, Type: pkColumnType}}

	nExtra := rapid.IntRange(cfg.MinExtraColumns, cfg.MaxExtraColumns).Draw(t, name+"_column_count")
	for j := 0; j < nExtra; j++ {
		colType := rapid.SampledFrom(cfg.ColumnTypes).Draw(t, fmt.Sprintf("%s_col_%d_type", name, j))
		columns = append(columns, parse.RawColumn{
			Name: fmt.Sprintf("col_%d", j),
			Type: colType,
		})
	}

	return parse.RawTable{
		Name:    name,
		Comment: &comment,
		PK:      []string{pkColumnName},
		Columns: columns,
	}
}
