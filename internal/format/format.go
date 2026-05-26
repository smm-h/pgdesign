// Package format implements canonical TOML formatting for pgdesign schema files.
// It parses the input via go-toml-edit to get a comment-preserving AST, then
// reorders sections in-place according to canonical ordering. Comments attached
// to sections and keys travel with them during reordering.
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
// Comments are preserved: leading comments and inline comments on sections and
// keys travel with their associated node during reordering.
func Format(input []byte, config *Config) ([]byte, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// Parse into AST (preserves comments).
	doc, err := tomledit.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("TOML parse error: %v", err)
	}

	// Parse into RawSchema for structural analysis (topo sort, FK info, etc.).
	raw, diags := parse.Bytes(input)
	if raw == nil {
		if len(diags) > 0 {
			return nil, fmt.Errorf("%s", diags[0].Message)
		}
		return nil, fmt.Errorf("failed to parse TOML")
	}

	// Determine canonical table order.
	tableOrder := orderTables(raw, config.TableOrder)

	// Reorder the AST in-place.
	reorderDocument(doc, raw, tableOrder, config)

	return doc.Format(), nil
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

// sectionKind classifies a top-level AST node by which schema section it
// belongs to: "meta", "types", "tables", or "other" (for top-level KVs,
// comments, etc.).
type sectionKind int

const (
	kindOther  sectionKind = iota
	kindMeta
	kindTypes
	kindTables
)

// classifyNode returns the section kind and (for tables) the table name.
func classifyNode(node tomledit.Node) (kind sectionKind, tableName string) {
	switch n := node.(type) {
	case *tomledit.TableNode:
		if len(n.KeyPath) >= 1 {
			switch n.KeyPath[0] {
			case "meta":
				return kindMeta, ""
			case "types":
				return kindTypes, ""
			case "tables":
				if len(n.KeyPath) >= 2 {
					return kindTables, n.KeyPath[1]
				}
				return kindTables, ""
			}
		}
	case *tomledit.ArrayTableNode:
		if len(n.KeyPath) >= 2 && n.KeyPath[0] == "tables" {
			return kindTables, n.KeyPath[1]
		}
	}
	return kindOther, ""
}

// typeName extracts the type name from a [types.X] table node.
func typeName(node tomledit.Node) string {
	if tbl, ok := node.(*tomledit.TableNode); ok {
		if len(tbl.KeyPath) >= 2 && tbl.KeyPath[0] == "types" {
			return tbl.KeyPath[1]
		}
	}
	return ""
}

// tableSubSectionKind classifies a table's sub-section for ordering purposes.
// Canonical order: table header, columns, fks, indexes, unique, checks,
// partitioning, maintenance, dependencies.
type tableSubSectionKind int

const (
	subHeader       tableSubSectionKind = iota // [tables.X] itself
	subColumns                                  // [tables.X.columns.*]
	subFKs                                      // [tables.X.fks.*]
	subIndexes                                  // [tables.X.indexes.*]
	subUnique                                   // [tables.X.unique.*]
	subChecks                                   // [tables.X.checks.*]
	subPartitioning                             // [tables.X.partitioning] and partitions
	subMaintenance                              // [tables.X.maintenance]
	subDependencies                             // [[tables.X.dependencies]]
	subOther                                    // anything else
)

// classifyTableSub classifies a node that belongs to a specific table.
func classifyTableSub(node tomledit.Node, tableName string) (tableSubSectionKind, string) {
	switch n := node.(type) {
	case *tomledit.TableNode:
		if len(n.KeyPath) == 2 && n.KeyPath[0] == "tables" && n.KeyPath[1] == tableName {
			return subHeader, ""
		}
		if len(n.KeyPath) >= 3 && n.KeyPath[0] == "tables" && n.KeyPath[1] == tableName {
			subName := n.KeyPath[2]
			switch subName {
			case "columns":
				name := ""
				if len(n.KeyPath) >= 4 {
					name = n.KeyPath[3]
				}
				return subColumns, name
			case "fks":
				name := ""
				if len(n.KeyPath) >= 4 {
					name = n.KeyPath[3]
				}
				return subFKs, name
			case "indexes":
				name := ""
				if len(n.KeyPath) >= 4 {
					name = n.KeyPath[3]
				}
				return subIndexes, name
			case "unique":
				name := ""
				if len(n.KeyPath) >= 4 {
					name = n.KeyPath[3]
				}
				return subUnique, name
			case "checks":
				name := ""
				if len(n.KeyPath) >= 4 {
					name = n.KeyPath[3]
				}
				return subChecks, name
			case "partitioning":
				return subPartitioning, ""
			case "maintenance":
				return subMaintenance, ""
			}
		}
	case *tomledit.ArrayTableNode:
		if len(n.KeyPath) >= 3 && n.KeyPath[0] == "tables" && n.KeyPath[1] == tableName {
			subName := n.KeyPath[2]
			switch subName {
			case "dependencies":
				return subDependencies, ""
			case "partitioning":
				// [[tables.X.partitioning.partitions]]
				return subPartitioning, ""
			}
		}
	}
	return subOther, ""
}

