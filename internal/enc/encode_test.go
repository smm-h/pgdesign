package enc

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/modelgen"
	"github.com/smm-h/pgdesign/internal/parse"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/typeinfo"
	"pgregory.net/rapid"
)

// buildModel runs generated raw schemas through the real Build pipeline
// (BuildMulti, which Canonicalizes), returning the canonical model the encoder
// consumes. It mirrors modelgen's own oracle so generated inputs reach enc in
// exactly the shape production would produce.
func buildModel(t rapid.TB, raws []*parse.RawSchema) *model.Schema {
	reg := semtype.NewBuiltinRegistry()
	for _, raw := range raws {
		if uts := parse.CollectUserTypes(raw); len(uts) > 0 {
			if d := reg.LoadUserTypes(uts); d.HasErrors() {
				t.Fatalf("LoadUserTypes: %v", d.Errors())
			}
		}
	}
	s, diags := model.BuildMulti(raws, reg)
	if diags.HasErrors() {
		t.Fatalf("BuildMulti: %v", diags.Errors())
	}
	return s
}

// TestPerObjectBytesIndependentOfNeighbors: a table's canonical bytes are a
// function of the table alone. The bytes computed standalone must equal the
// bytes the table gets inside a full-schema EncodeObjects, regardless of which
// other objects share the schema.
func TestPerObjectBytesIndependentOfNeighbors(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := buildModel(rt, modelgen.Draw(rt, modelgen.DefaultConfig()))
		objs, err := EncodeObjects(s)
		if err != nil {
			rt.Fatalf("EncodeObjects: %v", err)
		}
		for _, tbl := range s.Tables {
			standalone, err := EncodeTable(tbl)
			if err != nil {
				rt.Fatalf("EncodeTable: %v", err)
			}
			got := objs[KeyForTable(tbl)]
			if !bytes.Equal(standalone, got) {
				rt.Fatalf("table %s.%s: standalone bytes differ from in-schema bytes\nstandalone: %s\nin-schema:  %s",
					tbl.Schema, tbl.Name, standalone, got)
			}
		}
	})
}

// TestDecodeEncodeRoundTrip is decode∘enc = id on canonicalized models, in its
// byte-faithful formulation: encoding a canonical model, decoding it, and
// re-encoding yields byte-identical objects for every key. (Byte-equality is
// the right formulation because non-identity fields — caches, provenance — are
// deliberately not decoded; re-encoding proves all IDENTITY content survives.)
func TestDecodeEncodeRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := buildModel(rt, modelgen.Draw(rt, modelgen.DefaultConfig()))
		objs1, err := EncodeObjects(s)
		if err != nil {
			rt.Fatalf("EncodeObjects: %v", err)
		}
		decoded, err := DecodeObjects(objs1)
		if err != nil {
			rt.Fatalf("DecodeObjects: %v", err)
		}
		objs2, err := EncodeObjects(decoded)
		if err != nil {
			rt.Fatalf("re-EncodeObjects: %v", err)
		}
		assertSameObjects(rt, objs1, objs2)
	})
}

// TestShuffledDeclarationOrderConvergence: two models that differ only by a
// permutation of canonical-only collections (schema order, table declaration
// order) encode to identical bytes. This is canonicality, not mere
// repeatability — the encoder must erase declaration order that PostgreSQL does
// not observe. Column order is SEMANTIC and therefore never permuted.
func TestShuffledDeclarationOrderConvergence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		raws := modelgen.Draw(rt, modelgen.DefaultConfig())

		s1 := buildModel(rt, raws)

		// Permute schema order and, within each schema, table order.
		shuffled := shuffleRaws(rt, raws)
		s2 := buildModel(rt, shuffled)

		objs1, err := EncodeObjects(s1)
		if err != nil {
			rt.Fatalf("EncodeObjects s1: %v", err)
		}
		objs2, err := EncodeObjects(s2)
		if err != nil {
			rt.Fatalf("EncodeObjects s2: %v", err)
		}
		assertSameObjects(rt, objs1, objs2)
	})
}

// shuffleRaws returns a deep-enough copy of raws with schema order and each
// schema's table order permuted. Column order within a table is preserved
// (semantic).
func shuffleRaws(rt *rapid.T, raws []*parse.RawSchema) []*parse.RawSchema {
	schemaPerm := rapid.Permutation(indices(len(raws))).Draw(rt, "schema_perm")
	out := make([]*parse.RawSchema, len(raws))
	for i, p := range schemaPerm {
		src := raws[p]
		cp := *src
		tablePerm := rapid.Permutation(indices(len(src.Tables))).Draw(rt, "table_perm")
		cp.Tables = make([]parse.RawTable, len(src.Tables))
		for j, tp := range tablePerm {
			cp.Tables[j] = src.Tables[tp]
		}
		out[i] = &cp
	}
	return out
}

