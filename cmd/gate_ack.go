package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/ops"
)

// `gate ack` — marking evidence read without deciding anything.
//
// # Why this is not a flag
//
// F3's growth rule makes every new command answer that, and the answer here is
// not "it reads better": it is that there is no command to hang the flag on. A
// flag lives on a verb, and every existing gate verb ends the gate — approve,
// reject. `--ack` on `approve` can only ever mean "I read it *and* I decided
// now", which is precisely the single keystroke F2 set out to break. What is
// missing is a state transition with no decision in it, so it is a verb on the
// `gate` noun, which is one of the three the grammar allows. `gate ack` adds no
// fourth noun and no free-standing verb.
//
// # Why --ack rather than positional labels
//
// `gate ack <id> --step S --ack "Migration"` stutters, and `gate ack <id>
// "Migration"` would not. The stutter is the price of one property worth more:
// `gate show` prints a `--ack "…"` fragment, and that fragment is copyable into
// either verb unchanged. Two spellings for one argument would cost more than the
// repeated word does.
// # Two debts left on purpose
//
// gate.UnreadError still says "repeat --ack for each" and never mentions this
// verb, and `gate list` still prints required reading without printing progress
// through it. Both texts are pinned by F2 goldens this phase was told not to
// rewrite unless forced, and neither is forced: the verb is discoverable from
// `gate --help` and from `gate approve --help`. Naming it where a person
// actually meets the lock would be better, and is the first thing to change the
// next time those goldens are opened for another reason.
var gateAckCmd = &cobra.Command{
	Use:   "ack <run-id>",
	Short: "Mark required-reading evidence as read, without deciding the gate",
	Long: "Record that you read a piece of evidence, now, and leave the gate open.\n\n" +
		"Why a verb and not a flag on approve: `--ack` at approve time can only say \"I read it and " +
		"decided in the same breath\". This says only the first half, it persists, and a later " +
		"`corvex gate approve` accepts it — so reading on Monday and deciding on Tuesday is " +
		"expressible. The lock does not loosen: every required item still has to be acknowledged.\n\n" +
		"The labels come from `corvex gate show`. A label the gate does not carry is refused.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeGateArg,
	RunE:              runGateAck,
}

func runGateAck(_ *cobra.Command, args []string) error {
	if !ops.IsRunID(args[0]) {
		// An escalation is a failed step with a markdown file, not evidence
		// under a lock: there is nothing to acknowledge and no lock to release.
		return fmt.Errorf("%q is not a run id — reading state exists only on gates, "+
			"and an escalation is closed with `corvex gate approve %s` or `reject`", args[0], args[0])
	}
	if len(gateAck) == 0 {
		// No "--all": a switch that acknowledges everything unread in one word
		// is the rubber stamp this whole mechanism exists to make expensive.
		return fmt.Errorf("nothing to acknowledge: name each label with --ack " +
			"(the labels come from `corvex gate show`)")
	}
	view, err := (ops.GateLister{}).FindGate(args[0], gateStep)
	if err != nil {
		return err
	}
	g := view.Gate
	after, added, err := gate.MarkRead(g.Repo, g.RunID, g.StepID, gateAck, time.Now().UTC())
	if err != nil {
		return err
	}
	renderAck(after, added)
	return nil
}

// renderAck prints what was recorded and what the gate is still waiting for.
// Timestamps are printed, not hidden: seeing "read <now>" next to a decision a
// second later is the whole reason for storing them.
func renderAck(p gate.Pending, added []string) {
	newly := make(map[string]bool, len(added))
	for _, l := range added {
		newly[l] = true
	}
	for _, typed := range gateAck {
		mark, ok := p.ReadOf(typed)
		if !ok {
			continue
		}
		if newly[mark.Label] {
			fmt.Printf("Read %q at %s\n", mark.Label, mark.At.Format(time.RFC3339))
			continue
		}
		fmt.Printf("Already read %q at %s — the first reading is the one kept\n",
			mark.Label, mark.At.Format(time.RFC3339))
	}
	missing := p.MissingReading(nil)
	if len(missing) == 0 {
		fmt.Printf("\nAll required reading acknowledged. Approve with:\n  corvex gate approve %s --step %s\n",
			p.RunID, p.StepID)
		return
	}
	fmt.Printf("\nStill unread: %s\n  corvex gate show %s --step %s\n",
		strings.Join(missing, ", "), p.RunID, p.StepID)
}
