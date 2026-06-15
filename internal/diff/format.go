package diff

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smm-h/pgdesign/internal/risk"
)

// ANSI color codes.
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
)

// FormatTerminal renders the diff as human-readable colored terminal output.
func FormatTerminal(d *SchemaDiff) string {
	if d.IsEmpty() {
		return "Schema is up to date.\n"
	}

	var b strings.Builder

	b.WriteString(d.Summary())
	b.WriteString("\n\n")

	// Extensions
	for _, name := range d.ExtensionsAdded {
		fmt.Fprintf(&b, "%s+ extension %s%s\n", colorGreen, name, colorReset)
	}
	for _, name := range d.ExtensionsRemoved {
		fmt.Fprintf(&b, "%s- extension %s%s\n", colorRed, name, colorReset)
	}

	// Enums
	for _, name := range d.EnumsAdded {
		fmt.Fprintf(&b, "%s+ enum %s%s\n", colorGreen, name, colorReset)
	}
	for _, name := range d.EnumsRemoved {
		fmt.Fprintf(&b, "%s- enum %s%s\n", colorRed, name, colorReset)
	}
	for _, ec := range d.EnumsChanged {
		fmt.Fprintf(&b, "%s~ enum %s%s\n", colorYellow, ec.Name, colorReset)
		for _, v := range ec.ValuesAddedAtEnd {
			fmt.Fprintf(&b, "  %s+ %s (safe, appended)%s\n", colorGreen, v, colorReset)
		}
		for _, ins := range ec.ValuesInserted {
			if ins.After == "" {
				fmt.Fprintf(&b, "  %s+ %s (before first value, requires BEFORE/AFTER)%s\n", colorYellow, ins.Value, colorReset)
			} else {
				fmt.Fprintf(&b, "  %s+ %s (after %q, requires BEFORE/AFTER)%s\n", colorYellow, ins.Value, ins.After, colorReset)
			}
		}
		for _, v := range ec.ValuesRemoved {
			fmt.Fprintf(&b, "  %s- %s%s\n", colorRed, v, colorReset)
		}
		if ec.Reordered {
			fmt.Fprintf(&b, "  %s~ values reordered (dangerous)%s\n", colorRed, colorReset)
		}
	}

	// Tables
	for _, name := range d.TablesAdded {
		fmt.Fprintf(&b, "%s+ table %s%s\n", colorGreen, name, colorReset)
	}
	for _, name := range d.TablesRemoved {
		fmt.Fprintf(&b, "%s- table %s%s\n", colorRed, name, colorReset)
	}
	for _, td := range d.TablesChanged {
		formatTableDiff(&b, &td)
	}

	// Views
	for _, name := range d.ViewsAdded {
		fmt.Fprintf(&b, "%s+ view %s%s\n", colorGreen, name, colorReset)
	}
	for _, name := range d.ViewsRemoved {
		fmt.Fprintf(&b, "%s- view %s%s\n", colorRed, name, colorReset)
	}
	for _, vd := range d.ViewsChanged {
		fmt.Fprintf(&b, "%s~ view %s%s\n", colorYellow, vd.Name, colorReset)
		if vd.QueryChanged != nil {
			fmt.Fprintf(&b, "  query:\n")
			fmt.Fprintf(&b, "    %s- %s%s\n", colorRed, vd.QueryChanged[0], colorReset)
			fmt.Fprintf(&b, "    %s+ %s%s\n", colorGreen, vd.QueryChanged[1], colorReset)
		}
		if vd.CommentChanged != nil {
			fmt.Fprintf(&b, "  %s~ comment: %q -> %q%s\n", colorYellow, vd.CommentChanged[0], vd.CommentChanged[1], colorReset)
		}
	}

	return b.String()
}

