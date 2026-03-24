package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var resetCmd = &cobra.Command{
	Use:   "reset <project> <task>",
	Short: "Reset a task to PENDING status",
	Long:  "Mark a specific task as PENDING so it can be re-executed.",
	Args:  cobra.ExactArgs(2),
	RunE:  runReset,
}

func init() {
	rootCmd.AddCommand(resetCmd)
}

func runReset(_ *cobra.Command, args []string) error {
	project := args[0]
	taskID := strings.ToUpper(args[1])

	_, workDir, err := loadConfig()
	if err != nil {
		return err
	}

	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	pDir := projectDir(workDir, project)
	tasksPath := filepath.Join(pDir, "tasks.md")

	if err := task.UpdateTaskStatus(tasksPath, taskID, types.StatusPending); err != nil {
		return fmt.Errorf("resetting task: %w", err)
	}

	log.Info("task reset", "task", taskID, "status", "PENDING")
	return nil
}
