package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status <project>",
	Short: "Show DAG status and progress",
	Long:  "Display the task DAG with current status, completion, and dependencies.",
	Args:  cobra.ExactArgs(1),
	RunE:  runStatus,
}

func init() {
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

	passed := 0
	for _, t := range tasks {
		if t.Status == types.StatusPassed {
			passed++
		}
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
	return nil
}