func formatTableDiff(b *strings.Builder, td *TableDiff) {
	fmt.Fprintf(b, "%s~ table %s%s\n", colorYellow, td.Name, colorReset)

	// PK
	if td.PKChanged != nil {
		fmt.Fprintf(b, "  %s~ pk: [%s] -> [%s]%s\n", colorYellow,
			strings.Join(td.PKChanged[0], ", "),
			strings.Join(td.PKChanged[1], ", "),
			colorReset)
	}

	// Owner
	if td.OwnerChanged != nil {
		fmt.Fprintf(b, "  %s~ owner: %s -> %s%s\n", colorYellow,
			td.OwnerChanged[0], td.OwnerChanged[1], colorReset)
	}

	// Comment
	if td.CommentChanged != nil {
		fmt.Fprintf(b, "  %s~ comment: %q -> %q%s\n", colorYellow,
			td.CommentChanged[0], td.CommentChanged[1], colorReset)
	}

	// Columns
	for _, col := range td.ColumnsAdded {
		fmt.Fprintf(b, "  %s+ column %s %s%s\n", colorGreen, col.Name, col.PGType, colorReset)
	}
	for _, name := range td.ColumnsRemoved {
		fmt.Fprintf(b, "  %s- column %s%s\n", colorRed, name, colorReset)
	}
	for _, cc := range td.ColumnsChanged {
		formatColumnChange(b, &cc)
	}

	// FKs
	for _, fk := range td.FKsAdded {
		fmt.Fprintf(b, "  %s+ fk %s (%s) -> %s(%s)%s\n", colorGreen,
			fk.Name, strings.Join(fk.Columns, ", "),
			fk.RefTable, strings.Join(fk.RefColumns, ", "),
			colorReset)
	}
	for _, name := range td.FKsRemoved {
		fmt.Fprintf(b, "  %s- fk %s%s\n", colorRed, name, colorReset)
	}
	for _, fc := range td.FKsChanged {
		fmt.Fprintf(b, "  %s~ fk %s%s\n", colorYellow, fc.Name, colorReset)
	}

	// Indexes
	for _, idx := range td.IndexesAdded {
		fmt.Fprintf(b, "  %s+ index %s (%s)%s\n", colorGreen,
			idx.Name, strings.Join(idx.Columns, ", "), colorReset)
	}
	for _, name := range td.IndexesRemoved {
		fmt.Fprintf(b, "  %s- index %s%s\n", colorRed, name, colorReset)
	}
	for _, ic := range td.IndexesChanged {
		fmt.Fprintf(b, "  %s~ index %s%s\n", colorYellow, ic.Name, colorReset)
	}

	// Uniques
	for _, u := range td.UniquesAdded {
		fmt.Fprintf(b, "  %s+ unique %s (%s)%s\n", colorGreen,
			u.Name, strings.Join(u.Columns, ", "), colorReset)
	}
	for _, name := range td.UniquesRemoved {
		fmt.Fprintf(b, "  %s- unique %s%s\n", colorRed, name, colorReset)
	}

	// Checks
	for _, c := range td.ChecksAdded {
		fmt.Fprintf(b, "  %s+ check %s (%s)%s\n", colorGreen, c.Name, c.Expr, colorReset)
	}
	for _, name := range td.ChecksRemoved {
		fmt.Fprintf(b, "  %s- check %s%s\n", colorRed, name, colorReset)
	}

	// Partitioning
	if pd := td.PartitioningChanged; pd != nil {
		if pd.StrategyChanged != nil {
			fmt.Fprintf(b, "  %s~ partitioning: %q -> %q%s\n", colorYellow,
				pd.StrategyChanged[0], pd.StrategyChanged[1], colorReset)
		}
		if pd.KeyChanged != nil {
			fmt.Fprintf(b, "  %s~ partition key: %q -> %q%s\n", colorYellow,
				pd.KeyChanged[0], pd.KeyChanged[1], colorReset)
		}
		for _, name := range td.PartitioningChanged.ChildrenAdded {
			fmt.Fprintf(b, "  %s+ partition: %s%s\n", colorGreen, name, colorReset)
		}
		for _, name := range td.PartitioningChanged.ChildrenRemoved {
			fmt.Fprintf(b, "  %s- partition: %s%s\n", colorRed, name, colorReset)
		}
	}

	// AppendOnly
	if td.AppendOnlyChanged != nil {
		old := "false"
		new_ := "false"
		if td.AppendOnlyChanged[0] {
			old = "true"
		}
		if td.AppendOnlyChanged[1] {
			new_ = "true"
		}
		fmt.Fprintf(b, "  %s~ append_only: %s -> %s%s\n", colorYellow, old, new_, colorReset)
	}
}

func formatColumnChange(b *strings.Builder, cc *ColumnChange) {
	badge := riskBadge(cc.Risk.RiskLevel)
	fmt.Fprintf(b, "  %s~ column %s%s %s\n", colorYellow, cc.Name, colorReset, badge)

	if cc.TypeChanged != nil {
		fmt.Fprintf(b, "    type: %s -> %s\n", cc.TypeChanged[0], cc.TypeChanged[1])
	}
	if cc.NullableChanged != nil {
		oldNullStr := nullStr(cc.NullableChanged[0])
		newNullStr := nullStr(cc.NullableChanged[1])
		fmt.Fprintf(b, "    nullable: %s -> %s\n", oldNullStr, newNullStr)
	}
	if cc.DefaultChanged != nil {
		fmt.Fprintf(b, "    default: %q -> %q\n", cc.DefaultChanged[0], cc.DefaultChanged[1])
	}
	if cc.CommentChanged != nil {
		fmt.Fprintf(b, "    comment: %q -> %q\n", cc.CommentChanged[0], cc.CommentChanged[1])
	}
	if cc.GeneratedChanged != nil {
		fmt.Fprintf(b, "    generated: %q -> %q\n", cc.GeneratedChanged[0], cc.GeneratedChanged[1])
	}
	if cc.IdentityChanged != nil {
		fmt.Fprintf(b, "    identity: %q -> %q\n", cc.IdentityChanged[0], cc.IdentityChanged[1])
	}
	if cc.ArrayChanged != nil {
		fmt.Fprintf(b, "    array: %s -> %s\n", arrayStr(cc.ArrayChanged[0]), arrayStr(cc.ArrayChanged[1]))
	}
}

func arrayStr(isArray bool) string {
	if isArray {
		return "true"
	}
	return "false"
}

func nullStr(notNull bool) string {
	if notNull {
		return "NOT NULL"
	}
	return "NULL"
}

func riskBadge(level risk.RiskLevel) string {
	switch level {
	case risk.Safe:
		return colorGreen + "[SAFE]" + colorReset
	case risk.Caution:
		return colorYellow + "[CAUTION]" + colorReset
	case risk.Dangerous:
		return colorRed + "[DANGEROUS]" + colorReset
	default:
		return ""
	}
}

// FormatJSON renders the diff as a JSON string.
func FormatJSON(d *SchemaDiff) string {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\"error\": %q}", err.Error())
	}
	return string(data)
}
