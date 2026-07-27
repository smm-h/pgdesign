// Package rev is pgdesign's whole-model canonical form, revision identity, and
// the single JSON envelope serializer (roadmap kernel 1.5, laws L1 + L7).
//
// It sits one level above the per-object encoder (internal/enc): enc maps each
// resolved model object to canonical bytes; rev concatenates those per-object
// forms — in sorted manifest-key order, behind a versioned preamble — into ONE
// canonical whole-model byte string, and takes its SHA-256 as the model's
// revision. There is exactly one whole-model serializer, so `generate json`
// output and serve's /schema response are byte-identical for the same model
// (L1: one canonical form everywhere).
//
// Two structures are load-bearing:
//
//   - Revision (L7): an opaque content identity tagged with its MODEL CLASS.
//     A model built from TOML carries type information (registry-present); an
//     introspected model does not (registry-absent). The class marker lives
//     INSIDE the hashed bytes, so the two classes can never collide on a hash;
//     and the Revision type is deliberately non-comparable with == (it holds a
//     slice), forcing all comparison through Equal, which returns an ERROR on a
//     cross-class comparison rather than silently reporting "not equal".
//
//   - The envelope: the JSON artifact {format_version, revision, model,
//     diagnostics?}. The canonical whole-model bytes are embedded VERBATIM as a
//     json.RawMessage and the envelope is emitted with a COMPACT encoder, so the
//     `model` field bytes are byte-for-byte the bytes that `revision` hashes.
//     Re-indenting or re-encoding them would break revision == hash(model); the
//     envelope resolves the in-band-stamp circularity (bytes cannot contain
//     their own hash) by putting the hash beside the bytes, not inside them.
//
// rev imports enc, model, and diagnostic only. enc stays pure (it never imports
// diagnostic); serve and cmd import rev, never the reverse.
package rev

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
)

// FormatVersion is the version of the whole-model form and envelope STRUCTURE
// (the preamble shape, the objects-array framing, the envelope keys). It is
// independent of enc.CodecVersion, which versions the per-object byte forms:
// a change to how objects are framed bumps FormatVersion; a change to the bytes
// of an object bumps the codec. Both travel inside the hashed preamble.
const FormatVersion = 1

// ModelClass distinguishes model classes per law L7. A model with type
// information (built from TOML, carrying a semtype registry) and an introspected
// model without it are DIFFERENT classes; their revisions are not comparable.
type ModelClass string

const (
	// RegistryPresent is the class of a model built from TOML source, which
	// carries full type information (enums, domains, composites, state machines).
	RegistryPresent ModelClass = "registry_present"
	// RegistryAbsent is the class of a model recovered by introspecting a live
	// database, which lacks the type registry.
	RegistryAbsent ModelClass = "registry_absent"
)

// valid reports whether c is a known model class. An unset/unknown class is a
// hard error at the serializer boundary — there is no implicit default (L7).
func (c ModelClass) valid() bool {
	return c == RegistryPresent || c == RegistryAbsent
}

// Revision is the opaque content identity of a whole model: the SHA-256 of its
// canonical whole-model bytes, tagged with its model class. It is deliberately
// NOT comparable with == (the sum is a slice), so callers cannot silently get a
// false from a cross-class ==; comparison goes through Equal, which errors on a
// class mismatch (L7).
type Revision struct {
	class ModelClass
	sum   []byte // 32-byte SHA-256; a slice, so Revision is not == comparable
}

// Class returns the model class this revision belongs to.
func (r Revision) Class() ModelClass { return r.class }

// Hex returns the lowercase hex SHA-256 digest (without a class prefix).
func (r Revision) Hex() string { return hex.EncodeToString(r.sum) }

// String renders the revision as "<class>:<hex>" so a printed revision names
// its model class explicitly — a registry-present and a registry-absent
// revision of otherwise-identical structure are visibly distinct.
func (r Revision) String() string {
	return string(r.class) + ":" + r.Hex()
}

