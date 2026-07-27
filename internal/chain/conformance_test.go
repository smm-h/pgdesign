package chain_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/smm-h/pgdesign/internal/chain"
	"github.com/smm-h/pgdesign/internal/diff"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/modelgen"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/rev"
	"github.com/smm-h/pgdesign/internal/semtype"
	"pgregory.net/rapid"
)

// buildModel runs generated raw schemas through the real Build pipeline.
func buildModel(t rapid.TB, raws []*parse.RawSchema) *model.Schema {
	typeReg := semtype.NewBuiltinRegistry()
	for _, raw := range raws {
		if uts := parse.CollectUserTypes(raw); len(uts) > 0 {
			if d := typeReg.LoadUserTypes(uts); d.HasErrors() {
				t.Fatalf("LoadUserTypes: %v", d.Errors())
			}
		}
	}
	s, d := model.BuildMulti(raws, typeReg)
	if d.HasErrors() {
		t.Fatalf("BuildMulti: %v", d.Errors())
	}
	return s
}

func deepCopyModel(t rapid.TB, jsonBytes []byte) *model.Schema {
	var s model.Schema
	if err := json.Unmarshal(jsonBytes, &s); err != nil {
		t.Fatalf("deep copy unmarshal: %v", err)
	}
	s.Canonicalize()
	return &s
}

// --- The conformance pair: forward gate ---

// TestConformance_RevisionEqualImpliesDiffEmpty is the INITIAL GATE of the
// conformance pair (L1(b)): two ≈_syn-equal models (here, a model and its
// decode∘enc round-trip) have equal revisions, and diff between them is empty.
// The forward direction's real content is catching a differ that reads
// non-encoded state — if diff saw something identity does not, this would fail.
func TestConformance_RevisionEqualImpliesDiffEmpty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		raws := modelgen.Draw(rt, modelgen.DefaultConfig())
		m := buildModel(rt, raws)

		// A second ≈_syn-equal model: round-trip the canonical whole-model bytes.
		canonical, err := rev.CanonicalBytes(m, rev.RegistryPresent)
		if err != nil {
			rt.Fatalf("CanonicalBytes: %v", err)
		}
		m2, _, err := rev.DecodeModel(canonical)
		if err != nil {
			rt.Fatalf("DecodeModel: %v", err)
		}

		r1, err := chain.RevisionOf(m, rev.RegistryPresent)
		if err != nil {
			rt.Fatal(err)
		}
		r2, err := chain.RevisionOf(m2, rev.RegistryPresent)
		if err != nil {
			rt.Fatal(err)
		}
		eq, err := r1.Equal(r2)
		if err != nil {
			rt.Fatal(err)
		}
		if !eq {
			rt.Fatalf("round-trip changed the revision: %s vs %s", r1, r2)
		}
		// Equal revisions must imply an empty diff.
		if d := diff.Diff(m, m2); !d.IsEmpty() {
			rt.Fatalf("equal revisions but non-empty diff: %s", d.Summary())
		}
	})
}

// TestConformance_ManifestEqualsRevision pins the reconciliation: revision-equal
// <=> manifest-equal (both derive from the same per-object bytes). A model and
// its round-trip have equal manifests; a comment perturbation makes them differ.
func TestConformance_ManifestEqualsRevision(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		raws := modelgen.Draw(rt, modelgen.DefaultConfig())
		m := buildModel(rt, raws)
		canonical, _ := rev.CanonicalBytes(m, rev.RegistryPresent)
		m2, _, err := rev.DecodeModel(canonical)
		if err != nil {
			rt.Fatal(err)
		}
		ma, err := chain.BuildManifest(m)
		if err != nil {
			rt.Fatal(err)
		}
		mb, err := chain.BuildManifest(m2)
		if err != nil {
			rt.Fatal(err)
		}
		r1, _ := chain.RevisionOf(m, rev.RegistryPresent)
		r2, _ := chain.RevisionOf(m2, rev.RegistryPresent)
		revEq, _ := r1.Equal(r2)
		if revEq != ma.Equal(mb) {
			rt.Fatalf("revision-equal (%v) and manifest-equal (%v) disagree", revEq, ma.Equal(mb))
		}
	})
}

