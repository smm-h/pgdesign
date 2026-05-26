// Package format implements canonical TOML formatting for pgdesign schema files.
// It parses the input via internal/parse, determines canonical ordering, then
// builds a fresh TOML document using go-toml-edit's API. This approach loses
// comments (v1 limitation); a future version should do direct AST node
// reordering to preserve them.
package format

import (
	"fmt"
	"sort"

	"github.com/smm-h/pgdesign/internal/parse"

	tomledit "github.com/smm-h/go-toml-edit"
)

// Config controls formatting behavior.
type Config struct {
	TableOrder  string // "dependency" or "alphabetical" (default: "dependency")
	ColumnOrder string // "pk_fk_alpha", "alphabetical", "fk_last", "preserve" (default: "pk_fk_alpha")
}

// DefaultConfig returns a Config with default values.
func DefaultConfig() *Config {
	return &Config{
		TableOrder:  "dependency",
		ColumnOrder: "pk_fk_alpha",
	}
}

// Format parses input TOML bytes and returns the canonically formatted output.
func Format(input []byte, config *Config) ([]byte, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// Parse into RawSchema for structural analysis.
	raw, diags := parse.Bytes(input)
	if raw == nil {
		if len(diags) > 0 {
			return nil, fmt.Errorf("%s", diags[0].Message)
		}
		return nil, fmt.Errorf("failed to parse TOML")
	}
	_ = diags

	// Determine canonical table order.
	tableOrder := orderTables(raw, config.TableOrder)

	// Build a new document in canonical order.
	out := buildCanonical(raw, tableOrder, config)
	return out.Format(), nil
}

// orderTables returns table names in canonical order.
func orderTables(raw *parse.RawSchema, mode string) []string {
	switch mode {
	case "alphabetical":
		names := make([]string, len(raw.Tables))
		for i, t := range raw.Tables {
			names[i] = t.Name
		}
		sort.Strings(names)
		return names
	default: // "dependency"
		return topoSortTables(raw.Tables)
	}
}

// topoSortTables performs a topological sort on raw tables using FK refs.
// FK targets come before FK sources. Ties and cycle members are alphabetical.
func topoSortTables(tables []parse.RawTable) []string {
	tableSet := make(map[string]bool, len(tables))
	for _, t := range tables {
		tableSet[t.Name] = true
	}

	// Build dependency graph: dependsOn[A] = set of tables A references via FKs.
	dependsOn := make(map[string]map[string]bool, len(tables))
	for _, t := range tables {
		deps := make(map[string]bool)
		for _, fk := range t.FKs {
			if fk.RefTable != t.Name && tableSet[fk.RefTable] {
				deps[fk.RefTable] = true
			}
		}
		dependsOn[t.Name] = deps
	}

	// Kahn's algorithm with alphabetical tie-breaking.
	inDegree := make(map[string]int, len(tables))
	for _, t := range tables {
		inDegree[t.Name] = len(dependsOn[t.Name])
	}

	// Collect zero-degree nodes, sorted alphabetically.
	var queue []string
	for _, t := range tables {
		if inDegree[t.Name] == 0 {
			queue = append(queue, t.Name)
		}
	}
	sort.Strings(queue)

	visited := make(map[string]bool, len(tables))
	var result []string

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if visited[name] {
			continue
		}
		visited[name] = true
		result = append(result, name)

		// For each table that depends on this one, decrement its in-degree.
		var newReady []string
		for _, t := range tables {
			if visited[t.Name] {
				continue
			}
			if dependsOn[t.Name][name] {
				inDegree[t.Name]--
				if inDegree[t.Name] == 0 {
					newReady = append(newReady, t.Name)
				}
			}
		}
		sort.Strings(newReady)
		queue = append(queue, newReady...)
	}

	// Remaining nodes are in cycles -- add them alphabetically.
	if len(result) < len(tables) {
		var remaining []string
		for _, t := range tables {
			if !visited[t.Name] {
				remaining = append(remaining, t.Name)
			}
		}
		sort.Strings(remaining)
		result = append(result, remaining...)
	}

	return result
}

