package cmd

import (
	"fmt"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/ops"
)

// printTaskDetail renders the event stream of a single task for
// `corvex inspect <project> --task <id>`.
func printTaskDetail(entries []activity.Entry, taskID string) error {
	events := ops.FilterActivityByTask(entries, taskID)
	if len(events) == 0 {
		fmt.Printf("No events for task %s in the activity ledger.\n", taskID)
		return nil
	}

	fmt.Printf("Events for %s:\n\n", taskID)
	for _, e := range events {
		fmt.Printf("  %s  %-18s%s%s%s\n",
			e.Timestamp.Local().Format("15:04:05"), e.Type,
			eventExtra(e), eventDuration(e), eventCost(e))
	}
	return nil
}

// eventExtra picks the one annotation an event carries: its status, or failing
// that its message.
func eventExtra(e activity.Entry) string {
	switch {
	case e.Status != "":
		return fmt.Sprintf(" [%s]", e.Status)
	case e.Message != "":
		return " " + e.Message
	}
	return ""
}

func eventDuration(e activity.Entry) string {
	if e.DurationMs > 0 {
		return fmt.Sprintf(" %s", humanDuration(msDuration(e.DurationMs)))
	}
	return ""
}

func eventCost(e activity.Entry) string {
	if e.CostUSD > 0 {
		return fmt.Sprintf(" $%.2f", e.CostUSD)
	}
	return ""
}