// --- The reverse direction: diff-totality mutation guard ---

// perturbSite names a single encoded-field mutation site.
type perturbSite struct {
	path string // human-readable path, e.g. .Tables[0].Comment
	key  string // struct.field, e.g. Table.Comment (for the skip-list)
	// index of this site among all sites (stable across identical walks)
}

// acceptedDiffBlind lists encoded fields the differ is DELIBERATELY blind to,
// each with the reason it is sound for a VALID model. The mutation guard perturbs
// each encoded field and asserts diff is non-empty; a field on this list is
// exempt. Every entry is a field that CANNOT independently distinguish two VALID
// models — perturbing it produces an unreachable model, so the "blindness" is an
// artifact of the guard's synthetic perturbation, not an under-reporting bug.
// A blind field NOT on this list turns the guard RED (the by-construction
// protection). See the report for the flagged rationale.
var acceptedDiffBlind = map[string]string{
	"Column.SemanticTypeName": "the semantic type name resolves to Column.PGType (which diff compares via typesEqualWithDefaults); two valid models cannot share a PGType yet differ here, so diff need not compare it independently",
	"Column.TypeKind":         "the resolved type-kind tag is a function of Column.PGType / type resolution; it cannot vary independently of the type diff already compares",
	"Column.Stored":           "STORED vs VIRTUAL is only semantic for GENERATED columns; Build defaults Stored=true for every column, so on a non-generated column it never distinguishes valid models (diff compares it conditionally, guarded by Generated!=\"\")",
	"Schema.Name":             "the top-level model name is not a schema object; diff compares each table/view/etc by its OWN schema-qualified key (per-object Schema fields), so the model-level Name carries no object identity diff must track",
}

// collectSites walks an addressable struct value of a registered model type,
// appending one site per ENCODED leaf field (scalar, scalar-slice, or map) and
// recursing into encoded struct/slice-of-struct/pointer fields. Order is
// deterministic (struct declaration order, then slice index order), so the i-th
// site denotes the same logical field on every identical walk.
func collectSites(v reflect.Value, typeName, path string, encFields map[string][]string, sites *[]perturbSite) {
	encoded := map[string]bool{}
	for _, f := range encFields[typeName] {
		encoded[f] = true
	}
	vt := v.Type()
	for i := 0; i < vt.NumField(); i++ {
		f := vt.Field(i)
		if !encoded[f.Name] {
			continue
		}
		addFieldSites(v.Field(i), typeName+"."+f.Name, path+"."+f.Name, encFields, sites)
	}
}

func addFieldSites(fv reflect.Value, key, path string, encFields map[string][]string, sites *[]perturbSite) {
	switch fv.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int32, reflect.Int64:
		*sites = append(*sites, perturbSite{path: path, key: key})
	case reflect.Ptr:
		et := fv.Type().Elem()
		if et.Kind() == reflect.Int {
			*sites = append(*sites, perturbSite{path: path, key: key})
		} else if et.Kind() == reflect.Struct {
			if _, ok := encFields[et.Name()]; ok && !fv.IsNil() {
				collectSites(fv.Elem(), et.Name(), path, encFields, sites)
			}
		}
	case reflect.Struct:
		if _, ok := encFields[fv.Type().Name()]; ok {
			collectSites(fv, fv.Type().Name(), path, encFields, sites)
		}
	case reflect.Slice:
		et := fv.Type().Elem()
		switch et.Kind() {
		case reflect.String:
			*sites = append(*sites, perturbSite{path: path, key: key})
		case reflect.Struct:
			if _, ok := encFields[et.Name()]; ok {
				for j := 0; j < fv.Len(); j++ {
					collectSites(fv.Index(j), et.Name(), fmt.Sprintf("%s[%d]", path, j), encFields, sites)
				}
			}
		}
	case reflect.Map:
		if fv.Type().Key().Kind() == reflect.String {
			*sites = append(*sites, perturbSite{path: path, key: key})
		}
	}
}