// reorderDocument reorders the document's Children in canonical order:
// 1. [meta]
// 2. [types.*] alphabetically
// 3. [tables.*] in the specified order, with sub-sections in canonical order
func reorderDocument(doc *tomledit.DocumentNode, raw *parse.RawSchema, tableOrder []string, config *Config) {
	// Partition children by section kind.
	var others []tomledit.Node  // top-level KVs, comments before any section
	var metaNodes []tomledit.Node
	typeNodes := map[string][]tomledit.Node{} // keyed by type name
	tableNodes := map[string][]tomledit.Node{} // keyed by table name

	for _, child := range doc.Children {
		kind, tName := classifyNode(child)
		switch kind {
		case kindMeta:
			metaNodes = append(metaNodes, child)
		case kindTypes:
			name := typeName(child)
			typeNodes[name] = append(typeNodes[name], child)
		case kindTables:
			tableNodes[tName] = append(tableNodes[tName], child)
		default:
			others = append(others, child)
		}
	}

	// Collect type names and sort alphabetically.
	var typeNames []string
	for name := range typeNodes {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)

	// Build raw table lookup for column ordering.
	rawTableByName := make(map[string]*parse.RawTable, len(raw.Tables))
	for i := range raw.Tables {
		rawTableByName[raw.Tables[i].Name] = &raw.Tables[i]
	}

	// Reassemble children in canonical order.
	var newChildren []tomledit.Node

	// 1. "other" nodes (top-level comments, KVs) first -- typically none in pgdesign files.
	newChildren = append(newChildren, others...)

	// 2. [meta] section.
	newChildren = append(newChildren, metaNodes...)

	// 3. [types.*] alphabetically.
	for _, name := range typeNames {
		newChildren = append(newChildren, typeNodes[name]...)
	}

	// 4. [tables.*] in canonical order, with sub-sections reordered.
	for _, tblName := range tableOrder {
		nodes := tableNodes[tblName]
		if len(nodes) == 0 {
			continue
		}
		reordered := reorderTableSections(nodes, tblName, rawTableByName[tblName], config)
		newChildren = append(newChildren, reordered...)
	}

	// Post-process: reorder KVs within table header nodes (comment before pk).
	reorderDocumentPostProcess(newChildren)

	doc.Children = newChildren
}

// reorderTableSections reorders the nodes belonging to a single table in
// canonical sub-section order.
func reorderTableSections(nodes []tomledit.Node, tableName string, rawTable *parse.RawTable, config *Config) []tomledit.Node {
	// Classify each node.
	type classified struct {
		node tomledit.Node
		kind tableSubSectionKind
		name string // sub-item name (column name, fk name, etc.)
	}

	var items []classified
	for _, n := range nodes {
		kind, name := classifyTableSub(n, tableName)
		items = append(items, classified{node: n, kind: kind, name: name})
	}

	// Build the canonical column order if we have raw table info.
	var columnOrder []string
	if rawTable != nil {
		orderedCols := orderColumns(rawTable, config.ColumnOrder)
		for _, col := range orderedCols {
			columnOrder = append(columnOrder, col.Name)
		}
	}

	// Sort items by canonical order.
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]

		// Primary sort: sub-section kind.
		if a.kind != b.kind {
			return a.kind < b.kind
		}

		// Secondary sort within the same kind.
		switch a.kind {
		case subColumns:
			// Sort by canonical column order.
			ai := columnIndex(columnOrder, a.name)
			bi := columnIndex(columnOrder, b.name)
			return ai < bi
		case subFKs, subIndexes, subUnique, subChecks:
			// Sort alphabetically by name.
			return a.name < b.name
		case subDependencies:
			// Preserve original order (stable sort handles this).
			return false
		}
		return false
	})

	result := make([]tomledit.Node, len(items))
	for i, item := range items {
		result[i] = item.node
	}
	return result
}

// columnIndex returns the position of a column name in the canonical order.
// Returns a large number for unknown columns to push them to the end.
func columnIndex(order []string, name string) int {
	for i, n := range order {
		if n == name {
			return i
		}
	}
	return len(order)
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

// reorderTableKVs reorders key-value pairs within a table header node to
// ensure "comment" comes before "pk".
func reorderTableKVs(node tomledit.Node) {
	tbl, ok := node.(*tomledit.TableNode)
	if !ok {
		return
	}

	// Separate KVs by key name for canonical ordering.
	var commentKV, pkKV []tomledit.Node
	var otherKVs []tomledit.Node

	for _, child := range tbl.Children {
		kv, ok := child.(*tomledit.KeyValueNode)
		if !ok {
			otherKVs = append(otherKVs, child)
			continue
		}
		key := ""
		if len(kv.Key.Parts) > 0 {
			key = kv.Key.Parts[0]
		}
		switch key {
		case "comment":
			commentKV = append(commentKV, child)
		case "pk":
			pkKV = append(pkKV, child)
		default:
			otherKVs = append(otherKVs, child)
		}
	}

	// Reassemble: comment, pk, then everything else.
	var newChildren []tomledit.Node
	newChildren = append(newChildren, commentKV...)
	newChildren = append(newChildren, pkKV...)
	newChildren = append(newChildren, otherKVs...)
	tbl.Children = newChildren
}

// reorderDocumentPostProcess reorders KVs within table header nodes after
// the main section reordering.
func reorderDocumentPostProcess(children []tomledit.Node) {
	for _, child := range children {
		if tbl, ok := child.(*tomledit.TableNode); ok {
			if len(tbl.KeyPath) == 2 && tbl.KeyPath[0] == "tables" {
				reorderTableKVs(tbl)
			}
		}
	}
}
