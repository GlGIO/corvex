package cmd

import (
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
	inspectJSON *bool
)

// inspectOutput is the JSON shape emitted by `corvex inspect <project> --json`.
type inspectOutput struct {
	Project      string            `json:"project"`
	Intent       string            `json:"intent,omitempty"`
	Total        int               `json:"total"`
	Completed    int               `json:"completed"`
	TotalCostUSD float64           `json:"totalCostUSD"`
	Tasks        []inspectTaskStat `json:"tasks"`
}

// inspectTaskStat carries per-task metrics aggregated from the activity ledger.
type inspectTaskStat struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Status     string  `json:"status"`
	DurationMs int64   `json:"durationMs"`
	Retries    int     `json:"retries"`
	CostUSD    float64 `json:"costUSD"`
	TokensIn   int     `json:"tokensIn"`
	TokensOut  int     `json:"tokensOut"`
}

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
	inspectJSON = addJSONFlag(inspectCmd)
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

	if *inspectJSON {
		if inspectTask != "" {
			filtered := make([]activity.Entry, 0)
			for _, e := range entries {
				if e.TaskID == inspectTask {
					filtered = append(filtered, e)
				}
			}
			return printJSON(os.Stdout, filtered)
		}
		out, err := buildInspectData(workDir, project, entries)
		if err != nil {
			return err
		}
		return printJSON(os.Stdout, out)
	}

	if inspectTask != "" {
		return printTaskDetail(entries, inspectTask)
	}

	return printSummary(workDir, project, entries)
}

// buildInspectData aggregates activity entries and task metadata into the
// inspectOutput shape used by both --json and the human summary renderer.
func buildInspectData(workDir, project string, entries []activity.Entry) (inspectOutput, error) {
	tasksPath := filepath.Join(projectDir(workDir, project), "tasks.md")
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return inspectOutput{}, fmt.Errorf("reading tasks: %w", err)
	}

	anchorPath := filepath.Join(projectDir(workDir, project), "anchor.yaml")
	anchorState, _ := anchor.Load(anchorPath)

	type stat struct {
		taskID     string
		status     types.TaskStatus
		title      string
		durationMs int64
		retries    int
		costUSD    float64
		tokensIn   int
		tokensOut  int
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
			s.durationMs = e.DurationMs
			s.costUSD = e.CostUSD
			s.tokensIn = e.TokensIn
			s.tokensOut = e.TokensOut
		case "retry":
			s.retries++
		}
	}

	ids := make([]string, 0, len(statsByID))
	for id := range statsByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var totalCost float64
	completed := 0
	statList := make([]inspectTaskStat, 0, len(ids))
	for _, id := range ids {
		s := statsByID[id]
		totalCost += s.costUSD
		if s.status == types.StatusPassed {
			completed++
		}
		statList = append(statList, inspectTaskStat{
			ID:         s.taskID,
			Title:      s.title,
			Status:     string(s.status),
			DurationMs: s.durationMs,
			Retries:    s.retries,
			CostUSD:    s.costUSD,
			TokensIn:   s.tokensIn,
			TokensOut:  s.tokensOut,
		})
	}

	return inspectOutput{
		Project:      project,
		Intent:       anchorState.Intent,
		Total:        len(tasks),
		Completed:    completed,
		TotalCostUSD: totalCost,
		Tasks:        statList,
	}, nil
}

func printSummary(workDir, project string, entries []activity.Entry) error {
	out, err := buildInspectData(workDir, project, entries)
	if err != nil {
		return err
	}

	fmt.Printf("Project: %s\n", out.Project)
	if out.Intent != "" {
		fmt.Printf("Intent:  %s\n", out.Intent)
	}
	fmt.Printf("Tasks:   %d/%d done   ·   $%.2f total\n\n", out.Completed, out.Total, out.TotalCostUSD)

	fmt.Printf("%-5s %-8s %-10s %-7s %-8s %s\n", "ID", "STATUS", "DURATION", "RETRIES", "COST", "TITLE")
	for _, s := range out.Tasks {
		statusGlyph := glyphFor(types.TaskStatus(s.Status))
		dur := "—"
		if s.DurationMs > 0 {
			dur = humanDuration(time.Duration(s.DurationMs) * time.Millisecond)
		}
		cost := "—"
		if s.CostUSD > 0 {
			cost = fmt.Sprintf("$%.2f", s.CostUSD)
		}
		title := s.Title
		if len(title) > 50 {
			title = title[:49] + "…"
		}
		fmt.Printf("%-5s %-8s %-10s %-7d %-8s %s\n", s.ID, statusGlyph, dur, s.Retries, cost, title)
	}

	if len(out.Tasks) > 0 {
		slowest := make([]inspectTaskStat, len(out.Tasks))
		copy(slowest, out.Tasks)
		sort.Slice(slowest, func(i, j int) bool { return slowest[i].DurationMs > slowest[j].DurationMs })
		expensive := make([]inspectTaskStat, len(out.Tasks))
		copy(expensive, out.Tasks)
		sort.Slice(expensive, func(i, j int) bool { return expensive[i].CostUSD > expensive[j].CostUSD })

		fmt.Println()
		if slowest[0].DurationMs > 0 {
			fmt.Print("Slowest:    ")
			for i, s := range slowest[:min(3, len(slowest))] {
				if s.DurationMs == 0 {
					break
				}
				if i > 0 {
					fmt.Print(", ")
				}
				fmt.Printf("%s (%s)", s.ID, humanDuration(time.Duration(s.DurationMs)*time.Millisecond))
			}
			fmt.Println()
		}
		if expensive[0].CostUSD > 0 {
			fmt.Print("Expensive:  ")
			for i, s := range expensive[:min(3, len(expensive))] {
				if s.CostUSD == 0 {
					break
				}
				if i > 0 {
					fmt.Print(", ")
				}
				fmt.Printf("%s ($%.2f)", s.ID, s.CostUSD)
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
