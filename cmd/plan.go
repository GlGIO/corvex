package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/planning"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/spf13/cobra"
)

var (
	planHere     bool
	planReanchor bool
)

var planCmd = &cobra.Command{
	Use:               "plan <project>",
	Short:             "Generate or update tasks.md from a project spec",
	Long:              "Invoke the Planner agent (read-only) to analyze spec.md and generate a DAG of tasks.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeProjectArg,
	RunE:              runPlan,
}

func init() {
	planCmd.Flags().BoolVar(&planHere, "here", false, "plan from the current directory even when a worktree exists for this project")
	planCmd.Flags().BoolVar(&planReanchor, "reanchor", false, "accept the current spec.md as-is: update the stored spec hash without invoking the Planner or touching tasks.md (use when a spec edit doesn't change the task breakdown)")
	rootCmd.AddCommand(planCmd)
}

func runPlan(cmd *cobra.Command, args []string) error {
	project := args[0]

	cfg, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}

	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	// Refuse to plan from the main repo when a worktree exists — otherwise the
	// generated tasks.md lands in the wrong .corvex (the bug we hit running
	// `corvex plan` from the main checkout instead of the worktree).
	if err := checkWorktreeMismatch(workDir, project, "plan", planHere); err != nil {
		return err
	}

	pDir := ops.ProjectDir(workDir, project)
	specPath := filepath.Join(pDir, "spec.md")
	tasksPath := filepath.Join(pDir, "tasks.md")
	anchorPath := filepath.Join(pDir, "anchor.yaml")

	// --reanchor: the spec changed in a way that doesn't affect the task
	// breakdown (e.g. an edited validation note). Re-record the spec hash so the
	// drift guard in `corvex run` stops firing, without invoking the Planner —
	// which would regenerate (and can corrupt) the existing tasks.md.
	if planReanchor {
		return reanchorSpec(project, specPath, tasksPath, anchorPath)
	}

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	planner := planning.NewPlanner(p, cfg.Provider.Models.Planner, workDir, cfg.AgentRouting, cfg.Plan.ContextCommand)
	planner.SetProgressWriter(os.Stdout)

	log.Info("planning", "project", project)
	if err := planner.Plan(cmd.Context(), specPath, anchorPath, tasksPath); err != nil {
		return fmt.Errorf("planning failed: %w", err)
	}

	log.Info("tasks.md generated", "path", tasksPath)

	if err := ops.RecordSpecHash(project, specPath, anchorPath); err != nil {
		return err
	}

	fmt.Printf("Next: corvex run %s\n", project)
	return nil
}

// reanchorSpec re-records the spec hash in anchor.yaml without running the
// Planner. It refuses to run unless a spec.md and a parseable tasks.md already
// exist — re-anchoring against a missing or broken plan would silently mask the
// problem. Used for benign spec edits that don't change the task breakdown.
func reanchorSpec(project, specPath, tasksPath, anchorPath string) error {
	switch st := ops.InspectReanchor(specPath, tasksPath); st.Problem {
	case ops.ReanchorNoSpec:
		return fmt.Errorf("--reanchor: spec.md not found at %s", st.SpecPath)
	case ops.ReanchorNoTasks:
		return fmt.Errorf("--reanchor: no tasks.md at %s — nothing to anchor; run `corvex plan %s` first", st.TasksPath, project)
	case ops.ReanchorBrokenTasks:
		return fmt.Errorf("--reanchor: tasks.md does not parse, refusing to anchor a broken plan: %w", st.Err)
	}

	if err := ops.RecordSpecHash(project, specPath, anchorPath); err != nil {
		return err
	}

	log.Info("re-anchored spec hash (tasks.md left untouched)", "project", project)
	fmt.Printf("Next: corvex run %s\n", project)
	return nil
}
