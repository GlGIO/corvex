package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var statusJSON *bool

var statusCmd = &cobra.Command{
	Use:               "status <project>",
	Short:             "Show DAG status and progress",
	Long:              "Display the task DAG with current status, completion, and dependencies.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeProjectArg,
	RunE:              runStatus,
}

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
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	DependsOn []string   `json:"dependsOn"`
}

func init() {
	statusJSON = addJSONFlag(statusCmd)
	rootCmd.AddCommand(statusCmd)
}

func runStatus(_ *cobra.Command, args []string) error {
	project := args[0]

	_, workDir, err := loadConfig()
	if err != nil {
		return err
	}

	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	pDir := projectDir(workDir, project)
	tasksPath := filepath.Join(pDir, "tasks.md")
	anchorPath := filepath.Join(pDir, "anchor.yaml")

	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return fmt.Errorf("parsing tasks: %w", err)
	}

	anchorState, _ := anchor.Load(anchorPath)

	d := dag.NewDAG(tasks)
	order, err := d.Resolve()
	if err != nil {
		order = make([]string, len(tasks))
		for i, t := range tasks {
			order[i] = t.ID
		}
	}

	taskMap := make(map[string]*types.Task, len(tasks))
	for i := range tasks {
		taskMap[tasks[i].ID] = &tasks[i]
	}

	passed, failed := 0, 0
	for _, t := range tasks {
		switch t.Status {
		case types.StatusPassed:
			passed++
		case types.StatusFailed:
			failed++
		}
	}
	pending := len(tasks) - passed - failed

	if statusJSON != nil && *statusJSON {
		out := statusOutput{
			Project: project,
			Total:   len(tasks),
			Passed:  passed,
			Failed:  failed,
			Pending: pending,
			Tasks:   make([]statusTask, 0, len(tasks)),
		}
		for _, id := range order {
			t := taskMap[id]
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
		return printJSON(os.Stdout, out)
	}

	fmt.Printf("Project: %s\n", project)
	if anchorState.Intent != "" {
		fmt.Printf("Intent:  %s\n", anchorState.Intent)
	}
	fmt.Printf("Tasks:   %d/%d done\n\n", passed, len(tasks))

	maxIDLen := 0
	maxTitleLen := 0
	for _, t := range tasks {
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

	for _, id := range order {
		t := taskMap[id]
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
	if pending > 0 {
		fmt.Printf("%d task(s) pending — run: corvex run %s\n", pending, project)
	} else if len(tasks) > 0 {
		fmt.Println("All tasks complete.")
	}
	return nil
}
