package imports

import (
	"fmt"
	"sort"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// Check runs the OFFLINE import drift check for one alias (roadmap 7.2). It never
// touches the remote — it reads only the vendored surface and the lockfile. It
// reports, at Error severity:
//
//   - Integrity (E233/E234): every locked object id resolves in the alias store
//     (present, correct epoch, hashes to its id), and the surface hash of the
//     sorted resolved ids matches the lockfile.
//   - Semantic consistency (E235): the N-normalized re-encoding of the vendored
//     surface hashes to the lockfile's semantic hash. Because the semantic hash
//     folds equivalently-spelled defaults, a re-spelled default does NOT trip
//     this; a real column-type change DOES.
//   - Reference drift (E236/E237): every FK in the consumer model that resolves
//     through this alias names a table AND columns the vendored surface actually
//     provides, and the FK's local column type matches the referenced surface
//     column type (compared via typeinfo normalization). A drifted column type
//     yields an exact column+FK error. Columns the consumer does not reference
//     are never examined, so unreferenced framework changes are silent.
func Check(projectDir, alias string, consumer *model.Schema) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	lf, err := ReadLockfile(projectDir, alias)
	if err != nil {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error, Code: "E233",
			Message: fmt.Sprintf("import %q: %v (run `pgdesign import lock`)", alias, err),
		})
		return diags
	}

	root := AliasDir(projectDir, alias)
	store, err := objstore.New(root, enc.CodecVersion)
	if err != nil {
		diags = append(diags, diagnostic.Diagnostic{
			Severity: diagnostic.Error, Code: "E233",
			Message: fmt.Sprintf("import %q: opening vendored store: %v", alias, err),
		})
		return diags
	}

	// Integrity: resolve every locked object, decode into a surface model, and
	// recompute the surface hash from the resolved ids.
	surface := &model.Schema{}
	ids := make([]string, 0, len(lf.Objects))
	integrityOK := true
	for _, obj := range lf.Objects {
		b, err := store.Get(obj.ID)
		if err != nil {
			integrityOK = false
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error, Code: "E233",
				Message: fmt.Sprintf("import %q: object %s (%s) does not resolve in the vendored store: %v", alias, obj.Key, obj.ID[:min(12, len(obj.ID))], err),
			})
			continue
		}
		ids = append(ids, obj.ID)
		if err := enc.DecodeObject(surface, b); err != nil {
			integrityOK = false
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error, Code: "E233",
				Message: fmt.Sprintf("import %q: object %s failed to decode: %v", alias, obj.Key, err),
			})
		}
	}

	if integrityOK {
		if got := hashIDs(ids); got != lf.SurfaceHash {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error, Code: "E234",
				Message: fmt.Sprintf("import %q: vendored surface hash %s does not match lockfile %s (the vendored objects or lockfile were altered out of band)", alias, got[:12], lf.SurfaceHash[:min(12, len(lf.SurfaceHash))]),
			})
		}
		// Semantic consistency: N-normalized re-encoding must match the lockfile's
		// semantic hash. This is the drift channel that ignores equivalently-spelled
		// defaults.
		if forms, err := semanticForm(surface); err == nil {
			if got := hashForms(forms); got != lf.SemanticHash {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error, Code: "E235",
					Message: fmt.Sprintf("import %q: vendored surface semantically drifted from lockfile (semantic hash mismatch); run `pgdesign import update %s` if the framework legitimately changed", alias, alias),
				})
			}
		}
	}

	// Reference drift: the consumer's FKs through this alias must resolve into the
	// vendored surface at the column and type level.
	surfaceTables := map[string]model.Table{}
	for _, t := range surface.Tables {
		surfaceTables[t.Schema+"."+t.Name] = t
	}
	for _, ct := range consumer.Tables {
		for _, fk := range ct.FKs {
			if fk.RefAlias != alias {
				continue
			}
			key := fk.RefSchema + "." + fk.RefTable
			st, ok := surfaceTables[key]
			if !ok {
				diags = append(diags, diagnostic.Diagnostic{
					Severity: diagnostic.Error, Code: "E236", Table: ct.Name,
					Message: fmt.Sprintf("foreign key %q references imported table %s (alias %q) which is not in the vendored surface; run `pgdesign import update %s`", fk.Name, key, alias, alias),
				})
				continue
			}
			surfaceCol := map[string]model.Column{}
			for _, c := range st.Columns {
				surfaceCol[c.Name] = c
			}
			for i, refCol := range fk.RefColumns {
				sc, ok := surfaceCol[refCol]
				if !ok {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Error, Code: "E236", Table: ct.Name, Column: fkLocalColumn(fk, i),
						Message: fmt.Sprintf("foreign key %q references imported column %s.%s which is not in the vendored surface", fk.Name, key, refCol),
					})
					continue
				}
				// Junction type: local FK column type vs surface referenced column
				// type, compared via typeinfo normalization (N for types). A drifted
				// column type is an exact column+FK error.
				local, found := consumerColumn(ct, fkLocalColumn(fk, i))
				if found && !typesMatch(local.PGType, sc.PGType) {
					diags = append(diags, diagnostic.Diagnostic{
						Severity: diagnostic.Error, Code: "E237", Table: ct.Name, Column: fkLocalColumn(fk, i),
						Message: fmt.Sprintf("foreign key %q junction type drift: local column %q is %s but imported %s.%s is %s", fk.Name, fkLocalColumn(fk, i), typeinfo.Reconstruct(local.PGType), key, refCol, typeinfo.Reconstruct(sc.PGType)),
					})
				}
			}
		}
	}

	return diags
}

// typesMatch compares two column types after typeinfo normalization. Domain-
// backed columns compare on their resolved base plus domain identity, so an
// equivalently-spelled type alias does not read as drift.
func typesMatch(a, b typeinfo.Type) bool {
	return a.Equal(b)
}

// fkLocalColumn returns the local FK column at position i, or "" if out of range.
func fkLocalColumn(fk model.FK, i int) string {
	if i < len(fk.Columns) {
		return fk.Columns[i]
	}
	return ""
}

// consumerColumn returns the named column of table t.
func consumerColumn(t model.Table, name string) (model.Column, bool) {
	for _, c := range t.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return model.Column{}, false
}

// ImportAliases returns the sorted list of alias names that have a committed
// lockfile under projectDir. It is the offline check's discovery source — the
// remote is never consulted.
func ImportAliases(projectDir string, declared []string) []string {
	var out []string
	for _, a := range declared {
		if LockfileExists(projectDir, a) {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}
