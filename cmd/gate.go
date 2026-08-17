package cmd

import (
	"github.com/spf13/cobra"
)

// The `gate` noun. Approving a gate from another process is what makes a human
// gate a gate rather than an abort, so this command is not cosmetic: it is half
// of the mechanism.
//
// The verbs follow the noun→verb scheme the roadmap fixed for F3
// (`gate list|approve|reject`). `gate show` is the one name F2 adds beyond that
// table: the required-reading lock needs somewhere to print evidence, and
// putting diffs inside `gate list` would make the listing unreadable. F3 owns
// the final naming and may rename it.
//
// `gate answer` — the run asking *the human* a question mid-flight — is a
// different axis and deliberately out of scope here.
var gateCmd = &cobra.Command{
	Use:   "gate",
	Short: "Inspect and decide the gates waiting on you",
	Long: "A human gate parks its run until somebody decides. These commands are that somebody: " +
		"list what is waiting across every repository, read the evidence, and approve or reject.",
}

var (
	gateStep   string
	gateAck    []string
	gateReason string
	gateAllJSON,
	gateShowJSON *bool
)

func init() {
	gateListCmd.Flags().SortFlags = false
	gateAllJSON = addJSONFlag(gateListCmd)

	gateShowCmd.Flags().StringVar(&gateStep, "step", "", "step id, when more than one gate is waiting on the run")
	gateShowJSON = addJSONFlag(gateShowCmd)

	gateApproveCmd.Flags().StringVar(&gateStep, "step", "", "step id, when more than one gate is waiting on the run")
	gateApproveCmd.Flags().StringArrayVar(&gateAck, "ack", nil, "acknowledge one piece of required-reading evidence, by label (repeatable)")

	gateRejectCmd.Flags().StringVar(&gateStep, "step", "", "step id, when more than one gate is waiting on the run")
	gateRejectCmd.Flags().StringVar(&gateReason, "reason", "", "why it was rejected")

	gateCmd.AddCommand(gateListCmd, gateShowCmd, gateApproveCmd, gateRejectCmd)
	rootCmd.AddCommand(gateCmd)
}
