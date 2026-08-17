package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/ops"
)

var gateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every gate waiting for a decision, across repositories",
	Args:  cobra.NoArgs,
	RunE:  runGateList,
}

var gateShowCmd = &cobra.Command{
	Use:   "show <run-id>",
	Short: "Show one gate and the evidence behind it",
	Args:  cobra.ExactArgs(1),
	RunE:  runGateShow,
}

var gateApproveCmd = &cobra.Command{
	Use:   "approve <run-id>",
	Short: "Approve a gate and let its run continue",
	Long: "Approve a gate. Evidence marked required_reading must be acknowledged by label " +
		"with --ack; the labels come from `corvex gate show`.",
	Args: cobra.ExactArgs(1),
	RunE: runGateApprove,
}

var gateRejectCmd = &cobra.Command{
	Use:   "reject <run-id>",
	Short: "Reject a gate, failing its step",
	Args:  cobra.ExactArgs(1),
	RunE:  runGateReject,
}

func runGateList(_ *cobra.Command, _ []string) error {
	gates, err := ops.GateLister{}.ListGates()
	if err != nil {
		return err
	}
	if *gateAllJSON {
		return printJSON(os.Stdout, gates)
	}
	renderGateList(gates)
	return nil
}

func runGateShow(_ *cobra.Command, args []string) error {
	view, err := ops.GateLister{}.FindGate(args[0], gateStep)
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
	decided, err := ops.GateLister{}.DecideGate(args[0], gateStep, gate.Approved, gateAck, "")
	if err != nil {
		return err
	}
	fmt.Printf("Approved %s --step %s — %s\n", decided.RunID, decided.StepID, decided.Describe())
	return nil
}

func runGateReject(_ *cobra.Command, args []string) error {
	decided, err := ops.GateLister{}.DecideGate(args[0], gateStep, gate.Rejected, nil, gateReason)
	if err != nil {
		return err
	}
	fmt.Printf("Rejected %s --step %s — %s\n", decided.RunID, decided.StepID, decided.Describe())
	return nil
}
