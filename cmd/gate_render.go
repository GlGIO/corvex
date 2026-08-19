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

// renderGateAudit prints the sensor over the sensors: per-gate latency, verdict
// mix, and the gap between reading the evidence and deciding.
//
// The judgement lives HERE and not in the data. ops.GateAudit carries no
// `theatre` field on purpose (see its doc): "approved in four seconds" is the
// roadmap's policy line, and a policy compiled into a JSON field becomes a fact
// that outlives the argument for it. So the threshold arrives in argv
// (--suspect-under) and the accusation is printed prose, next to the counts that
// support it.
//
// Every number is followed by its population. With one decided gate on disk in
// the whole repository, a median with no `n` beside it is a lie of composition.
func renderGateAudit(audit ops.GateAudit, suspectUnder time.Duration) {
	if len(audit.Rows) == 0 {
		fmt.Println(emptyGateAudit(audit))
		return
	}
	decided, open := 0, 0
	for _, r := range audit.Rows {
		if r.Verdict == "" {
			open++
			continue
		}
		decided++
	}
	fmt.Printf("%d gate(s) in %s%s  ·  %d decided, %d open\n\n",
		len(audit.Rows), countOf(audit.Repos, "repository", "repositories"), auditWindow(audit.Since), decided, open)

	for _, g := range audit.Groups {
		fmt.Printf("  %s\n", g.Key)
		if g.Decided == 0 {
			// A gate nobody has answered yet has no distribution, and printing
			// four rows of "—" for it would bury the gates that do.
			fmt.Printf("      nothing decided yet   open %d\n\n", g.Open)
			continue
		}
		fmt.Printf("      decided %d   approved %d · rejected %d · expired %d   open %d\n",
			g.Decided, g.Approved, g.Rejected, g.Expired, g.Open)
		if g.RejectionRate == nil {
			// Every expiry, and no judgement at all. Naming it as such is the
			// point: "0% rejected" would be the flattering way to say this.
			fmt.Printf("      rejection rate —   nobody judged; %d expired, and an expiry is not an answer\n\n",
				g.Expired)
			continue
		}
		// Expiry is named on this line every time. The one thing a reader must
		// not conclude from a rejection rate is that a gate nobody answered was
		// answered strictly.
		fmt.Printf("      rejection rate %.0f%%  (%d judged; %d expired, outside the rate)\n",
			*g.RejectionRate*100, g.Judged, g.Expired)
		fmt.Printf("      latency    min %s · median %s · max %s  (n=%d)\n",
			optMs(g.LatencyMsMin), optMs(g.LatencyMsMedian), optMs(g.LatencyMsMax), g.Judged)
		if g.LatencyMsP95 != nil {
			fmt.Printf("      latency    p95 %s\n", optMs(g.LatencyMsP95))
		} else {
			fmt.Printf("      latency    p95 withheld — %d judged, %d needed for a percentile to mean anything\n",
				g.Judged, ops.P95MinSample)
		}
		fmt.Printf("      read gap   median %s  (measured on %d of %d; %d decided in the same breath)\n",
			optMs(g.ReadGapMsMedian), g.ReadGapMeasured, g.Judged, g.ReadGapZero)
		fmt.Println()
	}

	// The raw rows are always printed, never behind a flag: at this population
	// they are the evidence, and an aggregate over n=1 that hides its sample is
	// how a sensor starts lying.
	fmt.Println("  every gate, smallest read gap first:")
	for _, r := range audit.Rows {
		fmt.Printf("      %s  %-22s %-8s latency %-7s read gap %-7s %s\n",
			r.RunID, r.Key, verdictOrOpen(r.Verdict), optMs(r.LatencyMs), optMs(r.ReadGapMs), r.Label)
	}

	if hint := gateAuditHint(audit, suspectUnder); hint != "" {
		fmt.Printf("\n%s", hint)
	}
}

// countOf is "3 repositories" / "1 repository" — the count and its noun, unlike
// plural(), which returns only the suffix.
func countOf(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func emptyGateAudit(audit ops.GateAudit) string {
	where := countOf(audit.Repos, "repository", "repositories")
	if audit.Since <= 0 {
		return fmt.Sprintf("No gates recorded in %s.", where)
	}
	return fmt.Sprintf("No gates opened in %s%s (--all for every gate ever recorded).", where, auditWindow(audit.Since))
}

func auditWindow(since time.Duration) string {
	if since <= 0 {
		return ""
	}
	return ", last " + humanWait(since)
}

// gateAuditHint is the only place the "four seconds" policy is applied, and it
// applies it to two DIFFERENT facts kept apart on purpose: how fast the decision
// came, and whether reading and deciding were one act. A fast decision on a gate
// with no required reading is not evidence of a stamp; a zero read gap is.
func gateAuditHint(audit ops.GateAudit, suspectUnder time.Duration) string {
	fast, zeroGap := 0, 0
	for _, r := range audit.Rows {
		if !r.Judged() {
			continue
		}
		if suspectUnder > 0 && r.LatencyMs != nil && *r.LatencyMs < suspectUnder.Milliseconds() {
			fast++
		}
		if r.ReadGapMs != nil && *r.ReadGapMs == 0 {
			zeroGap++
		}
	}
	if fast == 0 && zeroGap == 0 {
		return ""
	}
	var b strings.Builder
	if fast > 0 {
		fmt.Fprintf(&b, "  %d judged in under %s (--suspect-under).\n", fast, humanWait(suspectUnder))
	}
	if zeroGap > 0 {
		fmt.Fprintf(&b, "  %d read and decided in the same command: a zero gap is one keystroke,\n"+
			"  because an inline --ack is dated at the decision itself.\n", zeroGap)
	}
	b.WriteString("  A gate that always reads this way is not a control. Automate it, or delete it —\n" +
		"  a stamp costs a person's attention and buys nothing.\n")
	return b.String()
}

// optMs prints a measured value or an explicit absence. Absence is NOT zero
// here: a gate with no required reading has no gap, and printing 0ms for it
// would manufacture the exact finding this screen exists to report.
func optMs(ms *int64) string {
	if ms == nil {
		return "—"
	}
	return humanMs(*ms)
}

// humanMs keeps milliseconds below a second, because the whole question is
// whether the number is exactly zero — humanWait rounds 0ms and 400ms to the
// same "0s" and erases the distinction between a keystroke and a glance.
func humanMs(ms int64) string {
	if ms < 0 {
		// Two stamps on one file that disagree: different machines, different
		// clocks. Printed as measured rather than clamped to 0.
		return "-" + humanMs(-ms)
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return humanWait(time.Duration(ms) * time.Millisecond)
}

func verdictOrOpen(v gate.Verdict) string {
	if v == "" {
		return "open"
	}
	return string(v)
}
