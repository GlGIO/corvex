package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/provider"
	"github.com/spf13/cobra"
)

const maxBrainstormIterations = 20

var startCmd = &cobra.Command{
	Use:   "start <project>",
	Short: "Single entry point for a new feature (brainstorm → grill → plan)",
	Long: `Start is the onboarding gate for a new feature. It presents the appropriate
entry point based on how defined your idea is:

  brainstorm  vague idea; AI asks questions and writes spec.md
  grill       clear idea; describe it quickly, then AI interrogates the spec
  plan        spec.md already exists; skip straight to grill → plan`,
	Args: cobra.ExactArgs(1),
	RunE: runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	project := args[0]
	reader := bufio.NewReader(os.Stdin)

	gitRoot, err := findGitRoot(".")
	if err != nil {
		return fmt.Errorf("not in a git repository: %w", err)
	}

	wtPath := worktreePath(gitRoot, project)

	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		baseBranch := promptBaseBranch(reader)
		if err := setupWorktree(gitRoot, wtPath, project, baseBranch); err != nil {
			return fmt.Errorf("setting up worktree: %w", err)
		}
		fmt.Printf("\n✓ Worktree ready at %s\n\n", wtPath)
	} else {
		fmt.Printf("✓ Using existing worktree at %s\n\n", wtPath)
	}

	if err := os.Chdir(wtPath); err != nil {
		return fmt.Errorf("entering worktree: %w", err)
	}

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
	qaPath := filepath.Join(pDir, "brainstorm-qa.md")

	_, specErr := os.Stat(specPath)
	specExists := specErr == nil

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	mode := promptMode(reader, specExists)

	switch mode {
	case "brainstorm":
		return brainstormPath(cmd.Context(), p, cfg.Provider.Models.Planner, workDir, project, specPath, decisionsPath, qaPath, reader)
	case "grill":
		return grillPath(cmd.Context(), p, cfg.Provider.Models.Planner, workDir, project, specPath, decisionsPath, reader)
	case "plan":
		return planPath(cmd.Context(), p, cfg.Provider.Models.Planner, workDir, project, specPath, decisionsPath, reader)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

func promptMode(reader *bufio.Reader, specExists bool) string {
	fmt.Println()
	fmt.Println("corvex: How defined is this feature?")
	fmt.Println()
	fmt.Println("  1) brainstorm — vague idea; AI will ask questions and write spec.md")
	fmt.Println("  2) grill      — clear idea; describe it quickly, then AI interrogates spec")
	if specExists {
		fmt.Println("  3) plan       — spec.md already exists; skip straight to grill → plan")
	}
	fmt.Println()
	fmt.Print("Choice [1]: ")

	raw, err := reader.ReadString('\n')
	if err != nil {
		return "brainstorm"
	}
	switch strings.TrimSpace(raw) {
	case "2":
		return "grill"
	case "3":
		if specExists {
			return "plan"
		}
		return "brainstorm"
	default:
		return "brainstorm"
	}
}

// brainstormPath runs an AI-driven Q&A to explore requirements, writes spec.md, then grills it.
func brainstormPath(ctx context.Context, p provider.Provider, model, workDir, project, specPath, decisionsPath, qaPath string, reader *bufio.Reader) error {
	description := readMultilineInput(reader, "Briefly describe the feature (blank line to submit):\n> ")
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("feature description cannot be empty")
	}

	br := orchestrator.NewBrainstormer(p, model, workDir)
	griller := orchestrator.NewGriller(p, model, workDir)

	fmt.Println()
	fmt.Printf("Brainstorming %s — Ctrl+C to stop, /done to finish early\n", project)

	totalCost := 0.0
	answered := 0

	for i := 0; i < maxBrainstormIterations; i++ {
		log.Info("brainstorming", "iteration", i+1)
		step, err := br.Interview(ctx, description, qaPath)
		if err != nil {
			return fmt.Errorf("brainstorm step: %w", err)
		}
		totalCost += step.CostUSD

		if step.Done {
			fmt.Printf("\n✓ Design questions resolved (%d answers, $%.2f). Writing spec.md...\n", answered, totalCost)
			break
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
			fmt.Printf("\n✓ Stopped early (%d answers). Writing spec.md...\n", answered)
			goto writeSpec
		case "/skip":
			if err := appendDecision(qaPath, step.Question, "(skipped)"); err != nil {
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
			if err := appendDecision(qaPath, step.Question, answer); err != nil {
				return err
			}
		}
		answered++
	}

writeSpec:
	log.Info("generating spec.md from brainstorm Q&A")
	if err := br.GenerateSpec(ctx, description, qaPath, specPath); err != nil {
		return fmt.Errorf("generating spec.md: %w", err)
	}
	fmt.Printf("✓ spec.md written to %s\n\n", specPath)

	return runGrillLoop(ctx, griller, reader, project, specPath, decisionsPath)
}

// grillPath captures a quick feature description as spec.md then runs the grill loop.
func grillPath(ctx context.Context, p provider.Provider, model, workDir, project, specPath, decisionsPath string, reader *bufio.Reader) error {
	description := readMultilineInput(reader, "Describe the feature (blank line to submit):\n> ")
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("feature description cannot be empty")
	}

	if err := writeMinimalSpec(specPath, project, description); err != nil {
		return err
	}
	fmt.Printf("✓ spec.md written to %s\n\n", specPath)

	griller := orchestrator.NewGriller(p, model, workDir)
	return runGrillLoop(ctx, griller, reader, project, specPath, decisionsPath)
}

