package cmd

import (
	"fmt"
	"time"

	"github.com/giovannialves/corvex/internal/ops"
)

// renderRunList prints the history screen (canvas 2e): newest first, one line
// per run, with the id first because the id is what every other command takes.
func renderRunList(rows []ops.RunRow, since time.Duration, scoped bool, filters string) {
	if len(rows) == 0 {
		fmt.Println(emptyRunList(since, scoped, filters))
		return
	}
	fmt.Printf("%d run(s):\n\n", len(rows))
	for _, r := range rows {
		fmt.Printf("  %s  %-10s %-9s %s\n", r.RunID, r.Status, r.Liveness, r.Label())
		fmt.Printf("      started %s ago  ·  %s\n", humanWait(r.Age), r.Repo)
	}
	fmt.Printf("\n  → corvex run show %s\n", rows[0].RunID)
}

func emptyRunList(since time.Duration, scoped bool, filters string) string {
	where := "on this machine"
	if scoped {
		where = "in this repository"
	}
	// The filters are named in the empty message on purpose. An empty listing
	// reads as "nothing is running", which is the answer a script waiting on one
	// is looking for — and if the emptiness came from a filter rather than from
	// the world, that reading is wrong in the direction that matters.
	if filters != "" {
		filters = " matching " + filters
	}
	if since <= 0 {
		return "No runs recorded " + where + filters + "."
	}
	return fmt.Sprintf("No runs %s in the last %s%s (--since 0 for all).", where, humanWait(since), filters)
}

// renderProjectRows prints the unit of the line F3's D11 kept: projects of this
// repository, including the ones that were planned and never ran — a state the
// run listing cannot show.
func renderProjectRows(rows []ops.ProjectRow) {
	if len(rows) == 0 {
		fmt.Println("No projects found. Create a spec in .corvex/tasks/<project>/spec.md")
		return
	}
	for _, p := range rows {
		fmt.Printf("  %-24s %-15s", p.Name, p.Status)
		if p.Total > 0 {
			fmt.Printf(" %d/%d", p.Completed, p.Total)
		}
		if p.CostUSD > 0 {
			fmt.Printf("  $%.2f", p.CostUSD)
		}
		if p.LastRunID != "" {
			fmt.Printf("  ·  last %s (%s)", p.LastRunID, p.LastRun)
		} else {
			fmt.Print("  ·  never run")
		}
		fmt.Println()
	}
}
