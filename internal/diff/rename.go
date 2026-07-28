package diff

// The rename gate (roadmap 5.9).
//
// Pure generation replaced the drop+add data-loss path's only signal (a
// Dangerous diagnostic nothing consumed) with a diff-time gate that MUST block.
// ResolveRenames runs after Diff and before op lowering. It:
//
//   - resolves DECLARED renames (from the project [renames] directive) into
//     ColumnsRenamed / TablesRenamed, replacing the drop+create pair so the op
//     generator emits ALTER ... RENAME (data-preserving) instead;
//   - HARD-ERRORS on a detected-but-undeclared plausible rename (a removed and
//     an added object that are content-equal except for the name), naming the
//     pair and pointing at [renames];
//   - HARD-ERRORS on an AMBIGUOUS detection (a removed object content-equal to
//     more than one added object), listing all candidates — never auto-pairing;
//   - HARD-ERRORS on a STALE declared entry (the old name is not present as a
//     removal, or the pair is not a content-equal rename).
//
// Detection is PURE: columns use the per-column comparator with the name masked
// (diffColumn ignores the name); tables use the masked table-object content id
// (the encoded table with its name blanked, hashed via objstore). No database.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
)

// renameEscapeHint is appended to every gate error: the two deliberate ways out.
const renameEscapeHint = "declare it under [renames] in pgdesign.toml to emit a data-preserving ALTER ... RENAME, or make the definitions differ (e.g. change a comment) if the drop+create is intentional"

