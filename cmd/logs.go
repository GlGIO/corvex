package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs <project> [task]",
	Short: "Show task details and completion info",
	Long:  "Display task description, criteria, files, and completion summary from anchor.",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runLogs,
}

func init() {
	rootCmd.AddCommand(logsCmd)
}

func runLogs(_ *cobra.Command, args []string) error {
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

	completedMap := make(map[string]types.CompletedTask)
	for _, c := range anchorState.Completed {
		completedMap[c.ID] = c
	}

	if len(args) == 2 {
		taskID := strings.ToUpper(args[1])
		return showTaskLog(tasks, completedMap, taskID)
	}

	for i, t := range tasks {
		if i > 0 {
			fmt.Println("---")
			fmt.Println()
		}
		if err := showTaskLog(tasks, completedMap, t.ID); err != nil {
			return err
		}
	}

	return nil
}

func showTaskLog(tasks []types.Task, completedMap map[string]types.CompletedTask, taskID string) error {
	var t *types.Task
	for i := range tasks {
		if tasks[i].ID == taskID {
			t = &tasks[i]
			break
		}
	}

	if t == nil {
		return fmt.Errorf("task %s not found", taskID)
	}

	emoji := statusEmoji(t.Status)
	fmt.Printf("%s %s — %s [%s]\n\n", emoji, t.ID, t.Title, t.Status)

	if t.Description != "" {
		fmt.Printf("Description:\n%s\n\n", t.Description)
	}

	if len(t.Criteria) > 0 {
		fmt.Println("Criteria:")
		for _, c := range t.Criteria {
			check := "[ ]"
			if t.Status == types.StatusPassed {
				check = "[✓]"
			}
			fmt.Printf("  %s %s\n", check, c)
		}
		fmt.Println()
	}

	if len(t.Files.Create) > 0 || len(t.Files.Modify) > 0 {
		fmt.Println("Files:")
		for _, f := range t.Files.Create {
			fmt.Printf("  + %s\n", f)
		}
		for _, f := range t.Files.Modify {
			fmt.Printf("  ~ %s\n", f)
		}
		fmt.Println()
	}

	if c, ok := completedMap[t.ID]; ok {
		if c.Summary != "" {
			fmt.Printf("Summary: %s\n\n", c.Summary)
		}
		if len(c.Decisions) > 0 {
			fmt.Println("Decisions:")
			for _, d := range c.Decisions {
				fmt.Printf("  • %s\n", d)
			}
			fmt.Println()
		}
	}

	return nil
}
