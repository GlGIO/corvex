package cmd

import "github.com/spf13/cobra"

// The flag surface of `corvex run`: the variables cobra binds to, and the help
// text that explains each one. Kept apart from the run flow so reading the flow
// does not mean scrolling past two dozen flag declarations.
//
// F4 registers the same set twice — on `run` (the permanent alias) and on
// `run start` (the verb) — binding both to these variables. Cobra only parses
// the flags of the command it dispatched to, so two registrations of one
// variable never fight; what it buys is that the alias and the verb cannot drift
// apart, because there is one list.

var (
	runTask         string
	runSingle       bool
	runDryRun       bool
	runPlain        bool
	flagValidate    bool
	runAB           string
	runNoReplan     bool
	runHere         bool
	runForce        bool
	runYes          bool
	runSkipDoctor   bool
	runApproveGates bool
	runRecompile    bool
	runNoRecompile  bool
	runEnvironment  string
)

func addRunFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&runTask, "task", "", "run only a specific task (e.g. S03)")
	cmd.Flags().BoolVar(&runSingle, "single", false, "run only the next pending task")
	cmd.Flags().BoolVar(&runDryRun, "dry-run", false, "show execution plan without running")
	cmd.Flags().BoolVar(&runPlain, "plain", false, "disable TUI, use plain log output")
	cmd.Flags().BoolVar(&flagValidate, "validate", false, "run integration validation after all tasks complete")
	cmd.Flags().StringVar(&runAB, "ab", "", "A/B run two models against one task (e.g. --ab sonnet,opus); requires --task")
	cmd.Flags().BoolVar(&runNoReplan, "no-replan", false, "fail if spec.md drifted instead of auto-regenerating tasks.md (protects manual edits)")
	cmd.Flags().BoolVar(&runHere, "here", false, "run from the current directory even when a worktree exists for this project (rarely correct — usually you want to cd into the worktree)")
	cmd.Flags().BoolVar(&runForce, "force", false, "run even if the working tree is dirty, discarding uncommitted changes first (DESTRUCTIVE)")
	cmd.Flags().BoolVarP(&runYes, "yes", "y", false, "skip the cost-preview confirmation prompt (for CI/scripts)")
	cmd.Flags().BoolVar(&runSkipDoctor, "skip-doctor", false, "skip the pre-run config checks (corvex doctor)")
	cmd.Flags().BoolVar(&runApproveGates, "approve-gates", false, "auto-approve recipe human-gate stages (otherwise a gate stops the run)")
	cmd.Flags().BoolVar(&runRecompile, "recompile", false, "recompile the recipe over the compiled DAG, DISCARDING the status of every step")
	cmd.Flags().BoolVar(&runNoRecompile, "no-recompile", false, "run the compiled DAG as it is, even when the recipe changed")
	cmd.Flags().StringVar(&runEnvironment, "env", "", "environment to stand up around the run: simple (default) or stack (the validate: stack, database included)")
}

func init() {
	addRunFlags(runCmd)
	rootCmd.AddCommand(runCmd)
}