func indices(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

// TestSemanticCollectionsAreSlices asserts the collections classified SEMANTIC
// in ORDER_SEMANTICS.md are modeled as SLICES, never as maps. A semantic order
// accidentally modeled as a map would be silently destroyed by encoding/json's
// key-sorting, so this static check protects identity from a whole class of
// modeling mistakes.
func TestSemanticCollectionsAreSlices(t *testing.T) {
	cases := []struct {
		typ   reflect.Type
		field string
	}{
		{reflect.TypeOf(model.Table{}), "Columns"},
		{reflect.TypeOf(model.Table{}), "PK"},
		{reflect.TypeOf(model.FK{}), "Columns"},
		{reflect.TypeOf(model.FK{}), "RefColumns"},
		{reflect.TypeOf(model.Index{}), "Columns"},
		{reflect.TypeOf(model.Index{}), "Desc"},
		{reflect.TypeOf(model.Enum{}), "Values"},
		{reflect.TypeOf(model.CompositeType{}), "Fields"},
		{reflect.TypeOf(model.Function{}), "Args"},
		{reflect.TypeOf(model.PartitionSpec{}), "Columns"},
		{reflect.TypeOf(model.ExclusionConstraint{}), "Elements"},
		{reflect.TypeOf(model.StateMachine{}), "States"},
	}
	for _, c := range cases {
		f, ok := c.typ.FieldByName(c.field)
		if !ok {
			t.Errorf("%s has no field %s", c.typ.Name(), c.field)
			continue
		}
		if f.Type.Kind() != reflect.Slice {
			t.Errorf("%s.%s must be a slice (semantic order), got %s", c.typ.Name(), c.field, f.Type.Kind())
		}
	}
}

// TestMapKeyOrderingDeterministic: map-typed fields encode to identical bytes
// regardless of Go map insertion order. Increment A produces no maps, so this
// uses a hand-built index whose opclass/collation/with maps are populated in
// two different insertion orders.
func TestMapKeyOrderingDeterministic(t *testing.T) {
	mk := func() model.Index {
		return model.Index{
			Name:    "idx",
			Columns: []string{"a", "b", "c"},
			Method:  "btree",
			Opclasses: func() map[string]string {
				m := map[string]string{}
				m["c"] = "text_ops"
				m["a"] = "int4_ops"
				m["b"] = "int4_ops"
				return m
			}(),
			With: map[string]string{"fillfactor": "70", "deduplicate_items": "off"},
		}
	}
	// Build a second index with keys inserted in the opposite order.
	other := mk()
	other.Opclasses = map[string]string{}
	other.Opclasses["b"] = "int4_ops"
	other.Opclasses["a"] = "int4_ops"
	other.Opclasses["c"] = "text_ops"

	var last []byte
	for i := 0; i < 20; i++ {
		b1, _ := canonicalJSON(indexToForm(mk()))
		b2, _ := canonicalJSON(indexToForm(other))
		if !bytes.Equal(b1, b2) {
			t.Fatalf("map-key ordering not deterministic:\n%s\n%s", b1, b2)
		}
		if last != nil && !bytes.Equal(last, b1) {
			t.Fatalf("encoding not stable across runs")
		}
		last = b1
	}
}

// TestCodecVersionPresent: every top-level form carries the codec epoch and its
// self-describing kind. A decode at a different epoch is a hard error.
func TestCodecVersionPresent(t *testing.T) {
	tbl := model.Table{Name: "t", Schema: "public", Comment: "c",
		Columns: []model.Column{{Name: "id", PGType: typeinfo.Type{Base: "int4"}, NotNull: true}}, PK: []string{"id"}}
	b, err := EncodeTable(tbl)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"codec":1`)) {
		t.Errorf("encoded table missing codec field: %s", b)
	}
	if !bytes.Contains(b, []byte(`"kind":"table"`)) {
		t.Errorf("encoded table missing kind field: %s", b)
	}
}

func assertSameObjects(t rapid.TB, a, b map[Key][]byte) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("object count differs: %d vs %d", len(a), len(b))
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			t.Fatalf("key %s missing from second encoding", k)
		}
		if !bytes.Equal(av, bv) {
			t.Fatalf("key %s bytes differ:\n%s\n%s", k, av, bv)
		}
	}
}
