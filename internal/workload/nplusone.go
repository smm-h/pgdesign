package workload

import (
	"fmt"

	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/model"
)

// NPlusOneThreshold is the minimum calls ratio (child/parent) that triggers a W025 warning.
const NPlusOneThreshold = 100

// MinSignificantCalls is the minimum number of calls for a query to be considered significant.
const MinSignificantCalls int64 = 100

// DetectNPlusOne analyzes statement statistics against the FK graph to detect
// potential N+1 query patterns. For each FK relationship (parent->child), if a
// child-table query has calls >> parent-table query calls (ratio > 100:1) and
// both have significant call counts, it signals a likely N+1 pattern.
func DetectNPlusOne(fkGraph *model.FKGraph, stats []StatementStats) []diagnostic.Diagnostic {
	tableStats := make(map[string][]StatementStats)
	for _, s := range stats {
		for _, t := range s.Tables {
			tableStats[t] = append(tableStats[t], s)
		}
	}

	type pair struct{ from, to string }
	seen := make(map[pair]bool)
	var diags []diagnostic.Diagnostic

	for _, edges := range fkGraph.Forward {
		for _, edge := range edges {
			p := pair{edge.FromTable, edge.ToTable}
			if seen[p] {
				continue
			}
			childStats := tableStats[edge.FromTable]
			parentStats := tableStats[edge.ToTable]
			for _, cs := range childStats {
				if cs.Calls < MinSignificantCalls {
					continue
				}
				for _, ps := range parentStats {
					if ps.Calls < MinSignificantCalls {
						continue
					}
					ratio := cs.Calls / ps.Calls
					if ratio >= NPlusOneThreshold {
						seen[p] = true
						diags = append(diags, diagnostic.Diagnostic{
							Severity:   diagnostic.Warning,
							Code:       "W025",
							Table:      edge.FromTable,
							Message:    fmt.Sprintf("Potential N+1 query pattern: %s queries (%d calls) vs %s queries (%d calls), ratio %d:1", edge.FromTable, cs.Calls, edge.ToTable, ps.Calls, ratio),
							Suggestion: "Consider using a JOIN or batch query to reduce round trips",
						})
						break
					}
				}
				if seen[p] {
					break
				}
			}
		}
	}

	return diags
}
