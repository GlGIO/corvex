package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/planning"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/spf13/cobra"
)

var grillCmd = &cobra.Command{
	Use:   "grill <project>",
	Short: "Interview to resolve ambiguities in a project spec before planning",
	Long: `Run the Griller agent (read-only) in an interactive loop. It surfaces the most
important unresolved design question, proposes a recommended answer grounded in
the codebase, and waits for your call. Resolved Q&A are appended to
.corvex/tasks/<project>/decisions.md and picked up automatically by 'corvex plan'.`,
	Args: cobra.ExactArgs(1),
	RunE: runGrill,
}

// grillBudget bounds how many questions one project may accumulate. It is a
// package var because `start` runs the same loop.
var grillBudget = DefaultGrillBudget

func init() {
	grillCmd.Flags().IntVar(&grillBudget, "max-questions", DefaultGrillBudget,
		"stop after this many recorded decisions for the project, counting previous sessions (0 = no limit)")
	rootCmd.AddCommand(grillCmd)
}

func runGrill(cmd *cobra.Command, args []string) error {
	project := args[0]

	cfg, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	pDir := ops.ProjectDir(workDir, project)
	specPath := filepath.Join(pDir, "spec.md")
	decisionsPath := filepath.Join(pDir, "decisions.md")

	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		return fmt.Errorf("spec.md not found at %s — create it first", specPath)
	}

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	griller := planning.NewGriller(p, cfg.Provider.Models.Planner, workDir)
	griller.SetProgressWriter(os.Stdout)
	reader := bufio.NewReader(os.Stdin)
	return runGrillLoop(cmd.Context(), griller, reader, project, specPath, decisionsPath)
}

// appendDecision is a call-through kept for cmd/start.go, which records
// brainstorm Q&A the same way. New call sites use ops.AppendDecision directly.
func appendDecision(path, question, answer string) error {
	return ops.AppendDecision(path, question, answer)
}
