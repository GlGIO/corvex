package cmd

import (
	"fmt"
	"time"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

// `corvex` with no arguments answers "what is going on", not "what are the
// flags". Somebody typing the bare command is asking the question the gate inbox
// exists for; the flags are one `--help` away and always were.
//
// It is deliberately three reads and no writes — inbox, live runs, recent runs —
// so the answer is instant and safe to type at any moment, including while a run
// is going.
func runRootState(cmd *cobra.Command, _ []string) error {
	lister := ops.RunLister{}
	inbox, inboxErr := (ops.GateLister{}).LoadInbox(optionalWorkspaceDir())
	rows, runsErr := lister.ListRuns(ops.RunListOptions{Since: 7 * 24 * time.Hour})
	if inboxErr != nil && runsErr != nil {
		// Nothing could be read at all: fall back to what the command did
		// before, rather than printing an empty screen that looks like "all
		// quiet".
		return cmd.Help()
	}

	waiting := len(inbox.Gates) + len(inbox.Escalations)
	live := 0
	for _, r := range rows {
		if r.Live() {
			live++
		}
	}

	switch {
	case waiting > 0:
		fmt.Printf("%d thing(s) waiting on you.\n\n", waiting)
		renderInbox(inbox)
	case live > 0:
		fmt.Printf("%d run(s) going, nothing waiting on you.\n\n", live)
	default:
		fmt.Println("Nothing waiting on you, nothing running.")
	}

	if len(rows) > 0 {
		fmt.Printf("\nLast %d day(s):\n", 7)
		for i, r := range rows {
			if i == 3 {
				fmt.Printf("  … %d more  ·  corvex run list\n", len(rows)-3)
				break
			}
			fmt.Printf("  %s  %-9s %-9s %s  (%s ago)\n", r.RunID, r.Status, r.Liveness, r.Label(), humanWait(r.Age))
		}
	}
	fmt.Printf("\n%s\n", nextCommandHint(inbox, rows))
	return nil
}

// nextCommandHint is the roadmap's "suggest the next command by state": one
// line, deterministic, no tokens spent. The order is the order of urgency — a
// gate blocks a live process, an escalation blocks a step, everything else is
// just a place to start.
func nextCommandHint(inbox ops.Inbox, rows []ops.RunRow) string {
	if len(inbox.Gates) > 0 {
		g := inbox.Gates[0].Gate
		return fmt.Sprintf("→ corvex gate show %s --step %s", g.RunID, g.StepID)
	}
	if len(inbox.Escalations) > 0 {
		e := inbox.Escalations[0]
		return fmt.Sprintf("→ corvex gate show %s --step %s", e.Project, e.Step)
	}
	for _, r := range rows {
		if r.Live() {
			return "→ corvex run watch " + r.RunID
		}
	}
	if len(rows) > 0 {
		return "→ corvex run show " + rows[0].RunID
	}
	return "→ corvex run list --projects   (or corvex --help)"
}
