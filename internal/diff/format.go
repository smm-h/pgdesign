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

	// Materialized Views
	for _, name := range d.MaterializedViewsAdded {
		fmt.Fprintf(&b, "%s+ materialized view %s%s\n", colorGreen, name, colorReset)
	}
	for _, name := range d.MaterializedViewsRemoved {
		fmt.Fprintf(&b, "%s- materialized view %s%s\n", colorRed, name, colorReset)
	}
	for _, mvd := range d.MaterializedViewsChanged {
		fmt.Fprintf(&b, "%s~ materialized view %s%s\n", colorYellow, mvd.Name, colorReset)
		if mvd.QueryChanged != nil {
			fmt.Fprintf(&b, "  query:\n")
			fmt.Fprintf(&b, "    %s- %s%s\n", colorRed, mvd.QueryChanged[0], colorReset)
			fmt.Fprintf(&b, "    %s+ %s%s\n", colorGreen, mvd.QueryChanged[1], colorReset)
		}
		if mvd.CommentChanged != nil {
			fmt.Fprintf(&b, "  %s~ comment: %q -> %q%s\n", colorYellow, mvd.CommentChanged[0], mvd.CommentChanged[1], colorReset)
		}
		if mvd.WithDataChanged != nil {
			fmt.Fprintf(&b, "  %s~ with_data: %v -> %v%s\n", colorYellow, mvd.WithDataChanged[0], mvd.WithDataChanged[1], colorReset)
		}
		for _, idx := range mvd.IndexesAdded {
			fmt.Fprintf(&b, "  %s+ index %s%s\n", colorGreen, idx.Name, colorReset)
		}
		for _, name := range mvd.IndexesRemoved {
			fmt.Fprintf(&b, "  %s- index %s%s\n", colorRed, name, colorReset)
		}
		for _, ic := range mvd.IndexesChanged {
			fmt.Fprintf(&b, "  %s~ index %s%s\n", colorYellow, ic.Name, colorReset)
		}
	}

	// Composite Types
	for _, name := range d.CompositeTypesAdded {
		fmt.Fprintf(&b, "%s+ composite type %s%s\n", colorGreen, name, colorReset)
	}
	for _, name := range d.CompositeTypesRemoved {
		fmt.Fprintf(&b, "%s- composite type %s%s\n", colorRed, name, colorReset)
	}
	for _, ctd := range d.CompositeTypesChanged {
		fmt.Fprintf(&b, "%s~ composite type %s%s\n", colorYellow, ctd.Name, colorReset)
		for _, f := range ctd.FieldsAdded {
			fmt.Fprintf(&b, "  %s+ field %s %s%s\n", colorGreen, f.Name, f.PGType, colorReset)
		}
		for _, name := range ctd.FieldsRemoved {
			fmt.Fprintf(&b, "  %s- field %s%s\n", colorRed, name, colorReset)
		}
		for _, fc := range ctd.FieldsChanged {
			if fc.TypeChanged != nil {
				fmt.Fprintf(&b, "  %s~ field %s: %s -> %s%s\n", colorYellow, fc.Name, fc.TypeChanged[0], fc.TypeChanged[1], colorReset)
			}
		}
		if ctd.CommentChanged != nil {
			fmt.Fprintf(&b, "  %s~ comment: %q -> %q%s\n", colorYellow, ctd.CommentChanged[0], ctd.CommentChanged[1], colorReset)
		}
	}

	// Domains
	for _, name := range d.DomainsAdded {
		fmt.Fprintf(&b, "%s+ domain %s%s\n", colorGreen, name, colorReset)
	}
	for _, name := range d.DomainsRemoved {
		fmt.Fprintf(&b, "%s- domain %s%s\n", colorRed, name, colorReset)
	}
	for _, dd := range d.DomainsChanged {
		fmt.Fprintf(&b, "%s~ domain %s%s\n", colorYellow, dd.Name, colorReset)
		if dd.BaseTypeChanged != nil {
			fmt.Fprintf(&b, "  %s~ base_type: %s -> %s%s\n", colorYellow, dd.BaseTypeChanged[0], dd.BaseTypeChanged[1], colorReset)
		}
		if dd.CheckChanged != nil {
			fmt.Fprintf(&b, "  %s~ check: %q -> %q%s\n", colorYellow, dd.CheckChanged[0], dd.CheckChanged[1], colorReset)
		}
		if dd.DefaultChanged != nil {
			fmt.Fprintf(&b, "  %s~ default: %q -> %q%s\n", colorYellow, dd.DefaultChanged[0], dd.DefaultChanged[1], colorReset)
		}
		if dd.NotNullChanged != nil {
			old := "NULL"
			new_ := "NULL"
			if dd.NotNullChanged[0] {
				old = "NOT NULL"
			}
			if dd.NotNullChanged[1] {
				new_ = "NOT NULL"
			}
			fmt.Fprintf(&b, "  %s~ not_null: %s -> %s%s\n", colorYellow, old, new_, colorReset)
		}
		if dd.CommentChanged != nil {
			fmt.Fprintf(&b, "  %s~ comment: %q -> %q%s\n", colorYellow, dd.CommentChanged[0], dd.CommentChanged[1], colorReset)
		}
	}

	// Sequences
	for _, name := range d.SequencesAdded {
		fmt.Fprintf(&b, "%s+ sequence %s%s\n", colorGreen, name, colorReset)
	}
	for _, name := range d.SequencesRemoved {
		fmt.Fprintf(&b, "%s- sequence %s%s\n", colorRed, name, colorReset)
	}
	for _, sd := range d.SequencesChanged {
		fmt.Fprintf(&b, "%s~ sequence %s%s\n", colorYellow, sd.Name, colorReset)
		if sd.StartChanged != nil {
			fmt.Fprintf(&b, "  %s~ start: %s -> %s%s\n", colorYellow, formatOptionalInt64(sd.StartChanged[0]), formatOptionalInt64(sd.StartChanged[1]), colorReset)
		}
		if sd.IncrementChanged != nil {
			fmt.Fprintf(&b, "  %s~ increment: %s -> %s%s\n", colorYellow, formatOptionalInt64(sd.IncrementChanged[0]), formatOptionalInt64(sd.IncrementChanged[1]), colorReset)
		}
		if sd.MinValueChanged != nil {
			fmt.Fprintf(&b, "  %s~ min_value: %s -> %s%s\n", colorYellow, formatOptionalInt64(sd.MinValueChanged[0]), formatOptionalInt64(sd.MinValueChanged[1]), colorReset)
		}
		if sd.MaxValueChanged != nil {
			fmt.Fprintf(&b, "  %s~ max_value: %s -> %s%s\n", colorYellow, formatOptionalInt64(sd.MaxValueChanged[0]), formatOptionalInt64(sd.MaxValueChanged[1]), colorReset)
		}
		if sd.CacheChanged != nil {
			fmt.Fprintf(&b, "  %s~ cache: %s -> %s%s\n", colorYellow, formatOptionalInt64(sd.CacheChanged[0]), formatOptionalInt64(sd.CacheChanged[1]), colorReset)
		}
		if sd.CycleChanged != nil {
			fmt.Fprintf(&b, "  %s~ cycle: %s -> %s%s\n", colorYellow, boolStr(sd.CycleChanged[0]), boolStr(sd.CycleChanged[1]), colorReset)
		}
		if sd.OwnedByChanged != nil {
			fmt.Fprintf(&b, "  %s~ owned_by: %q -> %q%s\n", colorYellow, sd.OwnedByChanged[0], sd.OwnedByChanged[1], colorReset)
		}
		if sd.CommentChanged != nil {
			fmt.Fprintf(&b, "  %s~ comment: %q -> %q%s\n", colorYellow, sd.CommentChanged[0], sd.CommentChanged[1], colorReset)
		}
	}

	// Functions
	for _, name := range d.FunctionsAdded {
		fmt.Fprintf(&b, "%s+ function %s%s\n", colorGreen, name, colorReset)
	}
	for _, name := range d.FunctionsRemoved {
		fmt.Fprintf(&b, "%s- function %s%s\n", colorRed, name, colorReset)
	}
	for _, fd := range d.FunctionsChanged {
		fmt.Fprintf(&b, "%s~ function %s%s\n", colorYellow, fd.Name, colorReset)
		if fd.BodyChanged != nil {
			fmt.Fprintf(&b, "  %s~ body changed%s\n", colorYellow, colorReset)
		}
		if fd.ReturnTypeChanged != nil {
			fmt.Fprintf(&b, "  %s~ returns: %s -> %s%s\n", colorYellow, fd.ReturnTypeChanged[0], fd.ReturnTypeChanged[1], colorReset)
		}
		if fd.ArgsChanged {
			fmt.Fprintf(&b, "  %s~ args changed%s\n", colorYellow, colorReset)
		}
		if fd.SignatureChanged {
			fmt.Fprintf(&b, "  %s~ signature changed (requires DROP + CREATE)%s\n", colorRed, colorReset)
		}
		if fd.LanguageChanged != nil {
			fmt.Fprintf(&b, "  %s~ language: %s -> %s%s\n", colorYellow, fd.LanguageChanged[0], fd.LanguageChanged[1], colorReset)
		}
		if fd.VolatilityChanged != nil {
			fmt.Fprintf(&b, "  %s~ volatility: %s -> %s%s\n", colorYellow, fd.VolatilityChanged[0], fd.VolatilityChanged[1], colorReset)
		}
		if fd.ParallelChanged != nil {
			fmt.Fprintf(&b, "  %s~ parallel: %s -> %s%s\n", colorYellow, fd.ParallelChanged[0], fd.ParallelChanged[1], colorReset)
		}
		if fd.SecurityChanged != nil {
			old := "INVOKER"
			new_ := "INVOKER"
			if fd.SecurityChanged[0] {
				old = "DEFINER"
			}
			if fd.SecurityChanged[1] {
				new_ = "DEFINER"
			}
			fmt.Fprintf(&b, "  %s~ security: %s -> %s%s\n", colorYellow, old, new_, colorReset)
		}
		if fd.CommentChanged != nil {
			fmt.Fprintf(&b, "  %s~ comment: %q -> %q%s\n", colorYellow, fd.CommentChanged[0], fd.CommentChanged[1], colorReset)
		}
		if fd.CostChanged != nil {
			fmt.Fprintf(&b, "  %s~ cost: %s -> %s%s\n", colorYellow, formatOptionalFloat64(fd.CostChanged[0]), formatOptionalFloat64(fd.CostChanged[1]), colorReset)
		}
		if fd.RowsChanged != nil {
			fmt.Fprintf(&b, "  %s~ rows: %s -> %s%s\n", colorYellow, formatOptionalFloat64(fd.RowsChanged[0]), formatOptionalFloat64(fd.RowsChanged[1]), colorReset)
		}
		if fd.IsProcChanged != nil {
			oldKind := "FUNCTION"
			newKind := "FUNCTION"
			if fd.IsProcChanged[0] {
				oldKind = "PROCEDURE"
			}
			if fd.IsProcChanged[1] {
				newKind = "PROCEDURE"
			}
			fmt.Fprintf(&b, "  %s~ kind: %s -> %s%s\n", colorYellow, oldKind, newKind, colorReset)
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

	// Exclusions
	for _, exc := range td.ExclusionsAdded {
		fmt.Fprintf(b, "  %s+ exclusion %s%s\n", colorGreen, exc.Name, colorReset)
	}
	for _, name := range td.ExclusionsRemoved {
		fmt.Fprintf(b, "  %s- exclusion %s%s\n", colorRed, name, colorReset)
	}

	// Triggers
	for _, trig := range td.TriggersAdded {
		fmt.Fprintf(b, "  %s+ trigger %s%s\n", colorGreen, trig.Name, colorReset)
	}
	for _, name := range td.TriggersRemoved {
		fmt.Fprintf(b, "  %s- trigger %s%s\n", colorRed, name, colorReset)
	}
	for _, tc := range td.TriggersChanged {
		fmt.Fprintf(b, "  %s~ trigger %s%s\n", colorYellow, tc.Name, colorReset)
	}

	// Policies
	for _, pol := range td.PoliciesAdded {
		fmt.Fprintf(b, "  %s+ policy %s%s\n", colorGreen, pol.Name, colorReset)
	}
	for _, name := range td.PoliciesRemoved {
		fmt.Fprintf(b, "  %s- policy %s%s\n", colorRed, name, colorReset)
	}
	for _, pd := range td.PoliciesChanged {
		fmt.Fprintf(b, "  %s~ policy %s%s\n", colorYellow, pd.Name, colorReset)
		if pd.TypeChanged != nil {
			fmt.Fprintf(b, "    type: %s -> %s\n", pd.TypeChanged[0], pd.TypeChanged[1])
		}
		if pd.RoleChanged != nil {
			fmt.Fprintf(b, "    role: %q -> %q\n", pd.RoleChanged[0], pd.RoleChanged[1])
		}
		if pd.UsingChanged != nil {
			fmt.Fprintf(b, "    using: %q -> %q\n", pd.UsingChanged[0], pd.UsingChanged[1])
		}
		if pd.WithCheckChanged != nil {
			fmt.Fprintf(b, "    with_check: %q -> %q\n", pd.WithCheckChanged[0], pd.WithCheckChanged[1])
		}
		if pd.ErrorCodeChanged != nil {
			fmt.Fprintf(b, "    error_code: %q -> %q\n", pd.ErrorCodeChanged[0], pd.ErrorCodeChanged[1])
		}
		if pd.ErrorMessageChanged != nil {
			fmt.Fprintf(b, "    error_message: %q -> %q\n", pd.ErrorMessageChanged[0], pd.ErrorMessageChanged[1])
		}
	}

	// EnableRLS
	if td.EnableRLSChanged != nil {
		fmt.Fprintf(b, "  %s~ enable_rls: %s -> %s%s\n", colorYellow,
			boolStr(td.EnableRLSChanged[0]), boolStr(td.EnableRLSChanged[1]), colorReset)
	}

	// ForceRLS
	if td.ForceRLSChanged != nil {
		fmt.Fprintf(b, "  %s~ force_rls: %s -> %s%s\n", colorYellow,
			boolStr(td.ForceRLSChanged[0]), boolStr(td.ForceRLSChanged[1]), colorReset)
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
	if cc.StoredChanged != nil {
		fmt.Fprintf(b, "    stored: %s -> %s\n", boolStr(cc.StoredChanged[0]), boolStr(cc.StoredChanged[1]))
	}
	if cc.IdentityChanged != nil {
		fmt.Fprintf(b, "    identity: %q -> %q\n", cc.IdentityChanged[0], cc.IdentityChanged[1])
	}
	if cc.ArrayChanged != nil {
		fmt.Fprintf(b, "    array: %s -> %s\n", arrayStr(cc.ArrayChanged[0]), arrayStr(cc.ArrayChanged[1]))
	}
	if cc.CollationChanged != nil {
		fmt.Fprintf(b, "    collation: %q -> %q\n", cc.CollationChanged[0], cc.CollationChanged[1])
	}
	if cc.JSONSchemaChanged != nil {
		fmt.Fprintf(b, "    json_schema: %q -> %q\n", cc.JSONSchemaChanged[0], cc.JSONSchemaChanged[1])
	}
	if cc.StatisticsChanged != nil {
		fmt.Fprintf(b, "    statistics: %s -> %s\n", formatOptionalInt(cc.StatisticsChanged[0]), formatOptionalInt(cc.StatisticsChanged[1]))
	}
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func arrayStr(isArray bool) string {
	return boolStr(isArray)
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

func formatOptionalInt64(v *int64) string {
	if v == nil {
		return "default"
	}
	return fmt.Sprintf("%d", *v)
}

func formatOptionalFloat64(v *float64) string {
	if v == nil {
		return "default"
	}
	return fmt.Sprintf("%g", *v)
}

func formatOptionalInt(v *int) string {
	if v == nil {
		return "default"
	}
	return fmt.Sprintf("%d", *v)
}

// FormatJSON renders the diff as a JSON string.
func FormatJSON(d *SchemaDiff) string {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\"error\": %q}", err.Error())
	}
	return string(data)
}