// buildCanonical constructs a new TOML document in canonical order.
func buildCanonical(raw *parse.RawSchema, tableOrder []string, config *Config) *tomledit.DocumentNode {
	doc := newDoc()

	// 1. [meta] section
	writeMeta(doc, raw)

	// 2. [types.*] sections, alphabetically by name
	writeTypes(doc, raw)

	// 3. [tables.*] sections in canonical order
	tableByName := make(map[string]*parse.RawTable, len(raw.Tables))
	for i := range raw.Tables {
		tableByName[raw.Tables[i].Name] = &raw.Tables[i]
	}
	for _, name := range tableOrder {
		t := tableByName[name]
		if t == nil {
			continue
		}
		writeTable(doc, t, config)
	}

	return doc
}

// newDoc creates a fresh empty DocumentNode.
func newDoc() *tomledit.DocumentNode {
	doc, _ := tomledit.Parse([]byte(""))
	return doc
}

// writeMeta writes the [meta] section to the document.
func writeMeta(doc *tomledit.DocumentNode, raw *parse.RawSchema) {
	if raw.Meta.Version == 0 && raw.Meta.Schema == "" && len(raw.Meta.Extensions) == 0 {
		return
	}
	_ = doc.NewTable("meta")
	if raw.Meta.Version != 0 {
		_ = doc.SetCreate("meta.version", raw.Meta.Version)
	}
	if raw.Meta.Schema != "" {
		_ = doc.SetCreate("meta.schema", raw.Meta.Schema)
	}
	if len(raw.Meta.Extensions) > 0 {
		_ = doc.SetCreate("meta.extensions", raw.Meta.Extensions)
	}
}

// writeTypes writes all [types.*] sections alphabetically.
func writeTypes(doc *tomledit.DocumentNode, raw *parse.RawSchema) {
	if len(raw.Types) == 0 {
		return
	}
	sorted := make([]parse.RawType, len(raw.Types))
	copy(sorted, raw.Types)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	for _, rt := range sorted {
		path := "types." + rt.Name
		_ = doc.NewTable(path)
		if rt.Kind != "" {
			_ = doc.SetCreate(path+".kind", rt.Kind)
		}
		if rt.BaseType != "" {
			_ = doc.SetCreate(path+".base_type", rt.BaseType)
		}
		if len(rt.Values) > 0 {
			_ = doc.SetCreate(path+".values", rt.Values)
		}
		if rt.NotNull != nil {
			_ = doc.SetCreate(path+".not_null", *rt.NotNull)
		}
		if rt.Default != nil {
			_ = doc.SetCreate(path+".default", *rt.Default)
		}
		if rt.DefaultExpr != nil {
			_ = doc.SetCreate(path+".default_expr", *rt.DefaultExpr)
		}
		if rt.Check != nil {
			_ = doc.SetCreate(path+".check", *rt.Check)
		}
		if rt.Unique != nil {
			_ = doc.SetCreate(path+".unique", *rt.Unique)
		}
		if rt.Comment != nil {
			_ = doc.SetCreate(path+".comment", *rt.Comment)
		}
	}
}

