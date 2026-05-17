package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/spf13/cobra"
)

var planCmd = &cobra.Command{
	Use:   "plan <project>",
	Short: "Generate or update tasks.md from a project spec",
	Long:  "Invoke the Planner agent (read-only) to analyze spec.md and generate a DAG of tasks.",
	Args:  cobra.ExactArgs(1),
	RunE:  runPlan,
}

func init() {
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

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	planner := orchestrator.NewPlanner(p, cfg.Provider.Models.Planner, workDir, cfg.AgentRouting)

	pDir := projectDir(workDir, project)
	specPath := filepath.Join(pDir, "spec.md")
	anchorPath := filepath.Join(pDir, "anchor.yaml")
	tasksPath := filepath.Join(pDir, "tasks.md")

	log.Info("planning", "project", project)
	if err := planner.Plan(cmd.Context(), specPath, anchorPath, tasksPath); err != nil {
		return fmt.Errorf("planning failed: %w", err)
	}

	log.Info("tasks.md generated", "path", tasksPath)
	return nil
}
