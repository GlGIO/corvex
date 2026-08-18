package cmd

import (
	"fmt"
	"time"

	"github.com/giovannialves/corvex/internal/ops"
)

// The one-step view: the plan for a step and everything the ledger recorded
// about it — what `logs` and `inspect --task` used to answer separately.
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