// ResolveRenames applies the rename gate to a computed diff. actual is the base
// model (the reconstructed head, or the introspected live schema); it may be nil
// at genesis, where no removals and therefore no renames are possible.
// actualIntrospected suppresses class-aware column comparison against an
// introspected base (matching diffColumn's own contract).
func ResolveRenames(d *SchemaDiff, desired, actual *model.Schema, spec RenameSpec, actualIntrospected bool) error {
	if actual == nil {
		if len(spec.Tables) > 0 || len(spec.Columns) > 0 {
			return fmt.Errorf("rename gate: [renames] declared %d table and %d column rename(s) but there is no base schema (genesis) to rename from; remove the [renames] entries", len(spec.Tables), len(spec.Columns))
		}
		return nil
	}
	var problems []string
	if err := resolveColumnRenames(d, desired, actual, spec, actualIntrospected); err != nil {
		problems = append(problems, err.Error())
	}
	if err := resolveTableRenames(d, desired, actual, spec); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// findTableByKey returns the table in s whose tableKey matches key, or nil.
func findTableByKey(s *model.Schema, key string) *model.Table {
	if s == nil {
		return nil
	}
	for i := range s.Tables {
		if tableKey(&s.Tables[i]) == key {
			return &s.Tables[i]
		}
	}
	return nil
}

// findColumn returns the column named n in t, or nil.
func findColumn(t *model.Table, n string) *model.Column {
	if t == nil {
		return nil
	}
	for i := range t.Columns {
		if t.Columns[i].Name == n {
			return &t.Columns[i]
		}
	}
	return nil
}

// resolveColumnRenames resolves declared column renames and gates undeclared /
// ambiguous ones, per matched table.
func resolveColumnRenames(d *SchemaDiff, desired, actual *model.Schema, spec RenameSpec, introspected bool) error {
	declaredByTable := map[string][]ColumnRenameSpec{}
	for _, c := range spec.Columns {
		declaredByTable[c.Table] = append(declaredByTable[c.Table], c)
	}
	consumedDeclared := map[string]bool{} // "table\x00from" keys consumed

	var problems []string
	for ti := range d.TablesChanged {
		td := &d.TablesChanged[ti]
		at := findTableByKey(actual, td.Name)
		if at == nil {
			continue
		}

		// plausible[removedName] = added indices content-equal-except-name.
		plausible := map[string][]int{}
		for _, rName := range td.ColumnsRemoved {
			rCol := findColumn(at, rName)
			if rCol == nil {
				continue
			}
			for ai := range td.ColumnsAdded {
				aCol := td.ColumnsAdded[ai]
				if diffColumn(&aCol, rCol, introspected) == nil {
					plausible[rName] = append(plausible[rName], ai)
				}
			}
		}

		consumedRemoved := map[string]bool{}
		consumedAdded := map[int]bool{}
		var renamed []RenamePair

		// 1. Declared renames for this table.
		for _, dec := range declaredByTable[td.Name] {
			addedIdx := -1
			for ai := range td.ColumnsAdded {
				if td.ColumnsAdded[ai].Name == dec.To {
					addedIdx = ai
					break
				}
			}
			removedPresent := false
			for _, rn := range td.ColumnsRemoved {
				if rn == dec.From {
					removedPresent = true
					break
				}
			}
			if !removedPresent || addedIdx == -1 {
				problems = append(problems, fmt.Sprintf("stale [renames] column entry %s.%s -> %s: the old column is not being removed and/or the new column is not being added", td.Name, dec.From, dec.To))
				continue
			}
			isPlausible := false
			for _, ai := range plausible[dec.From] {
				if ai == addedIdx {
					isPlausible = true
					break
				}
			}
			if !isPlausible {
				problems = append(problems, fmt.Sprintf("invalid [renames] column entry %s.%s -> %s: the two columns differ beyond their name; a rename cannot also change the column definition (split into a rename and a separate alter)", td.Name, dec.From, dec.To))
				continue
			}
			renamed = append(renamed, RenamePair{From: dec.From, To: dec.To})
			consumedRemoved[dec.From] = true
			consumedAdded[addedIdx] = true
			consumedDeclared[td.Name+"\x00"+dec.From] = true
		}

		// 2. Gate remaining plausible pairs (undeclared / ambiguous).
		var removedNames []string
		for rn := range plausible {
			removedNames = append(removedNames, rn)
		}
		sort.Strings(removedNames)
		for _, rn := range removedNames {
			if consumedRemoved[rn] {
				continue
			}
			var cands []string
			for _, ai := range plausible[rn] {
				if consumedAdded[ai] {
					continue
				}
				cands = append(cands, td.ColumnsAdded[ai].Name)
			}
			if len(cands) == 0 {
				continue
			}
			sort.Strings(cands)
			if len(cands) == 1 {
				problems = append(problems, fmt.Sprintf("undeclared plausible column rename in table %s: %q is being dropped and %q added with an identical definition — %s", td.Name, rn, cands[0], renameEscapeHint))
			} else {
				problems = append(problems, fmt.Sprintf("ambiguous column rename in table %s: dropped column %q matches %d added columns %v with identical definitions — cannot auto-pair; %s", td.Name, rn, len(cands), cands, renameEscapeHint))
			}
		}

		// 3. Mutate the TableDiff: strip renamed columns from add/remove, record.
		if len(renamed) > 0 {
			td.ColumnsRemoved = filterStrings(td.ColumnsRemoved, consumedRemoved)
			td.ColumnsAdded = filterColumns(td.ColumnsAdded, consumedAdded)
			td.ColumnsRenamed = append(td.ColumnsRenamed, renamed...)
		}
	}

	// Stale declared entries: any column rename not consumed.
	for _, c := range spec.Columns {
		if !consumedDeclared[c.Table+"\x00"+c.From] {
			problems = append(problems, fmt.Sprintf("stale [renames] column entry %s.%s -> %s: no such removal was detected (the old column is not being dropped)", c.Table, c.From, c.To))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(dedupeSorted(problems), "\n"))
	}
	return nil
}

// maskedTableID returns the content id of a table with its NAME blanked, so two
// tables identical except for their name share an id. Schema is retained (a
// rename is within a schema). Returns "" if encoding fails (never pairs).
func maskedTableID(t *model.Table) string {
	masked := *t
	masked.Name = ""
	b, err := enc.EncodeTable(masked)
	if err != nil {
		return ""
	}
	return objstore.ID(b)
}

// resolveTableRenames resolves declared table renames and gates undeclared /
// ambiguous ones at the schema level.
func resolveTableRenames(d *SchemaDiff, desired, actual *model.Schema, spec RenameSpec) error {
	// masked ids for removed (actual) and added (desired) tables.
	removedID := map[string]string{}
	for _, rk := range d.TablesRemoved {
		if t := findTableByKey(actual, rk); t != nil {
			removedID[rk] = maskedTableID(t)
		}
	}
	addedID := map[string]string{}
	for _, ak := range d.TablesAdded {
		if t := findTableByKey(desired, ak); t != nil {
			addedID[ak] = maskedTableID(t)
		}
	}

	// plausible[removedKey] = added keys with equal (non-empty) masked id.
	plausible := map[string][]string{}
	for rk, rid := range removedID {
		if rid == "" {
			continue
		}
		for ak, aid := range addedID {
			if aid != "" && aid == rid {
				plausible[rk] = append(plausible[rk], ak)
			}
		}
		sort.Strings(plausible[rk])
	}

	consumedRemoved := map[string]bool{}
	consumedAdded := map[string]bool{}
	consumedDeclared := map[string]bool{}
	var renamed []RenamePair
	var problems []string

	// 1. Declared table renames.
	for _, dec := range spec.Tables {
		_, removedPresent := removedID[dec.From]
		_, addedPresent := addedID[dec.To]
		if !removedPresent || !addedPresent {
			problems = append(problems, fmt.Sprintf("stale [renames] table entry %s -> %s: the old table is not being dropped and/or the new table is not being created", dec.From, dec.To))
			continue
		}
		if removedID[dec.From] == "" || removedID[dec.From] != addedID[dec.To] {
			problems = append(problems, fmt.Sprintf("invalid [renames] table entry %s -> %s: the two tables differ beyond their name; a rename cannot also change the table definition", dec.From, dec.To))
			continue
		}
		renamed = append(renamed, RenamePair{From: dec.From, To: dec.To})
		consumedRemoved[dec.From] = true
		consumedAdded[dec.To] = true
		consumedDeclared[dec.From] = true
	}

	// 2. Gate remaining plausible pairs.
	var removedKeys []string
	for rk := range plausible {
		removedKeys = append(removedKeys, rk)
	}
	sort.Strings(removedKeys)
	for _, rk := range removedKeys {
		if consumedRemoved[rk] {
			continue
		}
		var cands []string
		for _, ak := range plausible[rk] {
			if consumedAdded[ak] {
				continue
			}
			cands = append(cands, ak)
		}
		if len(cands) == 0 {
			continue
		}
		sort.Strings(cands)
		if len(cands) == 1 {
			problems = append(problems, fmt.Sprintf("undeclared plausible table rename: %q is being dropped and %q created with an identical definition — %s", rk, cands[0], renameEscapeHint))
		} else {
			problems = append(problems, fmt.Sprintf("ambiguous table rename: dropped table %q matches %d created tables %v with identical definitions — cannot auto-pair; %s", rk, len(cands), cands, renameEscapeHint))
		}
	}

	// 3. Mutate the SchemaDiff.
	if len(renamed) > 0 {
		d.TablesRemoved = filterStringSet(d.TablesRemoved, consumedRemoved)
		d.TablesAdded = filterStringSet(d.TablesAdded, consumedAdded)
		d.TablesRenamed = append(d.TablesRenamed, renamed...)
	}

	// Stale declared entries.
	for _, dec := range spec.Tables {
		if !consumedDeclared[dec.From] && !containsProblem(problems, dec.From) {
			problems = append(problems, fmt.Sprintf("stale [renames] table entry %s -> %s: no such table drop was detected", dec.From, dec.To))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(dedupeSorted(problems), "\n"))
	}
	return nil
}

// filterStrings drops names present in the drop set (used for ColumnsRemoved).
func filterStrings(in []string, drop map[string]bool) []string {
	var out []string
	for _, s := range in {
		if !drop[s] {
			out = append(out, s)
		}
	}
	return out
}

// filterStringSet drops names present in the drop set (used for Tables*).
func filterStringSet(in []string, drop map[string]bool) []string {
	return filterStrings(in, drop)
}

// filterColumns drops columns at indices present in the drop set.
func filterColumns(in []model.Column, dropIdx map[int]bool) []model.Column {
	var out []model.Column
	for i, c := range in {
		if !dropIdx[i] {
			out = append(out, c)
		}
	}
	return out
}

// containsProblem reports whether any problem string mentions sub (avoids a
// duplicate stale message for an entry already flagged invalid).
func containsProblem(problems []string, sub string) bool {
	for _, p := range problems {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}