// writeTable writes a single table with all sub-sections in canonical order:
// comment, pk, columns, fks, indexes, unique, checks, partitioning,
// maintenance, dependencies
func writeTable(doc *tomledit.DocumentNode, t *parse.RawTable, config *Config) {
	tpath := "tables." + t.Name
	_ = doc.NewTable(tpath)

	// comment
	if t.Comment != nil {
		_ = doc.SetCreate(tpath+".comment", *t.Comment)
	}

	// pk
	if len(t.PK) > 0 {
		_ = doc.SetCreate(tpath+".pk", t.PK)
	}

	// columns in canonical order
	columns := orderColumns(t, config.ColumnOrder)
	for _, col := range columns {
		cpath := tpath + ".columns." + col.Name
		_ = doc.NewTable(cpath)
		if col.Type != "" {
			_ = doc.SetCreate(cpath+".type", col.Type)
		}
		if col.Nullable != nil {
			_ = doc.SetCreate(cpath+".nullable", *col.Nullable)
		}
		if col.Default != nil {
			_ = doc.SetCreate(cpath+".default", *col.Default)
		}
		if col.DefaultExpr != nil {
			_ = doc.SetCreate(cpath+".default_expr", *col.DefaultExpr)
		}
		if col.Generated != nil {
			_ = doc.SetCreate(cpath+".generated", *col.Generated)
		}
		if col.Stored != nil {
			_ = doc.SetCreate(cpath+".stored", *col.Stored)
		}
		if col.Comment != nil {
			_ = doc.SetCreate(cpath+".comment", *col.Comment)
		}
	}

	// fks alphabetically
	writeMapSorted(doc, tpath, "fks", t.FKs, func(name string, fk parse.RawFK) {
		fkpath := tpath + ".fks." + name
		_ = doc.NewTable(fkpath)
		if len(fk.Columns) > 0 {
			_ = doc.SetCreate(fkpath+".columns", fk.Columns)
		}
		if fk.RefTable != "" {
			_ = doc.SetCreate(fkpath+".ref_table", fk.RefTable)
		}
		if len(fk.RefColumns) > 0 {
			_ = doc.SetCreate(fkpath+".ref_columns", fk.RefColumns)
		}
		if fk.OnDelete != "" {
			_ = doc.SetCreate(fkpath+".on_delete", fk.OnDelete)
		}
	})

	// indexes alphabetically
	writeMapSorted(doc, tpath, "indexes", t.Indexes, func(name string, idx parse.RawIndex) {
		ipath := tpath + ".indexes." + name
		_ = doc.NewTable(ipath)
		if len(idx.Columns) > 0 {
			_ = doc.SetCreate(ipath+".columns", idx.Columns)
		}
		if idx.Method != nil {
			_ = doc.SetCreate(ipath+".method", *idx.Method)
		}
		if idx.Opclass != nil {
			_ = doc.SetCreate(ipath+".opclass", *idx.Opclass)
		} else if idx.OpclassMap != nil {
			// Per-column opclass map: check if all values are the same
			// for compact string form, otherwise write as inline table.
			allSame := true
			var singleVal string
			for _, v := range idx.OpclassMap {
				if singleVal == "" {
					singleVal = v
				} else if v != singleVal {
					allSame = false
					break
				}
			}
			if allSame && singleVal != "" {
				_ = doc.SetCreate(ipath+".opclass", singleVal)
			} else {
				// Write per-column entries as dotted keys under opclass.
				for col, oc := range idx.OpclassMap {
					_ = doc.SetCreate(ipath+".opclass."+col, oc)
				}
			}
		}
		if idx.Where != nil {
			_ = doc.SetCreate(ipath+".where", *idx.Where)
		}
		if len(idx.Include) > 0 {
			_ = doc.SetCreate(ipath+".include", idx.Include)
		}
		if idx.Unique != nil {
			_ = doc.SetCreate(ipath+".unique", *idx.Unique)
		}
	})

	// unique constraints alphabetically
	writeMapSorted(doc, tpath, "unique", t.Uniques, func(name string, uq parse.RawUnique) {
		upath := tpath + ".unique." + name
		_ = doc.NewTable(upath)
		if len(uq.Columns) > 0 {
			_ = doc.SetCreate(upath+".columns", uq.Columns)
		}
	})

	// checks alphabetically
	writeMapSorted(doc, tpath, "checks", t.Checks, func(name string, chk parse.RawCheck) {
		cpath := tpath + ".checks." + name
		_ = doc.NewTable(cpath)
		if chk.Expr != "" {
			_ = doc.SetCreate(cpath+".expr", chk.Expr)
		}
	})

	// partitioning
	if t.Partitioning != nil {
		writePartitioning(doc, tpath, t.Partitioning)
	}

	// maintenance
	if t.Maintenance != nil {
		writeMaintenance(doc, tpath, t.Maintenance)
	}

	// dependencies (preserved in declaration order per spec)
	for _, dep := range t.Dependencies {
		_ = doc.NewArrayTable(tpath + ".dependencies")
		// Find the last array table entry and set values on it.
		// Since NewArrayTable appends, we use SetCreate on the last entry.
		idx := countArrayTableEntries(doc, tpath+".dependencies") - 1
		depPath := tpath + ".dependencies[" + itoa(idx) + "]"
		if len(dep.Determinant) > 0 {
			_ = doc.SetCreate(depPath+".determinant", dep.Determinant)
		}
		if len(dep.Dependent) > 0 {
			_ = doc.SetCreate(depPath+".dependent", dep.Dependent)
		}
	}
}

