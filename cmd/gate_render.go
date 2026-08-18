package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/types"
)

// renderInbox prints canvas 2a: everything that stopped and wants a person,
// gates first because a gate has a live process waiting on the answer while an
// escalation has already failed and will wait for ever.
func renderInbox(inbox ops.Inbox) {
	if inbox.Empty() {
		fmt.Println("Nothing is waiting on you.")
		return
	}
	renderGateList(inbox.Gates)
	if len(inbox.Escalations) == 0 {
		return
	}
	fmt.Printf("%d escalation(s) waiting:\n\n", len(inbox.Escalations))
	for _, e := range inbox.Escalations {
		fmt.Printf("  %s  step %s  ·  waiting %s\n", e.Project, e.Step, humanWait(e.Waiting))
		for _, line := range e.Head {
			fmt.Printf("      %s\n", line)
		}
		fmt.Printf("      → corvex gate show %s --step %s\n\n", e.Project, e.Step)
	}
}

// renderGateList prints the gate half of the inbox, oldest first.
func renderGateList(gates []ops.GateView) {
	if len(gates) == 0 {
		return
	}
	fmt.Printf("%d gate(s) waiting:\n\n", len(gates))
	for _, g := range gates {
		fmt.Printf("  %s  %s\n", g.Gate.RunID, g.Gate.Describe())
		fmt.Printf("      step %s", g.Gate.StepID)
		if g.Gate.Project != "" {
			fmt.Printf("  ·  project %s", g.Gate.Project)
		}
		if g.Gate.Recipe != "" {
			fmt.Printf("  ·  recipe %s", g.Gate.Recipe)
		}
		fmt.Printf("  ·  waiting %s\n", humanWait(g.Waiting))
		if required := gate.RequiredLabels(g.Gate.Evidence); len(required) > 0 {
			fmt.Printf("      required reading: %s\n", strings.Join(required, ", "))
		}
		fmt.Printf("      → corvex gate show %s --step %s\n\n", g.Gate.RunID, g.Gate.StepID)
	}
}

// renderGateDetail prints one gate with its evidence. This is the screen the
// approval lock depends on: the --ack labels are only discoverable here.
func renderGateDetail(g ops.GateView) {
	fmt.Printf("%s  ·  step %s  ·  %s\n", g.Gate.RunID, g.Gate.StepID, g.Gate.Describe())
	if g.Gate.Title != "" {
		fmt.Printf("%s\n", g.Gate.Title)
	}
	if g.Gate.Prompt != "" {
		fmt.Printf("\n%s\n", g.Gate.Prompt)
	}
	fmt.Printf("\nrun is %s · waiting %s", g.Liveness, humanWait(g.Waiting))
	if g.Gate.ExpiresAt != nil {
		fmt.Printf(" · expires %s", g.Gate.ExpiresAt.Format(time.RFC3339))
	}
	fmt.Println()

	for _, e := range g.Gate.Evidence {
		fmt.Printf("\n── %s  [%s/%s]", e.Label, e.Kind, statusOrNeutral(e.Status))
		if e.RequiredReading {
			fmt.Print("  ★ required reading")
		}
		if e.Truncated {
			fmt.Print("  (truncated)")
		}
		// Reading state, only when there is some: a gate nobody has opened yet
		// must read exactly as it did before F5 gave it memory.
		if mark, ok := g.Gate.ReadOf(e.Label); ok {
			fmt.Printf("  ✓ read %s", mark.At.Format(time.RFC3339))
		}
		fmt.Println()
		if body := strings.TrimSpace(e.Content); body != "" {
			fmt.Println(body)
		}
	}

	// What is still owed, not what was ever required: naming an item somebody
	// already read would train them to retype acknowledgements, which is the
	// habit that makes the lock a formality.
	required := gate.RequiredLabels(g.Gate.Evidence)
	missing := g.Gate.MissingReading(nil)
	switch {
	case len(missing) > 0:
		fmt.Printf("\nTo approve, acknowledge each starred item:\n  corvex gate approve %s --step %s",
			g.Gate.RunID, g.Gate.StepID)
		for _, label := range missing {
			fmt.Printf(" --ack %q", label)
		}
		fmt.Println()
	case len(required) > 0:
		fmt.Printf("\nAll required reading acknowledged.\n  corvex gate approve %s --step %s\n",
			g.Gate.RunID, g.Gate.StepID)
	default:
		fmt.Printf("\n  corvex gate approve %s --step %s\n", g.Gate.RunID, g.Gate.StepID)
	}
}

func statusOrNeutral(s types.EvidenceStatus) types.EvidenceStatus {
	if s == "" {
		return "—"
	}
	return s
}

// humanWait is a coarse duration. A gate parked for three days should read as
// three days, not as 76h13m4.2s.
func humanWait(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