// planPath runs the grill loop on an existing spec.md.
func planPath(ctx context.Context, p provider.Provider, model, workDir, project, specPath, decisionsPath string, reader *bufio.Reader) error {
	griller := orchestrator.NewGriller(p, model, workDir)
	return runGrillLoop(ctx, griller, reader, project, specPath, decisionsPath)
}

// readMultilineInput prints prompt and reads lines until a blank line or EOF.
func readMultilineInput(reader *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	var lines []string
	for {
		line, err := reader.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" || err != nil {
			break
		}
		lines = append(lines, trimmed)
		fmt.Print("> ")
	}
	return strings.Join(lines, "\n")
}

// writeMinimalSpec creates a bare spec.md from a user-supplied description.
func writeMinimalSpec(specPath, project, description string) error {
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		return fmt.Errorf("creating spec directory: %w", err)
	}
	content := fmt.Sprintf("# %s\n\n## Objective\n\n%s\n", project, description)
	if err := os.WriteFile(specPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing spec.md: %w", err)
	}
	return nil
}

// findGitRoot walks up from start until it finds a directory containing .git.
func findGitRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no .git found above %s", start)
		}
		abs = parent
	}
}

// worktreePath returns the sibling path for a feature's worktree:
// <parent-of-repo>/<repo-name>-<feature>
func worktreePath(gitRoot, feature string) string {
	return filepath.Join(filepath.Dir(gitRoot), filepath.Base(gitRoot)+"-"+feature)
}

// promptBaseBranch asks the user which branch to base the worktree on.
func promptBaseBranch(reader *bufio.Reader) string {
	fmt.Print("Base branch [main]: ")
	raw, err := reader.ReadString('\n')
	if err != nil {
		return "main"
	}
	if b := strings.TrimSpace(raw); b != "" {
		return b
	}
	return "main"
}

// setupWorktree creates a git worktree at wtPath branching from baseBranch,
// then symlinks the main repo's .corvex directory into it.
func setupWorktree(gitRoot, wtPath, feature, baseBranch string) error {
	branch := "feat/" + feature
	c := exec.Command("git", "-C", gitRoot, "worktree", "add", wtPath, "-b", branch, baseBranch)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("git worktree add: %w", err)
	}

	corvexSrc := filepath.Join(gitRoot, ".corvex")
	corvexDst := filepath.Join(wtPath, ".corvex")
	if err := os.Symlink(corvexSrc, corvexDst); err != nil {
		return fmt.Errorf("creating .corvex symlink: %w", err)
	}
	return nil
}
