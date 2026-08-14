package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/tui"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

// Everything `corvex run` puts on a screen: which renderer to use, how the dry
// run is laid out, and how the operation facts turn into sentences. The
// decisions themselves live in internal/ops.

// isInteractive reports whether stdout is a terminal — the switch between the
// TUI and plain log output, and between prompting and auto-proceeding.
func isInteractive() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// newRunRenderer builds the plain-log renderer for a non-TUI run, honouring
// --no-color/NO_COLOR/non-TTY and --quiet.
func newRunRenderer(cmd *cobra.Command) *PlainRenderer {
	noColor, _ := cmd.Flags().GetBool("no-color")
	noColor = noColor || os.Getenv("NO_COLOR") != "" || !isInteractive()
	quiet, _ := cmd.Flags().GetBool("quiet")
	return NewPlainRenderer(os.Stdout, noColor, quiet)
}

// missingProjectError turns a missing project into the sentence a terminal user
// needs: what exists, and the closest name when it looks like a typo.
func missingProjectError(m *ops.MissingProject) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "project %q not found", m.Name)
	if len(m.Available) > 0 {
		fmt.Fprintf(&sb, "\n\navailable projects: %s", strings.Join(m.Available, ", "))
	}
	if m.Suggestion != "" {
		fmt.Fprintf(&sb, "\n\ndid you mean %q?", m.Suggestion)
	}
	return fmt.Errorf("%s", sb.String())
}

// formatRunPreview is the one-line summary printed before a run starts.
func formatRunPreview(p ops.RunPreview) string {
	ceil := "no cost ceiling set"
	if p.MaxCostUSD > 0 || p.MaxCostPerTaskUSD > 0 {
		ceil = fmt.Sprintf("ceilings: %s/run, %s/task",
			tui.FormatCost(p.MaxCostUSD), tui.FormatCost(p.MaxCostPerTaskUSD))
	}

	line := fmt.Sprintf("Run %q: %d pending task(s) · %s", p.Project, p.Pending, ceil)
	if p.SpentUSD > 0 {
		line += fmt.Sprintf(" · already spent %s", tui.FormatCost(p.SpentUSD))
	}
	return line
}

// printDryRun writes the execution plan a run would follow, marking with → the
// tasks that would actually execute.
func printDryRun(w io.Writer, tasksPath, project string) error {
	plan, err := ops.PlanRun(tasksPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "Dry run for project: %s\n\n", project)
	fmt.Fprintln(w, "Execution order:")
	for _, t := range plan.Order {
		marker := " "
		if t.Status == types.StatusPending {
			marker = "→"
		}
		fmt.Fprintf(w, "  %s %s %s — %s [%s]\n", marker, statusEmoji(t.Status), t.ID, t.Title, t.Status)
	}
	fmt.Fprintf(w, "\n%d task(s) would be executed\n", plan.Pending)
	return nil
}

// runWithTUI runs the orchestrator behind the full-screen TUI, seeding the DAG
// panel and the cost header from disk so the first frame is already populated
// (without this the panel says "no tasks loaded" until the first event).
func runWithTUI(ctx context.Context, orc *orchestrator.Orchestrator, events chan orchestrator.Event, commands chan orchestrator.Command, cancel context.CancelFunc, project, workDir string) error {
	m := tui.NewWithCommands(events, commands, cancel, project)

	if progress, err := ops.LoadProjectProgress(workDir, project); err == nil {
		entries := make([]tui.TaskEntry, 0, len(progress.Tasks))
		for _, t := range progress.Tasks {
			entries = append(entries, tui.TaskEntry{
				ID:       t.ID,
				Title:    t.Title,
				Status:   t.Status,
				Duration: t.Duration,
			})
		}
		m = m.AddDAGTasks(entries)
		m = m.SetDAGProgress(progress.Completed, progress.Total)
		// Cumulative cost/tokens from previous runs. New EventTaskComplete
		// events accumulate on top during this run.
		m = m.SeedStatusTotals(progress.TokensIn, progress.TokensOut, progress.CostUSD)
	}

	p := tea.NewProgram(m, tea.WithAltScreen())

	go func() {
		_ = orc.Run(ctx, project)
		close(events)
	}()

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}
