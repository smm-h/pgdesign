package imports

import (
	"fmt"
	"sort"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

// CheckRequirements enforces the lockfile's carried REQUIREMENTS against the
// consumer's own declarations (roadmap 7.3): the consumer must re-declare every
// extension the imported surface requires, and its pg_version must be >= the
// imported floor. The lockfile carries these (7.2 InferRequirements); 7.3 turns
// them into hard errors so an offline build that would fail at apply time — a
// missing extension type, or a version-gated feature the consumer's target cannot
// run — fails loudly at build instead.
//
//   - E238... wait, those are live codes; drift codes E230-E237 are 7.1/7.2.
//     Requirement violations use E241 (missing extension) and E242 (pg_version
//     below floor), naming the requiring alias.
//
// consumer supplies the project's own declared Extensions and PGVersion. aliases
// is the set of declared import aliases; only those with a committed lockfile are
// consulted (the remote is never touched).
func CheckRequirements(projectDir string, aliases []string, consumer *model.Schema) diagnostic.Diagnostics {
	var diags diagnostic.Diagnostics

	declaredExt := make(map[string]bool, len(consumer.Extensions))
	for _, e := range consumer.Extensions {
		declaredExt[e] = true
	}

	for _, alias := range ImportAliases(projectDir, aliases) {
		lf, err := ReadLockfile(projectDir, alias)
		if err != nil {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error, Code: "E241",
				Message: fmt.Sprintf("import %q: cannot read lockfile to enforce requirements: %v", alias, err),
			})
			continue
		}

		missing := make([]string, 0)
		for _, ext := range lf.Extensions {
			if !declaredExt[ext] {
				missing = append(missing, ext)
			}
		}
		sort.Strings(missing)
		for _, ext := range missing {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error, Code: "E241",
				Message: fmt.Sprintf("import %q requires extension %q, which this project does not declare; add it to [meta].extensions (the imported surface uses it)", alias, ext),
			})
		}

		// pg_version floor: 0 means the framework declared no floor.
		if lf.PGVersion > 0 && consumer.PGVersion > 0 && consumer.PGVersion < lf.PGVersion {
			diags = append(diags, diagnostic.Diagnostic{
				Severity: diagnostic.Error, Code: "E242",
				Message: fmt.Sprintf("import %q requires PostgreSQL >= %d but this project targets %d; raise [meta].version (or [database].pg_version)", alias, lf.PGVersion, consumer.PGVersion),
			})
		}
	}
	return diags
}
