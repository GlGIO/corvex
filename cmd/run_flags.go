package cmd

// The flag surface of `corvex run`: the variables cobra binds to, and the help
// text that explains each one. Kept apart from the run flow so reading the flow
// does not mean scrolling past two dozen flag declarations.

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
)

func init() {
	runCmd.Flags().StringVar(&runTask, "task", "", "run only a specific task (e.g. S03)")
	runCmd.Flags().BoolVar(&runSingle, "single", false, "run only the next pending task")
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "show execution plan without running")
	runCmd.Flags().BoolVar(&runPlain, "plain", false, "disable TUI, use plain log output")
	runCmd.Flags().BoolVar(&flagValidate, "validate", false, "run integration validation after all tasks complete")
	runCmd.Flags().StringVar(&runAB, "ab", "", "A/B run two models against one task (e.g. --ab sonnet,opus); requires --task")
	runCmd.Flags().BoolVar(&runNoReplan, "no-replan", false, "fail if spec.md drifted instead of auto-regenerating tasks.md (protects manual edits)")
	runCmd.Flags().BoolVar(&runHere, "here", false, "run from the current directory even when a worktree exists for this project (rarely correct — usually you want to cd into the worktree)")
	runCmd.Flags().BoolVar(&runForce, "force", false, "run even if the working tree is dirty, discarding uncommitted changes first (DESTRUCTIVE)")
	runCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "skip the cost-preview confirmation prompt (for CI/scripts)")
	runCmd.Flags().BoolVar(&runSkipDoctor, "skip-doctor", false, "skip the pre-run config checks (corvex doctor)")
	runCmd.Flags().BoolVar(&runApproveGates, "approve-gates", false, "auto-approve recipe human-gate stages (otherwise a gate stops the run)")
	rootCmd.AddCommand(runCmd)
}
