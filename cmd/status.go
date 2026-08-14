package cmd

import (
	"os"

	"github.com/giovannialves/corvex/internal/ops"
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

func init() {
	statusJSON = addJSONFlag(statusCmd)
	rootCmd.AddCommand(statusCmd)
}

func runStatus(_ *cobra.Command, args []string) error {
	project := args[0]

	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}

	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	view, err := ops.ReadProject(workDir, project)
	if err != nil {
		return err
	}

	if statusJSON != nil && *statusJSON {
		return printJSON(os.Stdout, buildStatusOutput(view))
	}

	printStatusTable(view)
	return nil
}
