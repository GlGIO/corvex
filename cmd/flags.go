package cmd

import (
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

// addJSONFlag registers a --json boolean flag on cmd and returns a pointer to
// the flag value. Commands check *jsonFlag before printing human output and
// take an early-return JSON branch instead.
func addJSONFlag(cmd *cobra.Command) *bool {
	var b bool
	cmd.Flags().BoolVar(&b, "json", false, "print machine-readable JSON instead of formatted output")
	return &b
}

// completeProjectArg is the ValidArgsFunction for commands whose first positional
// argument is <project>. It returns project names only when completing the first
// arg; subsequent args (e.g. <task> in reset/logs) receive no completion.
func completeProjectArg(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return ops.ProjectNames(workDir), cobra.ShellCompDirectiveNoFileComp
}
