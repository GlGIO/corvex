package cmd

import (
	"context"

	"fmt"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/wizard"
	"github.com/spf13/cobra"
)

// executeRun is the half of `run` that actually runs: the TUI/plain switch, the
// identity-wrapped execution, and the optional validation afterwards. Split out
// of runRun to keep every production file in cmd/ under the 150-line ceiling the
// roadmap fixed in F0 — the ceiling is what keeps rules from creeping back into
// the cobra layer.
func executeRun(
	cmd *cobra.Command,
	runner *ops.Runner,
	workDir string,
	cfg *config.Config,
	project string,
	events chan orchestrator.Event,
	commands chan orchestrator.Command,
	ctx context.Context,
	cancel context.CancelFunc,
) error {
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
