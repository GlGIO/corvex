package cmd

import (
	"fmt"
	"os"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

// `run pause` is `run kill`'s non-destructive sibling and shares its shape: it
// addresses a run by bare id through the global index, so it works from any
// repository on the machine.
//
// What it does NOT share is the heartbeat proof. `kill` has to prove the pid
// still belongs to the run because SIGTERM on a recycled pid hits somebody
// else's process; pause writes a file addressed by run id, and a file addressed
// by an id nobody is using any more is inert — the worst case is an orphan, and
// both the run's teardown and the next claim of that id remove it.
func runRunPause(_ *cobra.Command, args []string) error {
	res, err := ops.RunLister{}.PauseRun(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Paused %s — it stops at the next wave, after the steps now running finish.\n", res.RunID)
	fmt.Fprintln(os.Stderr, "Work already in flight is not interrupted: `corvex run kill` is the verb that stops now.")
	fmt.Printf("  → corvex run resume %s\n", res.RunID)
	return nil
}

func runRunResume(_ *cobra.Command, args []string) error {
	res, err := ops.RunLister{}.ResumeRun(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Resumed %s — the next wave starts within a second.\n", res.RunID)
	fmt.Printf("  → corvex run show %s\n", res.RunID)
	return nil
}
