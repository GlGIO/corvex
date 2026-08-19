package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/giovannialves/corvex/internal/ops"
)

// `gate answer` — the axis inverted: the run asks, and a person replies in
// words.
//
// # Why a verb on `gate` and not a noun of its own
//
// The roadmap's open decision #3 says it in one line — "Mantenha `gate answer`;
// se a F3 concluir o contrário, registre e pare" — and the F3 surface table
// already carries the name with the body left for later. So this file writes the
// body under the name that was fixed, and the choice between `gate answer` and a
// free-standing `answer` at the top stays the owner's; it is recorded as pending
// in .corvex/tasks/rebrand/f7-registro.md rather than settled here. No alias is
// added, for the same reason: the roadmap allows exactly two, both named.
//
// # Why --text and not --choice
//
// The roadmap's table sketches `gate answer <id> --choice C`, and a choice is
// the narrower thing: it presupposes that the asking step declared a set of
// options, which is a field on the gate that does not exist and a schema
// decision that is not this task's to make. Free text is what the disk format
// can carry today, and `--choice` remains expressible on top of it later without
// a migration. Recorded as pending too — the flag is not the name, and the name
// is what the roadmap froze.
var gateAnswerCmd = &cobra.Command{
	Use:   "answer <run-id>",
	Short: "Answer a gate that asked you a question, in words",
	Long: "Reply to a `question` gate: the step parked because it needs something only you know, " +
		"and the words you write here are what unparks it.\n\n" +
		"This is not `approve` under another name, and neither verb accepts the other's gate. " +
		"Approving a question would resume the run with an empty answer — consent where a value " +
		"was asked for — so it is refused. To decline, use `corvex gate reject`, which already " +
		"means the step does not proceed.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeGateArg,
	RunE:              runGateAnswer,
}

func runGateAnswer(_ *cobra.Command, args []string) error {
	if !ops.IsRunID(args[0]) {
		// An escalation is a failed step with a markdown file. Nothing there
		// asked a question, and nothing is waiting to read a reply.
		return fmt.Errorf("%q is not a run id — only a run that is parked on a question can be answered, "+
			"and an escalation is closed with `corvex gate approve %s` or `reject`", args[0], args[0])
	}
	// Fast-fail on argv, the same shape `gate ack` uses for an empty --ack. The
	// rule itself lives in gate.Decide, which every writer goes through; this
	// only saves a person the round trip through a run that may be dead anyway.
	if strings.TrimSpace(gateText) == "" {
		return fmt.Errorf("nothing to answer with: write the reply in --text " +
			"(the question is printed by `corvex gate show`)")
	}
	answered, err := (ops.GateLister{}).AnswerGate(args[0], gateStep, gateAck, gateText)
	if err != nil {
		return err
	}
	// The answer is echoed back, not just acknowledged: it is about to be the
	// input of a step, and the moment to notice a typo is now rather than in
	// whatever the step does with it.
	fmt.Printf("Answered %s --step %s — %s\n  %s\n",
		answered.RunID, answered.StepID, answered.Describe(), answered.Decision.Answer)
	return nil
}
