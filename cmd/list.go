package cmd

import (
	"fmt"
	"os"

	"github.com/giovannialves/corvex/internal/ops"
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
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}

	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	summaries := ops.ListProjects(workDir)

	projects := make([]listProject, 0, len(summaries))
	for _, s := range summaries {
		projects = append(projects, listProject{
			Name: s.Name, HasSpec: s.HasSpec, HasTasks: s.HasTasks, Status: s.Status,
		})
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