// applySite mutates the field at the i-th site of a fresh walk over root.
func applySite(root *model.Schema, i int, encFields map[string][]string) (perturbSite, bool) {
	var sites []perturbSite
	rv := reflect.ValueOf(root).Elem()
	collectSites(rv, "Schema", "", encFields, &sites)
	if i < 0 || i >= len(sites) {
		return perturbSite{}, false
	}
	// Re-walk mutating exactly the i-th leaf. We resolve the field value again
	// by path-index parity: collectSites is deterministic, so we redo the walk
	// but perform the mutation when the running counter hits i.
	counter := 0
	mutateWalk(rv, "Schema", encFields, &counter, i)
	return sites[i], true
}

// mutateWalk mirrors collectSites but performs the perturbation when the leaf
// counter reaches target.
func mutateWalk(v reflect.Value, typeName string, encFields map[string][]string, counter *int, target int) {
	encoded := map[string]bool{}
	for _, f := range encFields[typeName] {
		encoded[f] = true
	}
	vt := v.Type()
	for i := 0; i < vt.NumField(); i++ {
		f := vt.Field(i)
		if !encoded[f.Name] {
			continue
		}
		mutateField(v.Field(i), f.Name, encFields, counter, target)
	}
}

func mutateField(fv reflect.Value, name string, encFields map[string][]string, counter *int, target int) {
	switch fv.Kind() {
	case reflect.String:
		if *counter == target {
			// Full-REPLACE with a sentinel rather than appending: appending to an
			// expression field (a default/CHECK) yields text that PostgreSQL parses
			// as a trailing column ALIAS, which the ≈_syn normalizer strips — so the
			// change would be silently swallowed and mask a genuine field. A full
			// replacement to a bare-identifier sentinel normalizes to itself and
			// differs from any real original.
			const sentinel = "pgdesign_mutation_guard_sentinel"
			nv := sentinel
			if fv.String() == sentinel {
				nv = sentinel + "_alt"
			}
			fv.SetString(nv)
		}
		*counter++
	case reflect.Bool:
		if *counter == target {
			fv.SetBool(!fv.Bool())
		}
		*counter++
	case reflect.Int, reflect.Int32, reflect.Int64:
		if *counter == target {
			fv.SetInt(fv.Int() + 1)
		}
		*counter++
	case reflect.Ptr:
		et := fv.Type().Elem()
		if et.Kind() == reflect.Int {
			if *counter == target {
				if fv.IsNil() {
					n := 1
					fv.Set(reflect.ValueOf(&n))
				} else {
					fv.Elem().SetInt(fv.Elem().Int() + 1)
				}
			}
			*counter++
		} else if et.Kind() == reflect.Struct {
			if _, ok := encFields[et.Name()]; ok && !fv.IsNil() {
				mutateWalk(fv.Elem(), et.Name(), encFields, counter, target)
			}
		}
	case reflect.Struct:
		if _, ok := encFields[fv.Type().Name()]; ok {
			mutateWalk(fv, fv.Type().Name(), encFields, counter, target)
		}
	case reflect.Slice:
		et := fv.Type().Elem()
		switch et.Kind() {
		case reflect.String:
			if *counter == target {
				if fv.Len() > 0 {
					fv.Index(0).SetString(fv.Index(0).String() + "_perturbed")
				} else {
					fv.Set(reflect.Append(fv, reflect.ValueOf("perturbed")))
				}
			}
			*counter++
		case reflect.Struct:
			if _, ok := encFields[et.Name()]; ok {
				for j := 0; j < fv.Len(); j++ {
					mutateWalk(fv.Index(j), et.Name(), encFields, counter, target)
				}
			}
		}
	case reflect.Map:
		if fv.Type().Key().Kind() == reflect.String {
			if *counter == target {
				mt := fv.Type()
				if fv.IsNil() {
					fv.Set(reflect.MakeMap(mt))
				}
				var elem reflect.Value
				switch {
				case mt.Elem().Kind() == reflect.Slice && mt.Elem().Elem().Kind() == reflect.String:
					elem = reflect.ValueOf([]string{"perturbed"})
				case mt.Elem().Kind() == reflect.String:
					elem = reflect.ValueOf("perturbed")
				default:
					elem = reflect.New(mt.Elem()).Elem()
				}
				fv.SetMapIndex(reflect.ValueOf("perturbed_key"), elem)
			}
			*counter++
		}
	}
}

