package imports

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/sqlparse"
)

// ExtractSurface computes the import surface a consumer needs from a framework
// model: the referenced tables plus the transitive composition-closure of their
// type definitions (enums, domains, composites, state machines). Every surface
// object is re-stamped into targetSchema so it matches how the consumer
// references it (fk.RefSchema == targetSchema). refTables is the set of table
// names the consumer references through this alias.
//
// A referenced table absent from the framework model is a hard error (the alias
// promises a table the framework does not provide). The returned surface is a
// sub-model containing ONLY the surface objects; it is not canonicalized against
// a full schema (it has no meta), so callers encode it per-object.
func ExtractSurface(framework *model.Schema, refTables []string, targetSchema string) (*model.Schema, error) {
	// Index framework objects by name.
	tableByName := map[string]model.Table{}
	for _, t := range framework.Tables {
		if _, dup := tableByName[t.Name]; dup {
			return nil, fmt.Errorf("imports: framework declares table %q in more than one schema; ambiguous import target", t.Name)
		}
		tableByName[t.Name] = t
	}
	enumByName := map[string]model.Enum{}
	for _, e := range framework.Enums {
		enumByName[e.Name] = e
	}
	domainByName := map[string]model.Domain{}
	for _, d := range framework.Domains {
		domainByName[d.Name] = d
	}
	compositeByName := map[string]model.CompositeType{}
	for _, c := range framework.CompositeTypes {
		compositeByName[c.Name] = c
	}
	smByName := map[string]model.StateMachine{}
	for _, sm := range framework.StateMachines {
		smByName[sm.Name] = sm
	}

	surface := &model.Schema{}
	wantEnum := map[string]bool{}
	wantDomain := map[string]bool{}
	wantComposite := map[string]bool{}
	wantSM := map[string]bool{}

	// typeNamesOf returns the candidate user-type names a column may reference.
	typeNamesOf := func(c model.Column) []string {
		names := []string{}
		if c.SemanticTypeName != "" {
			names = append(names, c.SemanticTypeName)
		}
		if c.PGType.Base != "" {
			names = append(names, c.PGType.Base)
		}
		if c.PGType.DomainName != "" {
			names = append(names, c.PGType.DomainName)
		}
		return names
	}

	// markType records that a named type is needed and, for composites, recurses
	// into field types so the closure is transitive.
	var markType func(name string)
	markType = func(name string) {
		if e, ok := enumByName[name]; ok && !wantEnum[name] {
			wantEnum[name] = true
			_ = e
		}
		if d, ok := domainByName[name]; ok && !wantDomain[name] {
			wantDomain[name] = true
			_ = d
		}
		if sm, ok := smByName[name]; ok && !wantSM[name] {
			wantSM[name] = true
			_ = sm
		}
		if c, ok := compositeByName[name]; ok && !wantComposite[name] {
			wantComposite[name] = true
			for _, f := range c.Fields {
				if f.PGType.Base != "" {
					markType(f.PGType.Base)
				}
				if f.PGType.DomainName != "" {
					markType(f.PGType.DomainName)
				}
			}
		}
	}

	// Collect referenced tables (deduplicated) and seed the type closure.
	seenTable := map[string]bool{}
	sortedRefs := append([]string(nil), refTables...)
	sort.Strings(sortedRefs)
	for _, name := range sortedRefs {
		if seenTable[name] {
			continue
		}
		seenTable[name] = true
		t, ok := tableByName[name]
		if !ok {
			return nil, fmt.Errorf("imports: referenced table %q is not provided by the framework schema", name)
		}
		t.Schema = targetSchema
		surface.Tables = append(surface.Tables, t)
		for _, col := range t.Columns {
			for _, tn := range typeNamesOf(col) {
				markType(tn)
			}
		}
	}

	// Materialize the closed type set, re-stamped into targetSchema.
	for name := range wantEnum {
		e := enumByName[name]
		e.Schema = targetSchema
		surface.Enums = append(surface.Enums, e)
	}
	for name := range wantDomain {
		d := domainByName[name]
		d.Schema = targetSchema
		surface.Domains = append(surface.Domains, d)
	}
	for name := range wantComposite {
		c := compositeByName[name]
		c.Schema = targetSchema
		surface.CompositeTypes = append(surface.CompositeTypes, c)
	}
	for name := range wantSM {
		sm := smByName[name]
		sm.Schema = targetSchema
		surface.StateMachines = append(surface.StateMachines, sm)
	}

	// Deterministic ordering (encoding is per-object and order-independent, but a
	// stable model keeps callers reproducible).
	sort.Slice(surface.Tables, func(i, j int) bool { return surface.Tables[i].Name < surface.Tables[j].Name })
	sort.Slice(surface.Enums, func(i, j int) bool { return surface.Enums[i].Name < surface.Enums[j].Name })
	sort.Slice(surface.Domains, func(i, j int) bool { return surface.Domains[i].Name < surface.Domains[j].Name })
	sort.Slice(surface.CompositeTypes, func(i, j int) bool { return surface.CompositeTypes[i].Name < surface.CompositeTypes[j].Name })
	sort.Slice(surface.StateMachines, func(i, j int) bool { return surface.StateMachines[i].Name < surface.StateMachines[j].Name })

	return surface, nil
}

