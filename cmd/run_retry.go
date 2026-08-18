package cmd

import (
	"fmt"
	"strings"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

// `run retry` is the roadmap's fourth differentiator — "re-execute one stage
// without redoing the rest" — finally having a command. It is `reset` plus
// `run --task` in one step, which is what the two were always used for
// together, and it is why `reset` could be deprecated instead of renamed: the
// old name described the file edit, not the intent.
func runRunRetry(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(runRetryStep) == "" {
		return fmt.Errorf("--step is required: name the step to re-execute (e.g. --step S03)")
	}
	step := strings.ToUpper(strings.TrimSpace(runRetryStep))

	workDir, project, err := resolveRunTarget(args[0])
	if err != nil {
		return err
	}
	if err := ops.ResetTask(workDir, project, step); err != nil {
		return err
	}
	fmt.Printf("%s reset to PENDING in %s\n", step, project)
	if runRetryNoRun {
		fmt.Printf("  → corvex run start %s --task %s\n", project, step)
		return nil
	}

	// Hand over to the run path with the target pinned. Setting the flag
	// variables rather than duplicating the flow is deliberate: a second copy of
	// the preflight, the cost preview and the TUI switch would drift.
	runTask, runSingle = step, false
	return runRun(cmd, []string{project})
}

// resolveRunTarget turns the positional argument into "which workspace, which
// project". A run id may name a run in another repository, and executing it from
// here would write generated code to the wrong tree — so that case is refused
// with the directory to cd into, not silently redirected.
func resolveRunTarget(arg string) (workDir, project string, err error) {
	_, local, err := ops.LoadConfig()
	if err != nil {
		return "", "", err
	}
	if err := requireCorvexDir(local); err != nil {
		return "", "", err
	}
	if !ops.IsRunID(arg) {
		return local, arg, nil
	}
	row, err := ops.RunLister{}.FindRun(arg)
	if err != nil {
		return "", "", err
	}
	name := row.Project
	if name == "" {
		name = row.Recipe
	}
	if !ops.SameRepo(local, row.Repo) {
		return "", "", fmt.Errorf("run %s belongs to %s, not to this repository — cd there and retry:\n  cd %s && corvex run retry %s --step %s",
			arg, row.Repo, row.Repo, arg, runRetryStep)
	}
	return local, name, nil
}
