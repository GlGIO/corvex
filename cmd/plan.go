package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var planHere bool

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
	rootCmd.AddCommand(planCmd)
}

func runPlan(cmd *cobra.Command, args []string) error {
	project := args[0]

	cfg, workDir, err := loadConfig()
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

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	planner := orchestrator.NewPlanner(p, cfg.Provider.Models.Planner, workDir, cfg.AgentRouting, cfg.Plan.ContextCommand)
	planner.SetProgressWriter(os.Stdout)

	pDir := projectDir(workDir, project)
	specPath := filepath.Join(pDir, "spec.md")
	anchorPath := filepath.Join(pDir, "anchor.yaml")
	tasksPath := filepath.Join(pDir, "tasks.md")

	log.Info("planning", "project", project)
	if err := planner.Plan(cmd.Context(), specPath, anchorPath, tasksPath); err != nil {
		return fmt.Errorf("planning failed: %w", err)
	}

	log.Info("tasks.md generated", "path", tasksPath)

	// Persist the spec hash in anchor.yaml so subsequent `corvex run`
	// invocations can detect whether the spec has drifted. Without this,
	// `needsPlanning` finds no anchor state, treats the spec as "changed",
	// and triggers an automatic replan every time — wiping manual edits to
	// tasks.md and resetting completed task statuses to PENDING.
	existing, _ := anchor.Load(anchorPath)
	hash, err := anchor.SpecHash(specPath)
	if err != nil {
		return fmt.Errorf("hashing spec: %w", err)
	}
	existing.Project = project
	existing.SpecHash = hash
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if existing.Completed == nil {
		existing.Completed = []types.CompletedTask{}
	}
	if err := anchor.Save(anchorPath, existing); err != nil {
		return fmt.Errorf("saving anchor: %w", err)
	}

	fmt.Printf("Next: corvex run %s\n", project)
	return nil
}