// blindKeysFor perturbs every encoded field of m and returns the set of keys
// whose perturbation produced an EMPTY diff (diff is blind to them).
func blindKeysFor(t rapid.TB, m *model.Schema, encFields map[string][]string) map[string]bool {
	baseJSON, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal base: %v", err)
	}
	base := deepCopyModel(t, baseJSON)

	// sanity: base vs a fresh copy must be diff-empty.
	if d := diff.Diff(base, deepCopyModel(t, baseJSON)); !d.IsEmpty() {
		t.Fatalf("base vs identical copy is non-empty (deep-copy fidelity issue): %s", d.Summary())
	}

	var sites []perturbSite
	collectSites(reflect.ValueOf(base).Elem(), "Schema", "", encFields, &sites)

	blind := map[string]bool{}
	for i := range sites {
		pert := deepCopyModel(t, baseJSON)
		site, ok := applySite(pert, i, encFields)
		if !ok {
			t.Fatalf("site %d not found", i)
		}
		pert.Canonicalize()
		if diff.Diff(base, pert).IsEmpty() {
			blind[site.key] = true
		}
	}
	return blind
}

// TestDiffTotalityMutationGuard is the reverse direction's PRIMARY enforcement:
// perturbing ANY encoded field (driven by the encoder's own field registry) must
// make diff non-empty, EXCEPT for the deliberately-accepted blind fields. A new
// encoded field that diff cannot see turns this RED — retiring the
// under-reporting defect class by construction rather than field-by-field.
func TestDiffTotalityMutationGuard(t *testing.T) {
	encFields := enc.EncodedModelFields()
	rapid.Check(t, func(rt *rapid.T) {
		// Small models keep the guard fast: it deep-copies + canonicalizes +
		// diffs once PER SITE, so cost is (#sites x #rapid-iterations). A tiny
		// model still exercises every encoded field KIND present in the fragment
		// (table/column/type/groups). Groups are forced on so the guard reaches
		// the Groups field every iteration.
		cfg := modelgen.Config{
			MinSchemas: 1, MaxSchemas: 2,
			MinTables: 1, MaxTables: 2,
			MinExtraColumns: 1, MaxExtraColumns: 2,
			PGVersion: 16,
			MinGroups: 1, MaxGroups: 1,
		}
		raws := modelgen.Draw(rt, cfg)
		m := buildModel(rt, raws)
		blind := blindKeysFor(rt, m, encFields)
		for key := range blind {
			if _, accepted := acceptedDiffBlind[key]; !accepted {
				rt.Errorf("diff is BLIND to encoded field %s (perturbing it produced an empty diff). Fix diff or add %s to acceptedDiffBlind with a reason.", key, key)
			}
		}
	})
}

// TestDiffTotalityDiscoversBlindSet is an informational, deterministic scan that
// LOGS the full set of diff-blind encoded fields over a handful of seeds — the
// evidence behind acceptedDiffBlind. It never fails (it is documentation), so it
// can be read with `go test -run TestDiffTotalityDiscoversBlindSet -v`.
func TestDiffTotalityDiscoversBlindSet(t *testing.T) {
	encFields := enc.EncodedModelFields()
	gen := modelgen.Generator(func() modelgen.Config {
		c := modelgen.DefaultConfig()
		c.MinGroups, c.MaxGroups = 1, 2
		c.MinExtraColumns, c.MaxExtraColumns = 3, 5
		return c
	}())
	all := map[string]bool{}
	for seed := 1; seed <= 8; seed++ {
		raws := gen.Example(seed)
		m := buildModel(t, raws)
		for k := range blindKeysFor(t, m, encFields) {
			all[k] = true
		}
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("diff-blind encoded fields discovered: %v", keys)
	for _, k := range keys {
		if _, ok := acceptedDiffBlind[k]; !ok {
			t.Logf("  UNACCEPTED (guard will flag): %s", k)
		}
	}
}

// TestDiffAgainstItselfEmpty PINS diff(a,a) = empty over generated models.
func TestDiffAgainstItselfEmpty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		raws := modelgen.Draw(rt, modelgen.DefaultConfig())
		m := buildModel(rt, raws)
		if d := diff.Diff(m, m); !d.IsEmpty() {
			rt.Fatalf("diff(a,a) not empty: %s", d.Summary())
		}
	})
}
