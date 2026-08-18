package cmd

import (
	"fmt"
	"time"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

// `run watch` is a reader, not the TUI (F3, D12). The TUI owns the run it draws
// and lives in the same process; watch exists precisely for the runs it does not
// own — one started in another terminal, or by the UI in F7. The only stream
// that crosses a process boundary here is what is on disk, so watch polls it.
func runRunWatch(_ *cobra.Command, args []string) error {
	workDir, err := showWorkDir(args[0])
	if err != nil {
		return err
	}
	every := runWatchEvery
	if every <= 0 {
		every = time.Second
	}
	for {
		report, err := ops.RunLister{}.LoadRunReport(workDir, args[0], "")
		if err != nil {
			return err
		}
		if isInteractive() {
			// Clear and home. Under a pipe this would just litter the output with
			// escape codes, so a non-terminal reader gets plain successive frames.
			fmt.Print("\033[2J\033[H")
		}
		renderRunReport(report)

		// A run this process cannot see (`dead`, `unknown`, finished) ends the
		// watch: redrawing a frozen screen forever would say less than stopping,
		// and the reason is already on the screen.
		if report.Settled() {
			return nil
		}
		time.Sleep(every)
	}
}
