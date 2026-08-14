package cmd

import (
	"fmt"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:   "review [project]",
	Short: "List pending escalations awaiting human review",
	Long: `Show open escalations written by the Reviewer when a task has repeatedly
failed with the same category. Each entry is a markdown file in
.corvex/escalations/. Resolve the underlying issue, delete the file, and
re-run "corvex run" to retry the task.

If a project name is given, only escalations for that project are shown.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runReview,
}

func init() {
	rootCmd.AddCommand(reviewCmd)
}

func runReview(_ *cobra.Command, args []string) error {
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}

	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	filter := ""
	if len(args) == 1 {
		filter = args[0]
	}

	list, err := ops.ListEscalations(workDir, filter)
	if err != nil {
		return err
	}

	if len(list.Items) == 0 {
		// A filter that matched nothing says so; a workspace that never
		// escalated anything gets the generic line, even with a filter.
		if filter != "" && list.Exists {
			fmt.Printf("No pending escalations for project %q.\n", filter)
		} else {
			fmt.Println("No pending escalations.")
		}
		return nil
	}

	fmt.Printf("Pending escalations (%d):\n\n", len(list.Items))
	for _, e := range list.Items {
		fmt.Printf("• %s\n", e.Path)
		// Surface the head of the file so users can triage without opening it.
		for _, line := range e.Head {
			fmt.Printf("    %s\n", line)
		}
		fmt.Println()
	}

	return nil
}
