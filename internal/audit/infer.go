package audit

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/fd"
	"github.com/smm-h/pgdesign/internal/model"
)

// inferFDs infers functional dependencies from PK and UNIQUE constraints,
// then checks if those inferred FDs are covered by the declared dependencies.
// It emits Error diagnostics for undeclared FDs but does not merge them.
func inferFDs(tbl *model.Table) []diagnostic.Diagnostic {
	var diags []diagnostic.Diagnostic

	allCols := columnNames(tbl)

	// Infer from PK
	if len(tbl.PK) > 0 {
		var allNonPK []string
		for _, col := range allCols {
			if !containsStr(tbl.PK, col) {
				allNonPK = append(allNonPK, col)
			}
		}

		if len(allNonPK) > 0 {
			closure := fd.Closure(tbl.PK, tbl.Dependencies)
			var undeclared []string
			for _, col := range allNonPK {
				if !containsStr(closure, col) {
					undeclared = append(undeclared, col)
				}
			}
			if len(undeclared) > 0 {
				diags = append(diags, diagnostic.Diagnostic{
					Code:     "A100",
					Table:    tbl.Name,
					Severity: diagnostic.Error,
					Message: fmt.Sprintf(
						"implied FD {%s} -> {%s} from primary key is not declared in [[dependencies]]",
						formatAttrs(tbl.PK), formatAttrs(undeclared),
					),
				})
			}
		}
	}

	// Infer from UNIQUE constraints
	for _, uniq := range tbl.Uniques {
		// Skip unique constraints with nullable columns (not candidate keys)
		allNotNull := true
		for _, ucol := range uniq.Columns {
			found := false
			for _, col := range tbl.Columns {
				if col.Name == ucol {
					if !col.NotNull {
						allNotNull = false
					}
					found = true
					break
				}
			}
			if !found {
				allNotNull = false
				break
			}
		}
		if !allNotNull {
			continue
		}

		// All other columns = all columns except the unique constraint's columns
		var otherCols []string
		for _, col := range allCols {
			if !containsStr(uniq.Columns, col) {
				otherCols = append(otherCols, col)
			}
		}

		if len(otherCols) == 0 {
			continue
		}

		closure := fd.Closure(uniq.Columns, tbl.Dependencies)
		var missing []string
		for _, col := range otherCols {
			if !containsStr(closure, col) {
				missing = append(missing, col)
			}
		}
		if len(missing) > 0 {
			diags = append(diags, diagnostic.Diagnostic{
				Code:     "A100",
				Table:    tbl.Name,
				Severity: diagnostic.Error,
				Message: fmt.Sprintf(
					"implied FD {%s} -> {%s} from unique constraint is not declared in [[dependencies]]",
					formatAttrs(uniq.Columns), formatAttrs(missing),
				),
			})
		}
	}

	return diags
}
