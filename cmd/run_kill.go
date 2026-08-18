package cmd

import (
	"fmt"
	"os"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

// `run kill` is the first command in this codebase that acts destructively on a
// liveness read, which is why it asks twice: it proves the pid still belongs to
// the run (ops.KillRun) and, on a terminal, it asks the human. `--now` drops the
// first, `-y` the second.
func runRunKill(_ *cobra.Command, args []string) error {
	lister := ops.RunLister{}
	row, err := lister.FindRun(args[0])
	if err != nil {
		return err
	}
	if !runYes && isInteractive() {
		fmt.Fprintf(os.Stderr, "Stop %s (%s) started %s ago in %s? [y/N] ",
			row.RunID, row.Label(), humanWait(row.Age), row.Repo)
		if !confirmYes() {
			return fmt.Errorf("aborted by user")
		}
	}
	if !runKillNow {
		fmt.Fprintf(os.Stderr, "waiting for %s to beat, to prove the pid is still its own...\n", row.RunID)
	}
	res, err := lister.KillRun(args[0], ops.KillOptions{Prove: !runKillNow})
	if err != nil {
		return err
	}
	fmt.Printf("Signalled %s (pid %d) — it writes `canceling` and unwinds.\n", res.RunID, res.PID)
	if !res.Proved {
		fmt.Println("Not proven: --now skipped the heartbeat check, so a recycled pid would have been signalled too.")
	}
	fmt.Printf("  → corvex run show %s\n", res.RunID)
	return nil
}
