// Package generate transforms a resolved model.Schema into PostgreSQL DDL output.
package generate

import (
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/sql"
)

// Options controls the DDL output behavior.
type Options struct {
	Idempotent      bool
	IncludeComments bool
	Format          string // "sql", "json", "d2", "svg"
	PGVersion       int
}

// Generate produces DDL output for the given schema according to opts.
// Currently only Format="sql" is implemented; other formats return a placeholder.
func Generate(schema *model.Schema, opts Options) string {
	switch strings.ToLower(opts.Format) {
	case "sql", "":
		return generateSQL(schema, opts)
	case "d2":
		return GenerateD2(schema)
	case "svg":
		d2Source := GenerateD2(schema)
		svg, err := RenderSVG(d2Source)
		if err != nil {
			return "error: " + err.Error()
		}
		return string(svg)
	default:
		return "not implemented"
	}
}

func generateSQL(schema *model.Schema, opts Options) string {
	var sections []string

	// 1. CREATE SCHEMA
	if schema.Name != "" {
		sections = append(sections, sql.CreateSchema(schema.Name, opts.Idempotent))
	}

	// 2. CREATE EXTENSION
	if len(schema.Extensions) > 0 {
		var extStmts []string
		for _, ext := range schema.Extensions {
			extStmts = append(extStmts, sql.CreateExtension(ext, opts.Idempotent))
		}
		sections = append(sections, strings.Join(extStmts, "\n"))
	}

	// 3. CREATE TYPE ... AS ENUM
	if len(schema.Enums) > 0 {
		var enumStmts []string
		for _, e := range schema.Enums {
			enumStmts = append(enumStmts, sql.CreateEnum(e.Schema, e.Name, e.Values, opts.Idempotent))
		}
		sections = append(sections, strings.Join(enumStmts, "\n"))
	}

	tables := schema.TableOrder()

	// 4. CREATE TABLE
	if len(tables) > 0 {
		var tableStmts []string
		for i := range tables {
			tableStmts = append(tableStmts, sql.CreateTable(&tables[i], schema.Name, opts.Idempotent))
		}
		sections = append(sections, strings.Join(tableStmts, "\n\n"))
	}

	// 5. ALTER TABLE ADD CONSTRAINT ... FOREIGN KEY
	var fkStmts []string
	for i := range tables {
		t := &tables[i]
		fks := sortedFKs(t.FKs)
		for _, fk := range fks {
			fkCopy := fk
			fkStmts = append(fkStmts, sql.AlterTableAddFK(schema.Name, t, &fkCopy))
		}
	}
	if len(fkStmts) > 0 {
		sections = append(sections, strings.Join(fkStmts, "\n"))
	}

	// 6. ALTER TABLE ADD CONSTRAINT ... UNIQUE
	var uqStmts []string
	for i := range tables {
		t := &tables[i]
		uqs := sortedUniques(t.Uniques)
		for _, uq := range uqs {
			uqCopy := uq
			uqStmts = append(uqStmts, sql.AlterTableAddUnique(schema.Name, t.Name, &uqCopy))
		}
	}
	if len(uqStmts) > 0 {
		sections = append(sections, strings.Join(uqStmts, "\n"))
	}

	// 7. ALTER TABLE ADD CONSTRAINT ... CHECK
	var ckStmts []string
	for i := range tables {
		t := &tables[i]
		cks := sortedChecks(t.Checks)
		for _, ck := range cks {
			ckCopy := ck
			ckStmts = append(ckStmts, sql.AlterTableAddCheck(schema.Name, t.Name, &ckCopy))
		}
	}
	if len(ckStmts) > 0 {
		sections = append(sections, strings.Join(ckStmts, "\n"))
	}

	// 8. CREATE INDEX (explicit + auto-FK)
	var idxStmts []string
	for i := range tables {
		t := &tables[i]
		idxs := sortedIndexes(t.Indexes)
		for _, idx := range idxs {
			idxCopy := idx
			idxStmts = append(idxStmts, sql.CreateIndex(schema.Name, &idxCopy, t.Name, opts.Idempotent))
		}
	}
	if len(idxStmts) > 0 {
		sections = append(sections, strings.Join(idxStmts, "\n"))
	}

	// 9. COMMENT ON TABLE + COMMENT ON COLUMN
	if opts.IncludeComments {
		var commentStmts []string
		for i := range tables {
			t := &tables[i]
			if t.Comment != "" {
				qualified := sql.QualifiedName(schema.Name, t.Name)
				commentStmts = append(commentStmts, sql.CommentOn("TABLE", qualified, t.Comment))
			}
			for _, col := range t.Columns {
				if col.Comment != "" {
					qualified := sql.QualifiedName(schema.Name, t.Name) + "." + sql.QuoteIdent(col.Name)
					commentStmts = append(commentStmts, sql.CommentOn("COLUMN", qualified, col.Comment))
				}
			}
		}
		if len(commentStmts) > 0 {
			sections = append(sections, strings.Join(commentStmts, "\n"))
		}
	}

	// 10. ALTER TABLE OWNER TO
	var ownerStmts []string
	for i := range tables {
		t := &tables[i]
		if t.Owner != "" {
			ownerStmts = append(ownerStmts, sql.AlterTableOwner(schema.Name, t.Name, t.Owner))
		}
	}
	if len(ownerStmts) > 0 {
		sections = append(sections, strings.Join(ownerStmts, "\n"))
	}

	return strings.Join(sections, "\n\n") + "\n"
}

// sortedFKs returns FKs sorted alphabetically by name.
func sortedFKs(fks []model.FK) []model.FK {
	result := make([]model.FK, len(fks))
	copy(result, fks)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// sortedUniques returns unique constraints sorted alphabetically by name.
func sortedUniques(uqs []model.UniqueConstraint) []model.UniqueConstraint {
	result := make([]model.UniqueConstraint, len(uqs))
	copy(result, uqs)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// sortedChecks returns check constraints sorted alphabetically by name.
func sortedChecks(cks []model.CheckConstraint) []model.CheckConstraint {
	result := make([]model.CheckConstraint, len(cks))
	copy(result, cks)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// sortedIndexes returns indexes sorted alphabetically by name.
func sortedIndexes(idxs []model.Index) []model.Index {
	result := make([]model.Index, len(idxs))
	copy(result, idxs)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}
