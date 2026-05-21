package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var (
	inspectTask string
	inspectJSON bool
)

var inspectCmd = &cobra.Command{
	Use:   "inspect <project>",
	Short: "Show per-task timeline, duration, retries, cost from the activity ledger",
	Long: `Read .corvex/tasks/<project>/activity.jsonl and render a timeline:
duration, retry count, cost, and tokens for each task. Use --task to drill
into a single task's event stream, or --json for raw output.`,
	Args: cobra.ExactArgs(1),
	RunE: runInspect,
}

func init() {
	inspectCmd.Flags().StringVar(&inspectTask, "task", "", "show detailed events for a single task ID (e.g. S05)")
	inspectCmd.Flags().BoolVar(&inspectJSON, "json", false, "print raw JSON instead of a formatted table")
	rootCmd.AddCommand(inspectCmd)
}

func runInspect(_ *cobra.Command, args []string) error {
	project := args[0]

	_, workDir, err := loadConfig()
	if err != nil {
		return err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	entries, err := activity.Read(workDir, project)
	if err != nil {
		return fmt.Errorf("reading activity ledger: %w", err)
	}
	if len(entries) == 0 {
		fmt.Printf("No activity recorded for project %q yet. Run `corvex run %s` first.\n", project, project)
		return nil
	}

	if inspectJSON {
		filtered := entries
		if inspectTask != "" {
			filtered = filtered[:0]
			for _, e := range entries {
				if e.TaskID == inspectTask {
					filtered = append(filtered, e)
				}
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(filtered)
	}

	if inspectTask != "" {
		return printTaskDetail(entries, inspectTask)
	}

	return printSummary(workDir, project, entries)
}

func printSummary(workDir, project string, entries []activity.Entry) error {
	// Load tasks.md + anchor for current statuses (ledger only has events).
	tasksPath := filepath.Join(projectDir(workDir, project), "tasks.md")
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return fmt.Errorf("reading tasks: %w", err)
	}

	anchorPath := filepath.Join(projectDir(workDir, project), "anchor.yaml")
	anchorState, _ := anchor.Load(anchorPath)

	type stat struct {
		taskID    string
		status    types.TaskStatus
		title     string
		duration  time.Duration
		retries   int
		costUSD   float64
		tokensIn  int
		tokensOut int
	}

	statsByID := map[string]*stat{}
	for _, t := range tasks {
		statsByID[t.ID] = &stat{taskID: t.ID, status: t.Status, title: t.Title}
	}

	for _, e := range entries {
		s, ok := statsByID[e.TaskID]
		if !ok {
			continue
		}
		switch e.Type {
		case "task_complete":
			s.duration = time.Duration(e.DurationMs) * time.Millisecond
			s.costUSD = e.CostUSD
			s.tokensIn = e.TokensIn
			s.tokensOut = e.TokensOut
		case "retry":
			s.retries++
		}
	}

	// Render summary header.
	var totalCost float64
	completed := 0
	for _, s := range statsByID {
		totalCost += s.costUSD
		if s.status == types.StatusPassed {
			completed++
		}
	}

	fmt.Printf("Project: %s\n", project)
	if anchorState.Intent != "" {
		fmt.Printf("Intent:  %s\n", anchorState.Intent)
	}
	fmt.Printf("Tasks:   %d/%d done   ·   $%.2f total\n\n", completed, len(tasks), totalCost)

	// Render per-task table sorted by ID.
	ids := make([]string, 0, len(statsByID))
	for id := range statsByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	fmt.Printf("%-5s %-8s %-10s %-7s %-8s %s\n", "ID", "STATUS", "DURATION", "RETRIES", "COST", "TITLE")
	for _, id := range ids {
		s := statsByID[id]
		statusGlyph := glyphFor(s.status)
		dur := "—"
		if s.duration > 0 {
			dur = humanDuration(s.duration)
		}
		cost := "—"
		if s.costUSD > 0 {
			cost = fmt.Sprintf("$%.2f", s.costUSD)
		}
		title := s.title
		if len(title) > 50 {
			title = title[:49] + "…"
		}
		fmt.Printf("%-5s %-8s %-10s %-7d %-8s %s\n", id, statusGlyph, dur, s.retries, cost, title)
	}

	// Highlights — slowest and most expensive.
	if len(statsByID) > 0 {
		all := make([]*stat, 0, len(statsByID))
		for _, s := range statsByID {
			all = append(all, s)
		}

		slowest := make([]*stat, len(all))
		copy(slowest, all)
		sort.Slice(slowest, func(i, j int) bool { return slowest[i].duration > slowest[j].duration })
		expensive := make([]*stat, len(all))
		copy(expensive, all)
		sort.Slice(expensive, func(i, j int) bool { return expensive[i].costUSD > expensive[j].costUSD })

		fmt.Println()
		if len(slowest) > 0 && slowest[0].duration > 0 {
			fmt.Print("Slowest:    ")
			for i, s := range slowest[:min(3, len(slowest))] {
				if s.duration == 0 {
					break
				}
				if i > 0 {
					fmt.Print(", ")
				}
				fmt.Printf("%s (%s)", s.taskID, humanDuration(s.duration))
			}
			fmt.Println()
		}
		if len(expensive) > 0 && expensive[0].costUSD > 0 {
			fmt.Print("Expensive:  ")
			for i, s := range expensive[:min(3, len(expensive))] {
				if s.costUSD == 0 {
					break
				}
				if i > 0 {
					fmt.Print(", ")
				}
				fmt.Printf("%s ($%.2f)", s.taskID, s.costUSD)
			}
			fmt.Println()
		}
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func printTaskDetail(entries []activity.Entry, taskID string) error {
	first := true
	for _, e := range entries {
		if e.TaskID != taskID {
			continue
		}
		if first {
			fmt.Printf("Events for %s:\n\n", taskID)
			first = false
		}
		ts := e.Timestamp.Local().Format("15:04:05")
		extra := ""
		switch {
		case e.Status != "":
			extra = fmt.Sprintf(" [%s]", e.Status)
		case e.Message != "":
			extra = " " + e.Message
		}
		dur := ""
		if e.DurationMs > 0 {
			dur = fmt.Sprintf(" %s", humanDuration(time.Duration(e.DurationMs)*time.Millisecond))
		}
		cost := ""
		if e.CostUSD > 0 {
			cost = fmt.Sprintf(" $%.2f", e.CostUSD)
		}
		fmt.Printf("  %s  %-18s%s%s%s\n", ts, e.Type, extra, dur, cost)
	}
	if first {
		fmt.Printf("No events for task %s in the activity ledger.\n", taskID)
	}
	return nil
}

func glyphFor(s types.TaskStatus) string {
	switch s {
	case types.StatusPassed:
		return "✅"
	case types.StatusRunning:
		return "🔄"
	case types.StatusFailed:
		return "❌"
	case types.StatusSkipped:
		return "⏭"
	default:
		return "⬜"
	}
}

func humanDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) - mins*60
	return fmt.Sprintf("%dm%02ds", mins, secs)
}