// Equal reports whether two revisions are equal. Comparing revisions of
// different model classes is a TYPE ERROR (L7): it returns a non-nil error, not
// a silent false, so an accidental cross-class comparison cannot be mistaken
// for a genuine difference.
func (r Revision) Equal(other Revision) (bool, error) {
	if r.class != other.class {
		return false, fmt.Errorf("rev: cannot compare revisions of different model classes: %s vs %s", r.class, other.class)
	}
	return bytes.Equal(r.sum, other.sum), nil
}

// wholeModelForm is the canonical whole-model structure: a versioned preamble
// (format version, codec epoch, model class) followed by the per-object forms
// in sorted manifest-key order. The Objects entries are enc's per-object
// canonical bytes embedded verbatim. This struct, marshalled compactly, IS the
// byte string a revision hashes.
type wholeModelForm struct {
	FormatVersion int               `json:"format_version"`
	Codec         int               `json:"codec"`
	Class         ModelClass        `json:"class"`
	Objects       []json.RawMessage `json:"objects"`
}

// compactJSON marshals v to compact, deterministic JSON with HTML escaping
// disabled (so SQL expressions keep <, >, & verbatim) and the streaming
// encoder's trailing newline stripped. It mirrors enc's canonicalJSON so the
// whole-model preamble and the envelope frame use the same byte discipline as
// the per-object forms they embed. Marshalling a json.RawMessage field through
// the compact encoder copies its bytes verbatim (the raw bytes are already
// compact), which is what keeps the embedded model bytes hash-stable.
func compactJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// CanonicalBytes returns the canonical whole-model form for s under model class
// class: the versioned preamble plus every per-object canonical form in sorted
// manifest-key order. This is the byte string Compute hashes and the bytes the
// envelope embeds verbatim. It is a pure function of the CANONICALIZED model —
// the caller is responsible for having built/canonicalized s (Build and
// introspect both do).
func CanonicalBytes(s *model.Schema, class ModelClass) ([]byte, error) {
	if !class.valid() {
		return nil, fmt.Errorf("rev: unknown model class %q", class)
	}
	objs, err := enc.EncodeObjects(s)
	if err != nil {
		return nil, err
	}
	// Whole-model ordering is sorted manifest-key order (Key.String is stable
	// and collision-free across kinds).
	keys := make([]enc.Key, 0, len(objs))
	for k := range objs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	forms := make([]json.RawMessage, len(keys))
	for i, k := range keys {
		forms[i] = json.RawMessage(objs[k])
	}
	form := wholeModelForm{
		FormatVersion: FormatVersion,
		Codec:         enc.CodecVersion,
		Class:         class,
		Objects:       forms,
	}
	return compactJSON(form)
}

// Compute returns the revision of a whole model: the SHA-256 of its canonical
// whole-model bytes, tagged with the model class. Because the class marker is
// inside the hashed bytes, two classes never collide on a hash; because Compute
// also records the class on the returned Revision, cross-class Equal errors.
func Compute(s *model.Schema, class ModelClass) (Revision, error) {
	b, err := CanonicalBytes(s, class)
	if err != nil {
		return Revision{}, err
	}
	return revisionOf(b, class), nil
}

// revisionOf builds a Revision from already-canonical whole-model bytes and a
// class. It is the single place the hash is taken.
func revisionOf(canonicalBytes []byte, class ModelClass) Revision {
	sum := sha256.Sum256(canonicalBytes)
	return Revision{class: class, sum: sum[:]}
}

// envelopeForm is the JSON envelope: format version, the revision string, the
// canonical whole-model bytes embedded verbatim, and optional diagnostics. It
// is emitted compactly so the Model bytes are byte-identical to what Revision
// hashed (re-indentation would desync revision == hash(model)).
type envelopeForm struct {
	FormatVersion int                     `json:"format_version"`
	Revision      string                  `json:"revision"`
	Model         json.RawMessage         `json:"model"`
	Diagnostics   []diagnostic.Diagnostic `json:"diagnostics,omitempty"`
}

