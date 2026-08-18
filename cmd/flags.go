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

// completeRunArg completes an argument that may be either a run id or a project
// name, which is exactly what `run show|watch|retry|kill` accept.
//
// It is deterministic and costs no token: run ids come from the global index and
// project names from disk. Ids come first because they are the answer the user
// cannot type from memory.
func completeRunArg(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := []string{}
	if rows, err := (ops.RunLister{}).ListRuns(ops.RunListOptions{}); err == nil {
		for _, r := range rows {
			out = append(out, r.RunID+"\t"+r.Label()+" ("+string(r.Status)+")")
		}
	}
	if _, workDir, err := ops.LoadConfig(); err == nil {
		out = append(out, ops.ProjectNames(workDir)...)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
