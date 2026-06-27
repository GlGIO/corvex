package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var listJSON *bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available projects",
	Long:  "List all project directories in .corvex/tasks/.",
	Args:  cobra.NoArgs,
	RunE:  runList,
}

// listProject is the stable JSON shape for a single project entry.
type listProject struct {
	Name     string `json:"name"`
	HasSpec  bool   `json:"hasSpec"`
	HasTasks bool   `json:"hasTasks"`
	Status   string `json:"status"`
}

func init() {
	listJSON = addJSONFlag(listCmd)
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
	tasksDir := filepath.Join(workDir, ".corvex", "tasks")

	projects := make([]listProject, 0, len(names))
	for _, name := range names {
		hasSpec := false
		hasTasks := false

		if _, err := os.Stat(filepath.Join(tasksDir, name, "spec.md")); err == nil {
			hasSpec = true
		}
		if _, err := os.Stat(filepath.Join(tasksDir, name, "tasks.md")); err == nil {
			hasTasks = true
		}

		status := "no spec"
		if hasSpec && hasTasks {
			status = "ready"
		} else if hasSpec {
			status = "needs planning"
		}

		projects = append(projects, listProject{Name: name, HasSpec: hasSpec, HasTasks: hasTasks, Status: status})
	}

	if *listJSON {
		return printJSON(os.Stdout, projects)
	}

	if len(projects) == 0 {
		fmt.Println("No projects found. Create a spec in .corvex/tasks/<project>/spec.md")
		return nil
	}

	for _, p := range projects {
		fmt.Printf("  %s (%s)\n", p.Name, p.Status)
	}

	return nil
}
