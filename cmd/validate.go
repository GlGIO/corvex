package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/giovannialves/corvex/internal/stack"
	"github.com/giovannialves/corvex/internal/wizard"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate <project>",
	Short: "Run integration validation against the live stack",
	Long: `Validate spins up the configured stack (DB + app + optional Chrome),
then runs an AI agent to test endpoints and UI flows against the spec.`,
	Args: cobra.ExactArgs(1),
	RunE: runValidate,
}

func init() {
	rootCmd.AddCommand(validateCmd)
}

func runValidate(cmd *cobra.Command, args []string) error {
	project := args[0]

	cfg, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)
	if !wizard.Configured(cfg.Validate) {
		if err := wizard.New(reader, os.Stdout).Run(cmd.Context(), workDir, cfg); err != nil {
			return fmt.Errorf("validate wizard: %w", err)
		}
		cfg, workDir, err = ops.LoadConfig()
		if err != nil {
			return err
		}
	}

	return validateProject(cmd.Context(), cfg, workDir, project)
}

// validateProject is the shared entry point used by both validateCmd and --validate in run.
func validateProject(ctx context.Context, cfg *config.Config, workDir, project string) error {
	pDir := ops.ProjectDir(workDir, project)
	specPath := filepath.Join(pDir, "spec.md")
	tasksPath := filepath.Join(pDir, "tasks.md")

	log.Info("setting up validation stack", "project", project)
	cleanup, err := stack.Setup(ctx, workDir, project, cfg.Validate, stack.Streams{Out: os.Stdout, Err: os.Stderr})
	if err != nil {
		return fmt.Errorf("stack setup failed: %w", err)
	}
	defer cleanup()

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	validator := orchestrator.NewValidator(p, cfg.Provider.Models.Worker, workDir)
	validator.SetProgressWriter(os.Stdout)

	log.Info("running validator agent", "project", project)
	result, err := validator.Validate(ctx, specPath, tasksPath, cfg.Validate)
	if err != nil {
		return fmt.Errorf("validator: %w", err)
	}

	fmt.Printf("\n%s\n", result.Summary)

	if result.Verdict == orchestrator.VerdictPass {
		fmt.Printf("\n✓ PASS  ($%.2f, %ds)\n", result.CostUSD, result.DurationMs/1000)
		return nil
	}

	fmt.Printf("\n✗ FAIL  ($%.2f, %ds)\n", result.CostUSD, result.DurationMs/1000)
	return fmt.Errorf("validation failed")
}
