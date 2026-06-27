package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available projects",
	Long:  "List all project directories in .corvex/tasks/.",
	Args:  cobra.NoArgs,
	RunE:  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func runList(_ *cobra.Command, _ []string) error {
	_, workDir, err := loadConfig()
	if err != nil {
		return err
	}

	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	names := projectNames(workDir)
	if len(names) == 0 {
		fmt.Println("No projects found. Create a spec in .corvex/tasks/<project>/spec.md")
		return nil
	}

	tasksDir := filepath.Join(workDir, ".corvex", "tasks")
	for _, name := range names {
		specExists := false
		tasksExist := false

		if _, err := os.Stat(filepath.Join(tasksDir, name, "spec.md")); err == nil {
			specExists = true
		}
		if _, err := os.Stat(filepath.Join(tasksDir, name, "tasks.md")); err == nil {
			tasksExist = true
		}

		status := "no spec"
		if specExists && tasksExist {
			status = "ready"
		} else if specExists {
			status = "needs planning"
		}

		fmt.Printf("  %s (%s)\n", name, status)
	}

	return nil
}
