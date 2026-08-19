package cmd

import (
	"fmt"
	"strings"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:               "logs <project> [task]",
	Short:             "Show task details and completion info",
	Long:              "Display task description, criteria, files, and completion summary from anchor.",
	Args:              cobra.RangeArgs(1, 2),
	ValidArgsFunction: completeProjectArg,
	RunE:              runLogs,
}

func init() {
	rootCmd.AddCommand(logsCmd)
}

// A run id handed to `logs` is the third symptom of the same dogfood failure.
//
// An operator whose stage failed has a run id in hand — it is what `--plain`
// and `run list` print — so `corvex logs run_8a2c` is the obvious next thing to
// type. It used to answer
// "parsing tasks .../.corvex/tasks/run_8a2c/tasks.md: no such file or
// directory": a filesystem path for a well-formed id, which reads as corruption
// and is really a category error. `logs` is project-scoped by contract (it
// prints every task of one project from its anchor) and it has been deprecated
// since F3, so teaching it to resolve run ids would widen a command that is on
// its way out — and a run id can name a run in another repository entirely,
// which this command has no way to open.
//
// So it refuses, and names the command that does take a run id. One hop instead
// of a dead end, and the deprecation keeps pointing one way.
func runIDNotAProject(arg string) error {
	return fmt.Errorf("%q is a run id, not a project — try `corvex run show %s --step <id>`", arg, arg)
}

func runLogs(_ *cobra.Command, args []string) error {
	project := args[0]
	if ops.IsRunID(project) {
		return runIDNotAProject(project)
	}

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

	if len(args) == 2 {
		return showTaskLog(view, strings.ToUpper(args[1]))
	}

	for i, t := range view.Tasks {
		if i > 0 {
			fmt.Println("---")
			fmt.Println()
		}
		if err := showTaskLog(view, t.ID); err != nil {
			return err
		}
	}

	return nil
}

// showTaskLog renders one task: its heading, description, criteria, touched
// files, and whatever the anchor recorded when it completed.
func showTaskLog(view *ops.ProjectView, taskID string) error {
	t, err := ops.FindTask(view.Tasks, taskID)
	if err != nil {
		return err
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

	if c, ok := view.Completed[t.ID]; ok {
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