// encodeSurfaceObjects encodes each surface object to its canonical per-object
// form, keyed by kind-qualified manifest key string. Unlike enc.EncodeObjects it
// emits NO schema-meta object: the surface is a sub-model, decoded per-object.
func encodeSurfaceObjects(s *model.Schema) (map[string][]byte, error) {
	out := map[string][]byte{}
	add := func(key string, b []byte, err error) error {
		if err != nil {
			return err
		}
		if _, dup := out[key]; dup {
			return fmt.Errorf("imports: duplicate surface object key %s", key)
		}
		out[key] = b
		return nil
	}
	for _, t := range s.Tables {
		b, err := enc.EncodeTable(t)
		if err := add(enc.KeyForTable(t).String(), b, err); err != nil {
			return nil, err
		}
	}
	for _, e := range s.Enums {
		b, err := enc.EncodeEnum(e)
		if err := add(enc.KeyForEnum(e).String(), b, err); err != nil {
			return nil, err
		}
	}
	for _, d := range s.Domains {
		b, err := enc.EncodeDomain(d)
		if err := add(enc.KeyForDomain(d).String(), b, err); err != nil {
			return nil, err
		}
	}
	for _, c := range s.CompositeTypes {
		b, err := enc.EncodeCompositeType(c)
		if err := add(enc.KeyForComposite(c).String(), b, err); err != nil {
			return nil, err
		}
	}
	for _, sm := range s.StateMachines {
		b, err := enc.EncodeStateMachine(sm)
		if err := add(enc.KeyForStateMachine(sm).String(), b, err); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// semanticForm returns the N-normalized per-object bytes for a surface model:
// the same per-object encoding as encodeSurfaceObjects but with every SQL
// expression field (defaults, CHECKs, index WHEREs) passed through
// sqlparse.NormalizeExpr first. Two surfaces that differ only by an
// equivalently-spelled default produce identical semantic forms — the property
// that keeps SemanticHash from false-drifting where raw ids would.
func semanticForm(s *model.Schema) (map[string][]byte, error) {
	norm := &model.Schema{
		Enums:          s.Enums,
		Domains:        make([]model.Domain, len(s.Domains)),
		CompositeTypes: s.CompositeTypes,
		StateMachines:  s.StateMachines,
		Tables:         make([]model.Table, len(s.Tables)),
	}
	for i, t := range s.Tables {
		norm.Tables[i] = normalizeTableExprs(t)
	}
	for i, d := range s.Domains {
		norm.Domains[i] = normalizeDomainExprs(d)
	}
	return encodeSurfaceObjects(norm)
}

// normalizeTableExprs returns a copy of t with its columns' default expressions,
// CHECK constraint expressions, and index WHERE clauses N-normalized.
func normalizeTableExprs(t model.Table) model.Table {
	cols := make([]model.Column, len(t.Columns))
	for i, c := range t.Columns {
		if c.Default != nil {
			n := sqlparse.NormalizeExpr(*c.Default)
			c.Default = &n
		}
		if c.DefaultExpr != "" {
			c.DefaultExpr = sqlparse.NormalizeExpr(c.DefaultExpr)
		}
		if c.Generated != "" {
			c.Generated = sqlparse.NormalizeExpr(c.Generated)
		}
		cols[i] = c
	}
	t.Columns = cols
	checks := make([]model.CheckConstraint, len(t.Checks))
	for i, ck := range t.Checks {
		ck.Expr = sqlparse.NormalizeExpr(ck.Expr)
		checks[i] = ck
	}
	t.Checks = checks
	idxs := make([]model.Index, len(t.Indexes))
	for i, idx := range t.Indexes {
		if idx.Where != "" {
			idx.Where = sqlparse.NormalizeExpr(idx.Where)
		}
		idxs[i] = idx
	}
	t.Indexes = idxs
	return t
}

// normalizeDomainExprs returns a copy of d with its CHECK and default expressions
// N-normalized.
func normalizeDomainExprs(d model.Domain) model.Domain {
	if d.Check != "" {
		d.Check = sqlparse.NormalizeExpr(d.Check)
	}
	if d.Default != "" {
		d.Default = sqlparse.NormalizeExpr(d.Default)
	}
	if d.DefaultExpr != "" {
		d.DefaultExpr = sqlparse.NormalizeExpr(d.DefaultExpr)
	}
	return d
}

// hashIDs returns the surface hash: SHA-256 over the sorted content ids joined by
// newline. It changes iff the vendored bytes change.
func hashIDs(ids []string) string {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	h := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(h[:])
}

// hashForms returns the semantic hash: SHA-256 over the per-object forms sorted
// by key, each rendered as "key\x00<hex(sha256(bytes))>". It is invariant under
// equivalently-spelled defaults (the forms are N-normalized by the caller).
func hashForms(forms map[string][]byte) string {
	keys := make([]string, 0, len(forms))
	for k := range forms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		sum := sha256.Sum256(forms[k])
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(hex.EncodeToString(sum[:])))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Vendor encodes the surface objects, stores them in the alias objstore rooted at
// root, and returns the per-object entries plus the surface and semantic hashes.
// Puts are idempotent (content-addressed), so re-vendoring the same surface is a
// no-op on disk.
func Vendor(surface *model.Schema, root string) (entries []ObjectEntry, surfaceHash, semanticHash string, err error) {
	store, err := objstore.New(root, enc.CodecVersion)
	if err != nil {
		return nil, "", "", fmt.Errorf("imports: opening alias store: %w", err)
	}
	objs, err := encodeSurfaceObjects(surface)
	if err != nil {
		return nil, "", "", err
	}
	ids := make([]string, 0, len(objs))
	for key, b := range objs {
		id, err := store.Put(b)
		if err != nil {
			return nil, "", "", fmt.Errorf("imports: vendoring %s: %w", key, err)
		}
		entries = append(entries, ObjectEntry{Key: key, ID: id})
		ids = append(ids, id)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })

	forms, err := semanticForm(surface)
	if err != nil {
		return nil, "", "", err
	}
	return entries, hashIDs(ids), hashForms(forms), nil
}

// InferRequirements returns the extension requirements and pg_version floor to
// carry in the lockfile. Extensions are the framework's declared set (a safe
// superset; per-object refinement is deferred — 7.3 wires the re-declaration
// error). pg_version is the framework model's PGVersion.
func InferRequirements(framework *model.Schema) (extensions []string, pgVersion int) {
	exts := append([]string(nil), framework.Extensions...)
	sort.Strings(exts)
	return exts, framework.PGVersion
}
