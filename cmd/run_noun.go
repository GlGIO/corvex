package cmd

import (
	"time"

	"github.com/spf13/cobra"
)

// The `run` noun (F3, §2). `run` was already a command and stays one: F3 kept
// `corvex run <recipe|project>` as a permanent alias for `run start`, because
// invariant 3 promises the legacy path keeps working and because it is the hot
// path.
//
// Cobra resolves a first argument that matches a verb as that verb, and hands
// anything else to the parent's RunE — which is exactly D1: the verb wins, and
// `corvex run start list` is the escape hatch for a project unlucky enough to be
// named after one. noticeVerbShadow prints that escape hatch instead of letting
// the shadowing happen in silence (see run_shadow.go).
var (
	runStartCmd = &cobra.Command{
		Use:               "start <recipe|project>",
		Short:             "Execute pending steps of a recipe or project",
		Long:              "Run the orchestration loop: DAG resolve → Worker → Reviewer → checkpoint → next.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeProjectArg,
		RunE:              runRun,
	}

	runListCmd = &cobra.Command{
		Use:   "list",
		Short: "List runs across every repository",
		Long: "List runs newest first, from the global index. The default window is 7 days and " +
			"every repository; --projects switches the unit of the line to projects of this repository.",
		Args: cobra.NoArgs,
		RunE: runRunList,
	}

	runShowCmd = &cobra.Command{
		Use:   "show <run-id|project>",
		Short: "Show one run: steps, gates, retries and cost",
		Long: "Show a run by id, or a project's current state by name. This is the screen that " +
			"replaces `status`, `inspect` and `logs`: identity, the DAG with per-step metrics, and " +
			"--step for one step's plan and event stream.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeRunArg,
		RunE:              runRunShow,
	}

	runWatchCmd = &cobra.Command{
		Use:               "watch <run-id|project>",
		Short:             "Redraw a run's screen until it finishes",
		Long:              "Poll the run's record and ledger and redraw the `run show` screen until the run reaches a terminal state.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeRunArg,
		RunE:              runRunWatch,
	}

	runRetryCmd = &cobra.Command{
		Use:               "retry <run-id|project>",
		Short:             "Re-execute one step without redoing the rest",
		Long:              "Mark a step PENDING and execute it. This spends money, so it inherits the cost preview and -y.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeRunArg,
		RunE:              runRunRetry,
	}

	runKillCmd = &cobra.Command{
		Use:               "kill <run-id>",
		Short:             "Signal a live run to stop",
		Long:              "Send SIGTERM to a live run of this machine. The run writes `canceling` and unwinds.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeRunArg,
		RunE:              runRunKill,
	}
)

var (
	runListSince    string
	runListRepo     string
	runListStatus   string
	runListLive     bool
	runListProjects bool
	runShowStep     string
	runWatchEvery   time.Duration
	runRetryStep    string
	runRetryNoRun   bool
	runKillNow      bool
	runListJSON,
	runShowJSON *bool
)

func init() {
	addRunFlags(runStartCmd)

	runListCmd.Flags().StringVar(&runListSince, "since", "7d", "only runs started within this window — 90m, 36h, 7d (0 for all)")
	runListCmd.Flags().StringVar(&runListRepo, "repo", "", "only runs of one repository (`--repo .` for the current one)")
	runListCmd.Flags().StringVar(&runListStatus, "status", "", "only runs reporting this status: running, parked, canceling, done, failed, canceled")
	runListCmd.Flags().BoolVar(&runListLive, "live", false, "only runs with a process behind them (the other axis: what a run REPORTS is --status)")
	runListCmd.Flags().BoolVar(&runListProjects, "projects", false, "list projects of this repository instead of runs")
	runListJSON = addJSONFlag(runListCmd)

	runShowCmd.Flags().StringVar(&runShowStep, "step", "", "show one step's plan and events (e.g. S03)")
	runShowJSON = addJSONFlag(runShowCmd)

	runWatchCmd.Flags().DurationVar(&runWatchEvery, "every", time.Second, "redraw interval")

	runRetryCmd.Flags().StringVar(&runRetryStep, "step", "", "step to re-execute (required)")
	runRetryCmd.Flags().BoolVar(&runRetryNoRun, "no-run", false, "mark the step PENDING and stop, without executing")
	runRetryCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "skip the cost-preview confirmation prompt (for CI/scripts)")
	runRetryCmd.Flags().BoolVar(&runPlain, "plain", false, "disable TUI, use plain log output")
	runRetryCmd.Flags().BoolVar(&runSkipDoctor, "skip-doctor", false, "skip the pre-run config checks (corvex doctor)")
	runRetryCmd.Flags().BoolVar(&runApproveGates, "approve-gates", false, "auto-approve recipe human-gate stages (otherwise a gate stops the run)")

	runKillCmd.Flags().BoolVar(&runKillNow, "now", false, "signal without first proving the pid still belongs to this run")
	runKillCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "skip the confirmation prompt")

	// Every verb of the noun warns when it shadows a project of the same name.
	for _, verb := range []*cobra.Command{runListCmd, runShowCmd, runWatchCmd, runRetryCmd, runKillCmd} {
		verb.PreRun = noticeVerbShadow
	}

	runCmd.AddCommand(runStartCmd, runListCmd, runShowCmd, runWatchCmd, runRetryCmd, runKillCmd)
}
