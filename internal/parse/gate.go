package parse

import (
	"regexp"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/parse/pgschema"
)

// gateDocument runs the strictspec-generated document-shape validator over the
// raw schema bytes BEFORE the native tomledit walk. strictspec is the single
// authority for document shape: well-formedness, closed records (unknown keys),
// base-type conformance, required fields, and the three custom-scalar lexeme
// refinements (identifier / pgtype / sql-expression). Semantic checks — type
// existence, FK/reference resolution, enum-value validity, SQL grammaticality,
// normal form — remain native in model/validate.
//
// It returns the strictspec diagnostics mapped into pgdesign's Diagnostic shape.
// A non-empty result means the document failed the shape gate; callers must NOT
// walk it (a partial RawSchema from a shape-invalid document is meaningless).
func gateDocument(data []byte, file string) []diagnostic.Diagnostic {
	_, ssdiags := pgschema.ValidateBytes(data, "toml")
	if len(ssdiags) == 0 {
		return nil
	}
	out := make([]diagnostic.Diagnostic, 0, len(ssdiags))
	for _, d := range ssdiags {
		table, column := tableColumnFromPath(d.Path)
		out = append(out, diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     d.Code,
			File:     file,
			Table:    table,
			Column:   column,
			Message:  d.Message,
		})
	}
	return out
}

var (
	pathTableRe  = regexp.MustCompile(`tables\["([^"]+)"\]`)
	pathColumnRe = regexp.MustCompile(`columns\["([^"]+)"\]`)
)

// tableColumnFromPath derives the pgdesign Table/Column fields from a strictspec
// rendered path (e.g. `$.tables["users"].columns["email"].type`) where cheap.
// A path that does not name a table/column yields empty strings — the message
// still carries the full path.
func tableColumnFromPath(path string) (table, column string) {
	if m := pathTableRe.FindStringSubmatch(path); m != nil {
		table = m[1]
	}
	if m := pathColumnRe.FindStringSubmatch(path); m != nil {
		column = m[1]
	}
	return table, column
}
