package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	charmbraceletlog "github.com/charmbracelet/log"
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

	runner, err := ops.NewRunner(ops.RunRequest{
		Config:       cfg,
		Project:      project,
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
	// A run whose identity could not be registered still runs — see
	// ops.Runner.IdentityErr — but it must say so, or "the run is missing from
	// the list" becomes an unexplained mystery later.
	if runner.IdentityErr != nil {
		charmbraceletlog.Warn("run identity unavailable", "err", runner.IdentityErr)
	}
	defer func() {
		if runner.StatusErr != nil {
			charmbraceletlog.Warn("recording run status", "run", runner.RunID, "err", runner.StatusErr)
		}
	}()

	if !runPlain && isInteractive() {
		// The TUI path has always returned nil for a failed run (the failure is
		// on screen, not in the exit code) — pre-existing, and not F1's to
		// change. The run record must still say `failed`, so the orchestrator's
		// error goes to Execute while runRun keeps returning only the TUI's.
		var tuiErr error
		_ = runner.Execute(ctx, func(ctx context.Context) error {
			var orcErr error
			orcErr, tuiErr = runWithTUI(ctx, runner.Orchestrator, events, commands, cancel, runner.Project, workDir)
			return orcErr
		})
		return tuiErr
	}

	renderer := newRunRenderer(cmd)
	go renderer.Drain(events)

	if err := runner.Execute(ctx, func(ctx context.Context) error {
		return runner.Orchestrator.Run(ctx, runner.Project)
	}); err != nil {
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