// orderColumns returns columns in the order specified by the column order config.
func orderColumns(t *parse.RawTable, mode string) []parse.RawColumn {
	if len(t.Columns) == 0 {
		return nil
	}

	switch mode {
	case "alphabetical":
		cols := make([]parse.RawColumn, len(t.Columns))
		copy(cols, t.Columns)
		sort.Slice(cols, func(i, j int) bool {
			return cols[i].Name < cols[j].Name
		})
		return cols

	case "preserve":
		return t.Columns

	case "fk_last":
		return orderFKLast(t)

	default: // "pk_fk_alpha"
		return orderPKFKAlpha(t)
	}
}

// orderPKFKAlpha orders: PK columns first (in PK declaration order), then FK
// columns alphabetically, then remaining columns alphabetically.
func orderPKFKAlpha(t *parse.RawTable) []parse.RawColumn {
	pkSet := make(map[string]bool, len(t.PK))
	for _, pk := range t.PK {
		pkSet[pk] = true
	}
	fkSet := buildFKColumnSet(t)

	var pkCols, fkCols, restCols []parse.RawColumn
	colByName := make(map[string]parse.RawColumn, len(t.Columns))
	for _, col := range t.Columns {
		colByName[col.Name] = col
	}

	// PK columns in PK declaration order.
	for _, pkName := range t.PK {
		if col, ok := colByName[pkName]; ok {
			pkCols = append(pkCols, col)
		}
	}

	// FK columns (not already in PK), alphabetically.
	for _, col := range t.Columns {
		if pkSet[col.Name] {
			continue
		}
		if fkSet[col.Name] {
			fkCols = append(fkCols, col)
		}
	}
	sort.Slice(fkCols, func(i, j int) bool {
		return fkCols[i].Name < fkCols[j].Name
	})

	// Remaining columns alphabetically.
	for _, col := range t.Columns {
		if pkSet[col.Name] || fkSet[col.Name] {
			continue
		}
		restCols = append(restCols, col)
	}
	sort.Slice(restCols, func(i, j int) bool {
		return restCols[i].Name < restCols[j].Name
	})

	result := make([]parse.RawColumn, 0, len(t.Columns))
	result = append(result, pkCols...)
	result = append(result, fkCols...)
	result = append(result, restCols...)
	return result
}

