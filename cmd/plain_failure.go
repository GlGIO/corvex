package cmd

import (
	"fmt"
	"strings"

	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/tui"
)

// writeFailure prints a failed step: the status line, and then what the step
// actually said.
//
// This is the half of the fix that saves a second command. A `kind: tool` /
// `kind: test` stage that failed used to print the count and nothing else
// (`! S03 failed · command exit did not pass after 1 iteration(s)`), so the
// operator's next move was `run show <id> --step S03` — which said the same
// thing — and then re-running the command by hand to find out that a CLI wanted
// a flag it had not been given. The sentence naming that flag was captured by
// internal/step all along; it just had nowhere to land.
//
// No header above the output. The line directly above it names the step and says
// it failed, so an "output:" line would only repeat what the indentation already
// says. Four spaces rather than a rule or a box because --no-color output has to
// stay ASCII and greppable, and each event is rendered by one goroutine draining
// one channel, so a parallel wave's two failures never interleave their lines.
//
// Only a FAILED event carries Output (see event.Event), so a run where
// everything passes prints exactly the bytes it printed before. Truncation
// happened at the producer, once, so this screen and `run show --step` cannot
// disagree about what the command said.
func (r *PlainRenderer) writeFailure(ev orchestrator.Event) {
	g := r.coloured("✗", "!", tui.StatusFailed)
	fmt.Fprintf(r.w, "%s %s  failed %s %s\n", g, ev.TaskID, r.dot(), ev.Message)
	if strings.TrimSpace(ev.Output) == "" {
		return
	}
	for _, line := range strings.Split(strings.TrimRight(ev.Output, "\n"), "\n") {
		fmt.Fprintf(r.w, "    %s\n", line)
	}
}
