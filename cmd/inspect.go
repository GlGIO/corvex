package cmd

import (
	"fmt"
	"os"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

var (
	inspectTask string
	inspectJSON *bool
)

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

	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	entries, err := ops.ReadActivityLedger(workDir, project)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Printf("No activity recorded for project %q yet. Run `corvex run %s` first.\n", project, project)
		return nil
	}

	if *inspectJSON {
		if inspectTask != "" {
			return printJSON(os.Stdout, ops.FilterActivityByTask(entries, inspectTask))
		}
		report, err := ops.BuildInspectReport(workDir, project, entries)
		if err != nil {
			return err
		}
		return printJSON(os.Stdout, report)
	}

	if inspectTask != "" {
		return printTaskDetail(entries, inspectTask)
	}

	report, err := ops.BuildInspectReport(workDir, project, entries)
	if err != nil {
		return err
	}
	return printSummary(report)
}
