package cmd

import (
	"time"

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
// F5 adds `gate ack`: reading state that survives the terminal being closed. It
// is a verb rather than a flag because every other gate verb ends the gate, so
// there is no command for "I read this and decided nothing" to be a flag on —
// the argument is made in full at the top of gate_ack.go.
//
// F5 also adds `gate audit`: the sensor over the sensors, measuring the gates
// themselves rather than what they guard. A verb on this noun for the same reason
// `ack` is one — the argument is at the top of gate_audit.go.
//
// `gate answer` is the last verb, and the only one on the other axis: the run
// asking *the human* a question mid-flight. It is a verb on this noun rather
// than a noun of its own because the roadmap's open decision #3 fixed the name
// and left the body — the argument is at the top of gate_answer.go.
var gateCmd = &cobra.Command{
	Use:   "gate",
	Short: "Inspect and decide the gates waiting on you",
	Long: "A human gate parks its run until somebody decides. These commands are that somebody: " +
		"list what is waiting across every repository, read the evidence, and approve or reject — " +
		"or, when the run is the one asking, answer it.",
}

var (
	gateStep         string
	gateAck          []string
	gateReason       string
	gateText         string
	gateAuditSince   string
	gateAuditRepo    string
	gateAuditAll     bool
	gateAuditSuspect time.Duration
	gateAllJSON,
	gateShowJSON,
	gateAuditJSON *bool
)

func init() {
	gateListCmd.Flags().SortFlags = false
	gateAllJSON = addJSONFlag(gateListCmd)

	gateShowCmd.Flags().StringVar(&gateStep, "step", "", "step id, when more than one gate is waiting on the run")
	gateShowJSON = addJSONFlag(gateShowCmd)

	gateAckCmd.Flags().StringVar(&gateStep, "step", "", "step id, when more than one gate is waiting on the run")
	gateAckCmd.Flags().StringArrayVar(&gateAck, "ack", nil, "mark one piece of evidence as read, by label (repeatable)")

	gateApproveCmd.Flags().StringVar(&gateStep, "step", "", "step id, when more than one gate is waiting on the run")
	gateApproveCmd.Flags().StringArrayVar(&gateAck, "ack", nil, "acknowledge one piece of required-reading evidence, by label (repeatable)")

	gateRejectCmd.Flags().StringVar(&gateStep, "step", "", "step id, when more than one gate is waiting on the run")
	gateRejectCmd.Flags().StringVar(&gateReason, "reason", "", "why it was rejected")

	gateAnswerCmd.Flags().StringVar(&gateStep, "step", "", "step id, when more than one gate is waiting on the run")
	gateAnswerCmd.Flags().StringVar(&gateText, "text", "", "the answer, in your own words")
	// The same lock as approve: a step that asks may also declare evidence you
	// have to read first, and without this flag that lock would have no verb
	// able to release it.
	gateAnswerCmd.Flags().StringArrayVar(&gateAck, "ack", nil, "acknowledge one piece of required-reading evidence, by label (repeatable)")

	gateAuditCmd.Flags().SortFlags = false
	gateAuditCmd.Flags().StringVar(&gateAuditSince, "since", "7d", "only gates opened within this window — 90m, 36h, 7d (0 for all)")
	gateAuditCmd.Flags().StringVar(&gateAuditRepo, "repo", "", "only gates of one repository (`--repo .` for the current one)")
	gateAuditCmd.Flags().BoolVar(&gateAuditAll, "all", false, "every gate ever recorded, ignoring --since (the window a small population needs)")
	// A duration flag, not a hardcoded 4s: the threshold is the roadmap's policy,
	// and it never enters --json — it only changes what the printed hint accuses.
	gateAuditCmd.Flags().DurationVar(&gateAuditSuspect, "suspect-under", 10*time.Second, "call out decisions faster than this in the printed hint (policy, never in --json)")
	gateAuditJSON = addJSONFlag(gateAuditCmd)

	for _, c := range []*cobra.Command{gateShowCmd, gateAckCmd, gateApproveCmd, gateRejectCmd, gateAnswerCmd} {
		_ = c.RegisterFlagCompletionFunc("step", completeGateStep)
	}

	gateCmd.AddCommand(gateListCmd, gateShowCmd, gateAckCmd, gateApproveCmd, gateRejectCmd, gateAnswerCmd, gateAuditCmd)
	rootCmd.AddCommand(gateCmd)
}
