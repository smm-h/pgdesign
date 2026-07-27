package enc

import (
	"encoding/json"
	"fmt"

	"github.com/smm-h/pgdesign/internal/model"
)

// EncodeObjects encodes every object of a schema to its canonical bytes, keyed
// by kind-qualified manifest key. This is the per-object encoder surface a
// manifest (roadmap 1.4) is built on: a manifest is a sorted map Key ->
// hash(bytes). It is NOT the whole-model serializer — there is no preamble,
// envelope, or concatenation here; that is roadmap 1.5.
//
// The schema-global header is included under a KindSchemaMeta key so that
// EncodeObjects / DecodeObjects round-trips the whole model. State-machine
// type definitions are first-class objects here (KindSMType) — they carry the
// full transition graph with comments and are the identity carrier for SM
// types. The registry snapshot is NOT an identity input (see snapshot.go): all
// identity-bearing registry state now has a model home, so the snapshot is
// empty for identity.
func EncodeObjects(s *model.Schema) (map[Key][]byte, error) {
	out := make(map[Key][]byte)

	add := func(k Key, b []byte, err error) error {
		if err != nil {
			return err
		}
		if _, dup := out[k]; dup {
			return fmt.Errorf("enc: duplicate object key %s", k)
		}
		out[k] = b
		return nil
	}

	meta, metaErr := EncodeSchemaMeta(s)
	if err := add(Key{Kind: KindSchemaMeta, Name: s.Name}, meta, metaErr); err != nil {
		return nil, err
	}
	for _, t := range s.Tables {
		b, err := EncodeTable(t)
		if err := add(KeyForTable(t), b, err); err != nil {
			return nil, err
		}
	}
	for _, v := range s.Views {
		b, err := EncodeView(v)
		if err := add(KeyForView(v), b, err); err != nil {
			return nil, err
		}
	}
	for _, mv := range s.MaterializedViews {
		b, err := EncodeMaterializedView(mv)
		if err := add(KeyForMatView(mv), b, err); err != nil {
			return nil, err
		}
	}
	for _, sq := range s.Sequences {
		b, err := EncodeSequence(sq)
		if err := add(KeyForSequence(sq), b, err); err != nil {
			return nil, err
		}
	}
	for _, fn := range s.Functions {
		b, err := EncodeFunction(fn)
		if err := add(KeyForFunction(fn), b, err); err != nil {
			return nil, err
		}
	}
	for _, e := range s.Enums {
		b, err := EncodeEnum(e)
		if err := add(KeyForEnum(e), b, err); err != nil {
			return nil, err
		}
	}
	for _, d := range s.Domains {
		b, err := EncodeDomain(d)
		if err := add(KeyForDomain(d), b, err); err != nil {
			return nil, err
		}
	}
	for _, c := range s.CompositeTypes {
		b, err := EncodeCompositeType(c)
		if err := add(KeyForComposite(c), b, err); err != nil {
			return nil, err
		}
	}
	for _, sm := range s.StateMachines {
		b, err := EncodeStateMachine(sm)
		if err := add(KeyForStateMachine(sm), b, err); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// PeekKind reads the self-describing "kind" field from a per-object canonical
// form without fully decoding it. It is what lets a whole-model form (roadmap
// 1.5) — an ordered concatenation of per-object forms carrying no external
// keys — route each form to the right decoder: the manifest key is not needed
// for decoding, only the kind, and the kind travels inside the bytes.
func PeekKind(b []byte) (Kind, error) {
	var head struct {
		Kind Kind `json:"kind"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return "", fmt.Errorf("enc: peek kind: %w", err)
	}
	if head.Kind == "" {
		return "", fmt.Errorf("enc: peek kind: form carries no kind field")
	}
	return head.Kind, nil
}

// DecodeObject decodes a single per-object canonical form and merges it into s.
// The schema-global header (KindSchemaMeta) sets the schema-level fields; every
// other kind appends to its collection. It is the shared per-object decode
// dispatch used by both DecodeObjects (keyed map) and the whole-model form
// decoder in roadmap 1.5 (keyless ordered concatenation). DecodeObject does NOT
// Canonicalize — the caller does that once after all objects are merged.
func DecodeObject(s *model.Schema, b []byte) error {
	kind, err := PeekKind(b)
	if err != nil {
		return err
	}
	switch kind {
	case KindSchemaMeta:
		meta, err := DecodeSchemaMeta(b)
		if err != nil {
			return err
		}
		s.Name = meta.Name
		s.Extensions = meta.Extensions
		s.Groups = meta.Groups
		s.PGVersion = meta.PGVersion
	case KindTable:
		t, err := DecodeTable(b)
		if err != nil {
			return err
		}
		s.Tables = append(s.Tables, t)
	case KindView:
		v, err := DecodeView(b)
		if err != nil {
			return err
		}
		s.Views = append(s.Views, v)
	case KindMatView:
		mv, err := DecodeMaterializedView(b)
		if err != nil {
			return err
		}
		s.MaterializedViews = append(s.MaterializedViews, mv)
	case KindSequence:
		sq, err := DecodeSequence(b)
		if err != nil {
			return err
		}
		s.Sequences = append(s.Sequences, sq)
	case KindFunction:
		fn, err := DecodeFunction(b)
		if err != nil {
			return err
		}
		s.Functions = append(s.Functions, fn)
	case KindEnum:
		e, err := DecodeEnum(b)
		if err != nil {
			return err
		}
		s.Enums = append(s.Enums, e)
	case KindDomain:
		d, err := DecodeDomain(b)
		if err != nil {
			return err
		}
		s.Domains = append(s.Domains, d)
	case KindComposite:
		c, err := DecodeCompositeType(b)
		if err != nil {
			return err
		}
		s.CompositeTypes = append(s.CompositeTypes, c)
	case KindSMType:
		sm, err := DecodeStateMachine(b)
		if err != nil {
			return err
		}
		s.StateMachines = append(s.StateMachines, sm)
	default:
		return fmt.Errorf("enc: DecodeObject: unknown kind %q", kind)
	}
	return nil
}

// DecodeObjects reconstructs a schema from a per-object encoding produced by
// EncodeObjects, then Canonicalizes it (rebuilding the derived caches and
// canonical ordering the encoding deliberately omits). Together with
// EncodeObjects it realizes decode∘enc = id on canonicalized models: encoding a
// canonical schema, decoding, and re-encoding yields byte-identical objects.
func DecodeObjects(objs map[Key][]byte) (*model.Schema, error) {
	s := &model.Schema{}
	sawMeta := false
	for k, b := range objs {
		if k.Kind == KindSchemaMeta {
			sawMeta = true
		}
		if err := DecodeObject(s, b); err != nil {
			return nil, err
		}
	}
	if !sawMeta {
		return nil, fmt.Errorf("enc: DecodeObjects: missing schema meta object")
	}
	s.Canonicalize()
	return s, nil
}
