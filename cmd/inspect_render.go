package cmd

import (
	"fmt"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/types"
)

// printSummary renders the human table for `corvex inspect <project>`. Every
// byte here is locked by golden files, including the blank line that precedes
// the highlights block even when the block itself is suppressed.
func printSummary(out ops.InspectReport) error {
	fmt.Printf("Project: %s\n", out.Project)
	if out.Intent != "" {
		fmt.Printf("Intent:  %s\n", out.Intent)
	}
	fmt.Printf("Tasks:   %d/%d done   ·   $%.2f total\n\n", out.Completed, out.Total, out.TotalCostUSD)

	fmt.Printf("%-5s %-8s %-10s %-7s %-8s %s\n", "ID", "STATUS", "DURATION", "RETRIES", "COST", "TITLE")
	for _, s := range out.Tasks {
		printSummaryRow(s)
	}

	if len(out.Tasks) > 0 {
		printHighlights(out.Tasks)
	}
	return nil
}

// printSummaryRow writes one task row. The %-8s status column is padded by rune
// count while the glyph occupies two terminal cells, so the column looks
// misaligned — a known anomaly kept as-is.
func printSummaryRow(s ops.InspectTaskStat) {
	dur := "—"
	if s.DurationMs > 0 {
		dur = humanDuration(msDuration(s.DurationMs))
	}
	cost := "—"
	if s.CostUSD > 0 {
		cost = fmt.Sprintf("$%.2f", s.CostUSD)
	}
	fmt.Printf("%-5s %-8s %-10s %-7d %-8s %s\n",
		s.ID, glyphFor(types.TaskStatus(s.Status)), dur, s.Retries, cost, truncateTitle(s.Title))
}

// printHighlights lists the top three slowest and most expensive tasks, each
// line omitted entirely when its metric is zero across the board.
func printHighlights(stats []ops.InspectTaskStat) {
	slowest := ops.InspectSlowest(stats, 3)
	expensive := ops.InspectCostliest(stats, 3)

	fmt.Println()
	if slowest[0].DurationMs > 0 {
		fmt.Print("Slowest:    ")
		for i, s := range slowest {
			if s.DurationMs == 0 {
				break
			}
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("%s (%s)", s.ID, humanDuration(msDuration(s.DurationMs)))
		}
		fmt.Println()
	}
	if expensive[0].CostUSD > 0 {
		fmt.Print("Expensive:  ")
		for i, s := range expensive {
			if s.CostUSD == 0 {
				break
			}
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("%s ($%.2f)", s.ID, s.CostUSD)
		}
		fmt.Println()
	}
}
