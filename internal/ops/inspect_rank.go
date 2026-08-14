package ops

import "sort"

// InspectSlowest returns at most n tasks ordered by descending duration.
// Tasks that never ran keep a zero duration and therefore sink to the tail, so
// callers still have to decide whether a zero entry is worth showing.
func InspectSlowest(stats []InspectTaskStat, n int) []InspectTaskStat {
	return rankInspect(stats, n, func(a, b InspectTaskStat) bool { return a.DurationMs > b.DurationMs })
}

// InspectCostliest returns at most n tasks ordered by descending cost, with the
// same tail rule as InspectSlowest for tasks that cost nothing.
func InspectCostliest(stats []InspectTaskStat, n int) []InspectTaskStat {
	return rankInspect(stats, n, func(a, b InspectTaskStat) bool { return a.CostUSD > b.CostUSD })
}

func rankInspect(stats []InspectTaskStat, n int, less func(a, b InspectTaskStat) bool) []InspectTaskStat {
	ranked := make([]InspectTaskStat, len(stats))
	copy(ranked, stats)
	sort.Slice(ranked, func(i, j int) bool { return less(ranked[i], ranked[j]) })
	if n < len(ranked) {
		ranked = ranked[:n]
	}
	return ranked
}
