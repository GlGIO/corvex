package cmd

import (
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

// completeGateArg completes only what is actually waiting: parked runs by id,
// and projects that have an open escalation.
//
// The roadmap asked for exactly this ("gate approve <TAB> completes ONLY with
// pending gates"), and the reason is not convenience. A completion listing every
// run invites approving one that is not waiting — and being asked to approve
// something that already decided itself is how a gate becomes a rubber stamp.
func completeGateArg(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	inbox, err := (ops.GateLister{}).LoadInbox(optionalWorkspaceDir())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(inbox.Gates)+len(inbox.Escalations))
	for _, g := range inbox.Gates {
		out = append(out, g.Gate.RunID+"\t"+g.Gate.Describe())
	}
	for _, e := range inbox.Escalations {
		out = append(out, e.Project+"\tescalation, step "+e.Step)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeGateStep completes --step with the steps waiting on the run or
// project already typed, for the same reason.
func completeGateStep(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	inbox, err := (ops.GateLister{}).LoadInbox(optionalWorkspaceDir())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	target := ""
	if len(args) > 0 {
		target = args[0]
	}
	out := []string{}
	for _, g := range inbox.Gates {
		if target == "" || g.Gate.RunID == target {
			out = append(out, g.Gate.StepID)
		}
	}
	for _, e := range inbox.Escalations {
		if target == "" || e.Project == target {
			out = append(out, e.Step)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
