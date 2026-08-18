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
	fmt.Printf("\n%d/%d steps  ·  $%.2f\n\n", r.Completed, r.Total, r.CostUSD)
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

func renderRunStep(s ops.RunStepDetail) {
	fmt.Printf("%s %s — %s [%s]\n", statusEmoji(s.Status), s.ID, s.Title, s.Status)
	if meta := stepMeta(s.RunTaskRow); meta != "" {
		fmt.Printf("%s\n", meta)
	}
	if s.Description != "" {
		fmt.Printf("\nDescription:\n%s\n", s.Description)
	}
	if len(s.Criteria) > 0 {
		fmt.Println("\nCriteria:")
		for _, c := range s.Criteria {
			fmt.Printf("  - %s\n", c)
		}
	}
	if len(s.Create) > 0 || len(s.Modify) > 0 {
		fmt.Println("\nFiles:")
		for _, f := range s.Create {
			fmt.Printf("  + %s\n", f)
		}
		for _, f := range s.Modify {
			fmt.Printf("  ~ %s\n", f)
		}
	}
	if s.Summary != "" {
		fmt.Printf("\nSummary: %s\n", s.Summary)
	}
	for _, d := range s.Decisions {
		fmt.Printf("  • %s\n", d)
	}
	if len(s.Events) == 0 {
		fmt.Println("\nNo events recorded for this step.")
		return
	}
	fmt.Println("\nEvents:")
	for _, e := range s.Events {
		fmt.Printf("  %s  %-14s %s\n", e.Timestamp.Format(time.RFC3339), e.Type, e.Message)
	}
}

// firstInterestingStep points the reader at the step worth opening: the one that
// is not finished, or the last one when everything is.
func firstInterestingStep(r ops.RunReport) string {
	for _, t := range r.Tasks {
		if !t.Status.IsTerminal() {
			return t.ID
		}
	}
	if len(r.Tasks) > 0 {
		return r.Tasks[len(r.Tasks)-1].ID
	}
	return "S01"
}
