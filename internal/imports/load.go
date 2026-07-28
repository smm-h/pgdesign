package imports

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
)

// LoadSurface decodes the vendored surface for one alias (imports/<alias>/) into a
// sub-model containing the imported REFERENCE objects — tables plus their type
// closure (enums, domains, composites, state machines) — already stamped into the
// target schema. It reads only the lockfile and the content-addressed store; it
// never touches the remote (offline builds and the drift check need no network).
//
// The returned schema is a bare sub-model: its Tables/Enums/... are populated and
// each object carries the target Schema it was vendored into, but no
// canonicalization or FK-graph derivation is performed (the consumer's Build folds
// these into its own model via WithImportedTables and the registry). A referenced
// object that fails to resolve or decode is a hard error — the vendored surface is
// part of the committed project state, so corruption must fail loudly rather than
// silently drop a reference (which would then trip a spurious E204).
func LoadSurface(projectDir, alias string) (*model.Schema, error) {
	lf, err := ReadLockfile(projectDir, alias)
	if err != nil {
		return nil, err
	}
	store, err := objstore.New(AliasDir(projectDir, alias), enc.CodecVersion)
	if err != nil {
		return nil, fmt.Errorf("imports: opening vendored store for %q: %w", alias, err)
	}
	surface := &model.Schema{}
	for _, obj := range lf.Objects {
		b, err := store.Get(obj.ID)
		if err != nil {
			return nil, fmt.Errorf("imports: %q: object %s does not resolve in the vendored store: %w (run `pgdesign import lock`)", alias, obj.Key, err)
		}
		if err := enc.DecodeObject(surface, b); err != nil {
			return nil, fmt.Errorf("imports: %q: object %s failed to decode: %w", alias, obj.Key, err)
		}
	}
	return surface, nil
}

// LoadAllSurfaces decodes the vendored surfaces for every alias that has a
// committed lockfile under projectDir, aggregating their reference objects into
// one sub-model. aliases is the set of declared import aliases (typically the keys
// of the project's [imports] config); aliases without a lockfile are skipped (the
// project has not run `import lock` yet — the build proceeds without the union,
// and the unresolved FK surfaces as a normal E204/E236). The aggregated tables are
// suitable for model.WithImportedTables; enums/domains/composites/state machines
// are returned for registry loading so imported types are usable in local columns.
func LoadAllSurfaces(projectDir string, aliases []string) (*model.Schema, error) {
	agg := &model.Schema{}
	for _, alias := range ImportAliases(projectDir, aliases) {
		s, err := LoadSurface(projectDir, alias)
		if err != nil {
			return nil, err
		}
		agg.Tables = append(agg.Tables, s.Tables...)
		agg.Enums = append(agg.Enums, s.Enums...)
		agg.Domains = append(agg.Domains, s.Domains...)
		agg.CompositeTypes = append(agg.CompositeTypes, s.CompositeTypes...)
		agg.StateMachines = append(agg.StateMachines, s.StateMachines...)
	}
	return agg, nil
}
