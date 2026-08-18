package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/ops"
)

// The gate inbox after F3's D6: one screen for everything that stopped and
// wants a person. A human gate has a live process waiting on the answer; an
// escalation is a file a failed run left behind. The first argument says which
// one is being addressed — a run id or a project — the same discrimination
// `run show` makes, so there is one rule to learn.

var gateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every gate and escalation waiting for a decision, across repositories",
	Args:  cobra.NoArgs,
	RunE:  runGateList,
}

var gateShowCmd = &cobra.Command{
	Use:               "show <run-id|project>",
	Short:             "Show one gate or escalation and the evidence behind it",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeGateArg,
	RunE:              runGateShow,
}

var gateApproveCmd = &cobra.Command{
	Use:   "approve <run-id|project>",
	Short: "Approve a gate, or close an escalation and let its step run again",
	Long: "Approve a gate. Evidence marked required_reading must be acknowledged by label " +
		"with --ack; the labels come from `corvex gate show`. Anything you already marked with " +
		"`corvex gate ack` counts and needs no repeating — the lock asks that every required item " +
		"was acknowledged, not that it was acknowledged in this command.\n\n" +
		"With a project instead of a run id, it closes an escalation: the file is removed and " +
		"the step goes back to PENDING, which is the \"I fixed it, try again\" answer.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeGateArg,
	RunE:              runGateApprove,
}

var gateRejectCmd = &cobra.Command{
	Use:               "reject <run-id|project>",
	Short:             "Reject a gate, or close an escalation leaving its step failed",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeGateArg,
	RunE:              runGateReject,
}

func runGateList(_ *cobra.Command, _ []string) error {
	inbox, err := (ops.GateLister{}).LoadInbox(optionalWorkspaceDir())
	if err != nil {
		return err
	}
	if *gateAllJSON {
		return printJSON(os.Stdout, inbox)
	}
	renderInbox(inbox)
	return nil
}

func runGateShow(_ *cobra.Command, args []string) error {
	if !ops.IsRunID(args[0]) {
		return showEscalation(args[0])
	}
	view, err := (ops.GateLister{}).FindGate(args[0], gateStep)
	if err != nil {
		return err
	}
	if *gateShowJSON {
		return printJSON(os.Stdout, view)
	}
	renderGateDetail(view)
	return nil
}

func runGateApprove(_ *cobra.Command, args []string) error {
	if !ops.IsRunID(args[0]) {
		return closeEscalation(args[0], true)
	}
	decided, err := (ops.GateLister{}).DecideGate(args[0], gateStep, gate.Approved, gateAck, "")
	if err != nil {
		return err
	}
	fmt.Printf("Approved %s --step %s — %s\n", decided.RunID, decided.StepID, decided.Describe())
	return nil
}

func runGateReject(_ *cobra.Command, args []string) error {
	if !ops.IsRunID(args[0]) {
		return closeEscalation(args[0], false)
	}
	decided, err := (ops.GateLister{}).DecideGate(args[0], gateStep, gate.Rejected, nil, gateReason)
	if err != nil {
		return err
	}
	fmt.Printf("Rejected %s --step %s — %s\n", decided.RunID, decided.StepID, decided.Describe())
	return nil
}
