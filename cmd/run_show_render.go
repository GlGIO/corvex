package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/ops"
)

// renderRunReport is the screen F3's D3 merged three commands into: identity,
// then the DAG with per-step metrics, then (with --step) one step in full.
//
// The order is the order of the questions: "which run is this", "where did it
// get to", "what happened in this step". `status` answered the second, `inspect`
// the second and third, `logs` the third — and none of them ever answered the
// first, which is why a user with two runs of one project could not tell them
// apart.
func renderRunReport(r ops.RunReport) {
	renderRunHeader(r)
	if r.Step != nil {
		renderRunStep(*r.Step)
		return
	}
	for _, t := range r.Tasks {
		fmt.Printf("  %s %-5s %s\n", statusEmoji(t.Status), t.ID, t.Title)
		if meta := stepMeta(t); meta != "" {
			fmt.Printf("        %s\n", meta)
		}
	}
	if r.RunID != "" {
		fmt.Printf("\n  → corvex run show %s --step %s\n", r.RunID, firstInterestingStep(r))
	}
}

func renderRunHeader(r ops.RunReport) {
	if r.RunID != "" {
		fmt.Printf("%s  ·  %s", r.RunID, label(r))
		if r.Status != "" {
			fmt.Printf("  ·  %s (%s)", r.Status, r.Liveness)
		}
		fmt.Println()
		if !r.StartedAt.IsZero() {
			fmt.Printf("started %s ago  ·  %s\n", humanWait(time.Since(r.StartedAt)), r.Repo)
		}
		// Only when it is not the default: printing "environment: simple" on
		// every screen would train the eye to skip the line that matters.
		if r.Environment != "" && r.Environment != string(ops.EnvSimple) {
			fmt.Printf("environment: %s\n", r.Environment)
		}
	} else {
		fmt.Printf("%s  ·  never run  ·  %s\n", label(r), r.Repo)
	}
	if r.Scope == ops.ScopeProject && r.RunID != "" {
		// Saying so matters: this screen is the project's whole history, not just
		// the run named in the header.
		fmt.Println("scope: project (every run of it) — pass the run id above for one run")
	}
	if r.Intent != "" {
		fmt.Printf("intent: %s\n", r.Intent)
	}
	fmt.Printf("\n%d/%d steps  ·  $%.2f", r.Completed, r.Total, r.CostUSD)
	// The human clock, apart from the run's clock. A run that took four hours of
	// which three were a person asleep is not a slow run, and before F5 the two
	// numbers were the same number.
	if r.HumanWaitMs > 0 {
		fmt.Printf("  ·  %s waiting on a person", humanWait(time.Duration(r.HumanWaitMs)*time.Millisecond))
	}
	fmt.Println()
	renderPhaseBar(r.PerPhase, r.CostUSD)
	fmt.Println()
}

// renderPhaseBar is the 2f bar in a terminal: where the money went, by the
// nature of the work that spent it. Printed only when the ledger actually
// attributed something — every line written before F5 has no phase, and a
// breakdown of a run that predates the column would be a bar of one bucket
// called "unattributed" pretending to be information.
func renderPhaseBar(phases []ops.PhaseCost, total float64) {
	if len(phases) == 0 || total <= 0 {
		return
	}
	for _, p := range phases {
		if p.CostUSD <= 0 {
			continue
		}
		share := int((p.CostUSD / total) * 20)
		fmt.Printf("  %-13s $%-7.2f %s\n", p.Phase, p.CostUSD, strings.Repeat("█", max(share, 1)))
	}
}

func label(r ops.RunReport) string {
	if r.Recipe != "" {
		return "recipe " + r.Recipe
	}
	return "project " + r.Project
}

// stepMeta is the metrics half of the line: only what happened, so a plan that
// never ran stays quiet instead of printing a row of zeros.
func stepMeta(t ops.RunTaskRow) string {
	parts := make([]string, 0, 4)
	if t.DurationMs > 0 {
		parts = append(parts, humanWait(time.Duration(t.DurationMs)*time.Millisecond))
	}
	if t.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", t.CostUSD))
	}
	if t.Retries > 0 {
		parts = append(parts, fmt.Sprintf("%d retr%s", t.Retries, plural(t.Retries, "y", "ies")))
	}
	if len(t.DependsOn) > 0 {
		parts = append(parts, "after "+strings.Join(t.DependsOn, ", "))
	}
	return strings.Join(parts, "  ·  ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
