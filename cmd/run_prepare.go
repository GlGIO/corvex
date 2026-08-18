package cmd

import (
	"fmt"
	"os"

	"github.com/giovannialves/corvex/internal/ops"
)

// resolveRecipeTarget compiles a recipe into its project when the run needs it,
// and reports what it did.
//
// The one thing it must never do is recompile on its own. tasks.md holds the
// status of every step, so an automatic recompilation is an automatic reset of
// finished work — the failure F2 hit inside its own fan-out. When the recipe is
// newer than the compiled DAG, ops.PrepareRunTarget returns a drift error whose
// message names both ways out; this function just lets it through.
func resolveRecipeTarget(workDir, project string) error {
	plan, err := ops.PrepareRunTarget(workDir, project, runRecompile, runNoRecompile)
	if err != nil {
		return err
	}
	if !plan.Compiled {
		return nil
	}
	fmt.Fprintf(os.Stderr, "compiled %s from its recipe — %d step(s)", project, plan.Tasks)
	if plan.Finished > 0 {
		fmt.Fprintf(os.Stderr, ", discarding %d finished step(s)", plan.Finished)
	}
	fmt.Fprintln(os.Stderr)
	return nil
}
