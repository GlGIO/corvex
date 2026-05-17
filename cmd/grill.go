package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/spf13/cobra"
)

const maxGrillIterations = 50

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

func init() {
	rootCmd.AddCommand(grillCmd)
}

func runGrill(cmd *cobra.Command, args []string) error {
	project := args[0]

	cfg, workDir, err := loadConfig()
	if err != nil {
		return err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	pDir := projectDir(workDir, project)
	specPath := filepath.Join(pDir, "spec.md")
	decisionsPath := filepath.Join(pDir, "decisions.md")

	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		return fmt.Errorf("spec.md not found at %s — create it first", specPath)
	}

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	griller := orchestrator.NewGriller(p, cfg.Provider.Models.Planner, workDir)
	reader := bufio.NewReader(os.Stdin)
	return runGrillLoop(cmd.Context(), griller, reader, project, specPath, decisionsPath)
}

// runGrillLoop is the shared interactive Q&A loop used by both `grill` and `start`.
func runGrillLoop(ctx context.Context, griller *orchestrator.Griller, reader *bufio.Reader, project, specPath, decisionsPath string) error {
	totalCost := 0.0
	answered := 0

	fmt.Printf("Grilling %s — Ctrl+C to stop (decisions persist in decisions.md)\n", project)

	for i := 0; i < maxGrillIterations; i++ {
		log.Info("grilling", "iteration", i+1)
		step, err := griller.Grill(ctx, specPath, decisionsPath)
		if err != nil {
			return fmt.Errorf("grill step: %w", err)
		}
		totalCost += step.CostUSD

		if step.Done {
			fmt.Printf("\n✓ No further ambiguities. %d decision(s) recorded, $%.2f spent.\n", answered, totalCost)
			fmt.Printf("  Next: corvex plan %s\n", project)
			return nil
		}

		fmt.Printf("\n🔍 %s\n", step.Question)
		if step.Recommended != "" {
			fmt.Printf("💡 Recommended: %s\n", step.Recommended)
		}
		if step.Rationale != "" {
			fmt.Printf("   why: %s\n", step.Rationale)
		}
		fmt.Print("Your answer (Enter to accept recommendation, /skip to skip, /done to finish): ")

		raw, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading answer: %w", err)
		}
		answer := strings.TrimSpace(raw)

		switch answer {
		case "/done":
			fmt.Printf("\n✓ Stopped early. %d decision(s) recorded, $%.2f spent.\n", answered, totalCost)
			return nil
		case "/skip":
			if err := appendDecision(decisionsPath, step.Question, "(skipped — leave to planner)"); err != nil {
				return err
			}
		default:
			if answer == "" {
				if step.Recommended == "" {
					fmt.Println("(no recommendation — please type an answer or /skip)")
					i--
					continue
				}
				answer = step.Recommended
			}
			if err := appendDecision(decisionsPath, step.Question, answer); err != nil {
				return err
			}
		}
		answered++
	}

	fmt.Printf("\nReached iteration cap (%d). Run 'corvex plan %s' with what we have or continue with another 'corvex grill'.\n",
		maxGrillIterations, project)
	return nil
}

func appendDecision(path, question, answer string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating decisions dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening decisions file: %w", err)
	}
	defer f.Close()

	ts := time.Now().UTC().Format(time.RFC3339)
	entry := fmt.Sprintf("## %s\n_recorded: %s_\n\n**A:** %s\n\n", question, ts, answer)
	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("writing decision: %w", err)
	}
	return nil
}
