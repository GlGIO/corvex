package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/wizard"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:               "run <project>",
	Short:             "Execute pending tasks for a project",
	Long:              "Run the orchestration loop: DAG resolve → Worker → Reviewer → checkpoint → next.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeProjectArg,
	RunE:              runRun,
}

func runRun(cmd *cobra.Command, args []string) error {
	project := args[0]

	cfg, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}

	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	// Fail early with a helpful message when the project does not exist.
	if missing := ops.CheckProject(workDir, project); missing != nil {
		return missingProjectError(missing)
	}

	// `corvex start <proj>` creates a sibling worktree at <repo>-<proj>.
	// If that worktree exists but the user is invoking `run` from somewhere
	// else (typically the main repo where they ran `start`), the run would
	// write all generated code to the wrong branch and orphan the worktree.
	// Refuse with an actionable message; `--here` is the escape hatch when
	// the user really means to run from the current directory (e.g. the
	// worktree is a leftover from an abandoned experiment).
	if err := checkWorktreeMismatch(workDir, project, "run", runHere); err != nil {
		return err
	}

	if runDryRun {
		tasksPath := filepath.Join(ops.ProjectDir(workDir, project), "tasks.md")
		return printDryRun(os.Stdout, tasksPath, project)
	}

	// Pre-run config gate: fail fast on misconfiguration the way `corvex doctor`
	// would, before spending any tokens. Warnings don't block; --skip-doctor
	// bypasses entirely.
	if !runSkipDoctor {
		if gateErr := doctorGate(cfg, workDir); gateErr != nil {
			return gateErr
		}
	}

	// Cost preview + confirmation: corvex run spends real money. Show what will
	// run and the ceilings, then confirm on an interactive TTY (auto-proceed for
	// --yes or non-TTY/CI so pipes don't hang).
	fmt.Fprintln(os.Stderr, formatRunPreview(ops.LoadRunPreview(workDir, project, cfg)))
	if !confirmRun() {
		return fmt.Errorf("aborted by user")
	}

	events := make(chan orchestrator.Event, 256)
	commands := make(chan orchestrator.Command, 16)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	orc, err := ops.NewRunner(ops.RunRequest{
		Config:       cfg,
		WorkDir:      workDir,
		TargetTask:   runTask,
		SingleTask:   runSingle,
		NoReplan:     runNoReplan,
		Force:        runForce,
		ApproveGates: runApproveGates,
		ABSpec:       runAB,
		Events:       events,
		Commands:     commands,
	})
	if err != nil {
		return err
	}

	if !runPlain && isInteractive() {
		return runWithTUI(ctx, orc, events, commands, cancel, project, workDir)
	}

	renderer := newRunRenderer(cmd)
	go renderer.Drain(events)

	if err := orc.Run(ctx, project); err != nil {
		return fmt.Errorf("run failed: %w", err)
	}

	if flagValidate {
		if !wizard.Configured(cfg.Validate) {
			return fmt.Errorf("--validate set but validate: not configured — run 'corvex validate %s' first to set it up", project)
		}
		return validateProject(cmd.Context(), cfg, workDir, project)
	}
	return nil
}