// Marshal is THE single whole-model serializer. It produces the envelope JSON
// {format_version, revision, model, diagnostics?} for s under model class class,
// embedding the canonical whole-model bytes verbatim. `generate json` and
// serve's /schema response both call this function, so their bodies are
// byte-identical for the same (schema, class, diagnostics). diags may be nil
// (the field is then omitted).
func Marshal(s *model.Schema, class ModelClass, diags []diagnostic.Diagnostic) ([]byte, error) {
	canonical, err := CanonicalBytes(s, class)
	if err != nil {
		return nil, err
	}
	env := envelopeForm{
		FormatVersion: FormatVersion,
		Revision:      revisionOf(canonical, class).String(),
		Model:         json.RawMessage(canonical),
		Diagnostics:   diags,
	}
	return compactJSON(env)
}

// Envelope is a parsed and revision-VERIFIED envelope. Parse guarantees that
// Revision equals the class-tagged hash of the embedded Model bytes.
type Envelope struct {
	FormatVersion int
	Revision      Revision
	Model         json.RawMessage
	Diagnostics   []diagnostic.Diagnostic
}

// Parse decodes an envelope produced by Marshal and VERIFIES that its revision
// matches the class-tagged hash of the embedded model bytes — the whole point
// of embedding the bytes verbatim. A mismatch (re-encoded or tampered model
// bytes) is a hard error.
func Parse(data []byte) (Envelope, error) {
	var f envelopeForm
	if err := json.Unmarshal(data, &f); err != nil {
		return Envelope{}, fmt.Errorf("rev: parse envelope: %w", err)
	}
	class, sum, err := splitRevision(f.Revision)
	if err != nil {
		return Envelope{}, err
	}
	got := revisionOf(f.Model, class)
	want := Revision{class: class, sum: sum}
	eq, err := got.Equal(want)
	if err != nil {
		return Envelope{}, err
	}
	if !eq {
		return Envelope{}, fmt.Errorf("rev: envelope revision %s does not match hash of embedded model bytes %s", want, got)
	}
	return Envelope{
		FormatVersion: f.FormatVersion,
		Revision:      want,
		Model:         f.Model,
		Diagnostics:   f.Diagnostics,
	}, nil
}

// DecodeModel reconstructs the schema from canonical whole-model bytes (the
// Model field of an envelope, or the output of CanonicalBytes) and returns it
// alongside its model class. The schema is Canonicalized. This realizes
// decode∘enc = id at the whole-model level: encoding a canonical model, then
// DecodeModel, then re-encoding yields byte-identical whole-model bytes.
func DecodeModel(canonicalBytes []byte) (*model.Schema, ModelClass, error) {
	var f wholeModelForm
	if err := json.Unmarshal(canonicalBytes, &f); err != nil {
		return nil, "", fmt.Errorf("rev: decode whole-model form: %w", err)
	}
	if f.FormatVersion != FormatVersion {
		return nil, "", fmt.Errorf("rev: whole-model form_version %d, want %d", f.FormatVersion, FormatVersion)
	}
	if f.Codec != enc.CodecVersion {
		return nil, "", fmt.Errorf("rev: whole-model codec epoch %d, want %d", f.Codec, enc.CodecVersion)
	}
	if !f.Class.valid() {
		return nil, "", fmt.Errorf("rev: whole-model unknown model class %q", f.Class)
	}
	s := &model.Schema{}
	for _, obj := range f.Objects {
		if err := enc.DecodeObject(s, obj); err != nil {
			return nil, "", err
		}
	}
	s.Canonicalize()
	return s, f.Class, nil
}

// splitRevision parses the "<class>:<hex>" revision string form.
func splitRevision(s string) (ModelClass, []byte, error) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			class := ModelClass(s[:i])
			if !class.valid() {
				return "", nil, fmt.Errorf("rev: revision string has unknown model class %q", class)
			}
			sum, err := hex.DecodeString(s[i+1:])
			if err != nil {
				return "", nil, fmt.Errorf("rev: revision string has invalid hex digest: %w", err)
			}
			if len(sum) != sha256.Size {
				return "", nil, fmt.Errorf("rev: revision digest is %d bytes, want %d", len(sum), sha256.Size)
			}
			return class, sum, nil
		}
	}
	return "", nil, fmt.Errorf("rev: malformed revision string %q (want <class>:<hex>)", s)
}
