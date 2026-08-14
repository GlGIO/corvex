package cmd

import (
	"fmt"
	"strings"

	"github.com/giovannialves/corvex/internal/ops"
)

// statusOutput is the stable JSON shape for corvex status --json.
type statusOutput struct {
	Project string       `json:"project"`
	Total   int          `json:"total"`
	Passed  int          `json:"passed"`
	Failed  int          `json:"failed"`
	Pending int          `json:"pending"`
	Tasks   []statusTask `json:"tasks"`
}

// statusTask is the per-task entry within statusOutput.
type statusTask struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	DependsOn []string `json:"dependsOn"`
}

// buildStatusOutput projects a ProjectView onto the JSON shape, walking tasks in
// dependency order and normalising a nil dependency list to [] so the field
// never marshals as null.
func buildStatusOutput(view *ops.ProjectView) statusOutput {
	out := statusOutput{
		Project: view.Project,
		Total:   len(view.Tasks),
		Passed:  view.Passed,
		Failed:  view.Failed,
		Pending: view.Pending,
		Tasks:   make([]statusTask, 0, len(view.Tasks)),
	}
	for _, id := range view.Order {
		t := view.ByID[id]
		if t == nil {
			continue
		}
		deps := t.DependsOn
		if deps == nil {
			deps = []string{}
		}
		out.Tasks = append(out.Tasks, statusTask{
			ID:        t.ID,
			Title:     t.Title,
			Status:    string(t.Status),
			DependsOn: deps,
		})
	}
	return out
}

// printStatusTable renders the human view: a header, one padded line per task in
// dependency order, and a next-step footer.
//
// The title truncation below slices bytes, not runes, so a title cut mid-rune
// prints a replacement char — and the %-*s padding counts runes, not terminal
// cells, so accented titles come out one cell short. Both are frozen behaviour
// (see f-1-anomalias.md); the golden files record them on purpose.
func printStatusTable(view *ops.ProjectView) {
	fmt.Printf("Project: %s\n", view.Project)
	if view.Intent != "" {
		fmt.Printf("Intent:  %s\n", view.Intent)
	}
	fmt.Printf("Tasks:   %d/%d done\n\n", view.Passed, len(view.Tasks))

	maxIDLen := 0
	maxTitleLen := 0
	for _, t := range view.Tasks {
		if len(t.ID) > maxIDLen {
			maxIDLen = len(t.ID)
		}
		if len(t.Title) > maxTitleLen {
			maxTitleLen = len(t.Title)
		}
	}
	if maxTitleLen > 40 {
		maxTitleLen = 40
	}

	for _, id := range view.Order {
		t := view.ByID[id]
		if t == nil {
			continue
		}

		emoji := statusEmoji(t.Status)
		title := t.Title
		if len(title) > maxTitleLen {
			title = title[:maxTitleLen-1] + "…"
		}

		deps := ""
		if len(t.DependsOn) > 0 {
			deps = fmt.Sprintf(" ← [%s]", strings.Join(t.DependsOn, ", "))
		}

		fmt.Printf("  %s %-*s — %-*s%s\n", emoji, maxIDLen, t.ID, maxTitleLen, title, deps)
	}

	fmt.Println()
	if view.Pending > 0 {
		fmt.Printf("%d task(s) pending — run: corvex run %s\n", view.Pending, view.Project)
	} else if len(view.Tasks) > 0 {
		fmt.Println("All tasks complete.")
	}
}