// orderFKLast orders: PK first, then non-FK non-PK alphabetically, then FK
// columns last alphabetically.
func orderFKLast(t *parse.RawTable) []parse.RawColumn {
	pkSet := make(map[string]bool, len(t.PK))
	for _, pk := range t.PK {
		pkSet[pk] = true
	}
	fkSet := buildFKColumnSet(t)

	var pkCols, midCols, fkCols []parse.RawColumn
	colByName := make(map[string]parse.RawColumn, len(t.Columns))
	for _, col := range t.Columns {
		colByName[col.Name] = col
	}

	for _, pkName := range t.PK {
		if col, ok := colByName[pkName]; ok {
			pkCols = append(pkCols, col)
		}
	}

	for _, col := range t.Columns {
		if pkSet[col.Name] {
			continue
		}
		if fkSet[col.Name] {
			fkCols = append(fkCols, col)
		} else {
			midCols = append(midCols, col)
		}
	}
	sort.Slice(midCols, func(i, j int) bool {
		return midCols[i].Name < midCols[j].Name
	})
	sort.Slice(fkCols, func(i, j int) bool {
		return fkCols[i].Name < fkCols[j].Name
	})

	result := make([]parse.RawColumn, 0, len(t.Columns))
	result = append(result, pkCols...)
	result = append(result, midCols...)
	result = append(result, fkCols...)
	return result
}

// buildFKColumnSet returns the set of column names that appear in any FK's
// columns list for the given table.
func buildFKColumnSet(t *parse.RawTable) map[string]bool {
	fkSet := make(map[string]bool)
	for _, fk := range t.FKs {
		for _, col := range fk.Columns {
			fkSet[col] = true
		}
	}
	return fkSet
}

// writePartitioning writes [tables.<name>.partitioning] and its sub-entries.
func writePartitioning(doc *tomledit.DocumentNode, tpath string, part *parse.RawPartitioning) {
	ppath := tpath + ".partitioning"
	_ = doc.NewTable(ppath)
	if part.Strategy != "" {
		_ = doc.SetCreate(ppath+".strategy", part.Strategy)
	}
	if part.Column != "" {
		_ = doc.SetCreate(ppath+".column", part.Column)
	}
	for _, sub := range part.Partitions {
		_ = doc.NewArrayTable(ppath + ".partitions")
		idx := countArrayTableEntries(doc, ppath+".partitions") - 1
		subPath := ppath + ".partitions[" + itoa(idx) + "]"
		if sub.Strategy != "" {
			_ = doc.SetCreate(subPath+".strategy", sub.Strategy)
		}
		if sub.Column != "" {
			_ = doc.SetCreate(subPath+".column", sub.Column)
		}
	}
}

// writeMaintenance writes [tables.<name>.maintenance].
func writeMaintenance(doc *tomledit.DocumentNode, tpath string, m *parse.RawMaintenance) {
	mpath := tpath + ".maintenance"
	_ = doc.NewTable(mpath)
	if m.Premake != nil {
		_ = doc.SetCreate(mpath+".premake", *m.Premake)
	}
	if m.Retention != nil {
		_ = doc.SetCreate(mpath+".retention", *m.Retention)
	}
	if m.RetentionKeepTable != nil {
		_ = doc.SetCreate(mpath+".retention_keep_table", *m.RetentionKeepTable)
	}
}

// writeMapSorted writes map entries sorted by key name.
func writeMapSorted[V any](doc *tomledit.DocumentNode, tpath, section string, m map[string]V, writeFn func(name string, v V)) {
	if len(m) == 0 {
		return
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		writeFn(name, m[name])
	}
}

// countArrayTableEntries counts how many [[path]] entries exist in the document.
func countArrayTableEntries(doc *tomledit.DocumentNode, path string) int {
	// Parse the path to get the key path segments.
	keyPath := splitDotPath(path)
	count := 0
	for _, child := range doc.Children {
		if at, ok := child.(*tomledit.ArrayTableNode); ok {
			if pathsEqual(at.KeyPath, keyPath) {
				count++
			}
		}
	}
	return count
}

// splitDotPath splits a dot-separated path into parts.
func splitDotPath(path string) []string {
	var parts []string
	current := ""
	for _, r := range path {
		if r == '.' {
			if current != "" {
				parts = append(parts, current)
			}
			current = ""
		} else if r == '[' {
			// Stop at array index syntax.
			if current != "" {
				parts = append(parts, current)
			}
			break
		} else {
			current += string(r)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// pathsEqual returns true if two string slices are identical.
func pathsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// itoa converts an int to a string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
