package generate

import (
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/typeinfo"
)

// columnPresentation is the shared, format-neutral derivation of a single
// column's displayable attributes. It is the ONE place the doc (Markdown) and
// d2 (diagram) renderers derive column facts from, so neither re-implements the
// logic (roadmap 9.2: "doc already derives all of this — no second
// derivation"). generateDoc consumes Type/Nullable/Default/Comment for its
// column table; GenerateD2 additionally consumes the membership markers
// (IsPK/IsFK/IsUnique/Indexed) for its sql_table constraint annotations.
type columnPresentation struct {
	Name     string
	Type     string // typeinfo.Reconstruct(col.PGType)
	Nullable bool   // !col.NotNull
	Default  string // DefaultExpr, else *Default, else ""
	Comment  string
	IsPK     bool // participates in the primary key
	IsFK     bool // participates in a foreign key
	IsUnique bool // participates in a UNIQUE constraint or a unique index
	Indexed  bool // participates in any index
}

// deriveColumnPresentations derives the presentation view of every column in a
// table, in declaration order. This is the single shared derivation consumed by
// both renderers.
func deriveColumnPresentations(t *model.Table) []columnPresentation {
	pk := make(map[string]bool, len(t.PK))
	for _, c := range t.PK {
		pk[c] = true
	}

	fk := make(map[string]bool)
	for _, f := range t.FKs {
		for _, c := range f.Columns {
			fk[c] = true
		}
	}

	unique := make(map[string]bool)
	for _, u := range t.Uniques {
		for _, c := range u.Columns {
			unique[c] = true
		}
	}

	indexed := make(map[string]bool)
	for _, idx := range t.Indexes {
		for _, c := range idx.Columns {
			indexed[c] = true
			if idx.Unique {
				unique[c] = true
			}
		}
	}

	out := make([]columnPresentation, len(t.Columns))
	for i, col := range t.Columns {
		out[i] = columnPresentation{
			Name:     col.Name,
			Type:     typeinfo.Reconstruct(col.PGType),
			Nullable: !col.NotNull,
			Default:  columnDefault(col),
			Comment:  col.Comment,
			IsPK:     pk[col.Name],
			IsFK:     fk[col.Name],
			IsUnique: unique[col.Name],
			Indexed:  indexed[col.Name],
		}
	}
	return out
}

// columnDefault extracts a column's default as a display string: the raw
// DefaultExpr when present, else the quoted-literal *Default, else empty.
func columnDefault(col model.Column) string {
	if col.DefaultExpr != "" {
		return col.DefaultExpr
	}
	if col.Default != nil {
		return *col.Default
	}
	return ""
}
