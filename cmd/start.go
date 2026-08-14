package cmd

import (
	"bufio"
	"fmt"
	"os"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

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

	gitRoot, err := ops.FindGitRoot(".")
	if err != nil {
		return fmt.Errorf("not in a git repository: %w", err)
	}

	wtPath := ops.WorktreePath(gitRoot, project)
	if err := enterStartWorktree(reader, gitRoot, wtPath, project); err != nil {
		return err
	}

	cfg, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	// Bring gitignored state the checkout omits (deps, dotenv) into the worktree
	// per worktree.link, so a fresh worktree can build/run without a manual copy.
	ops.LinkWorktreePaths(gitRoot, wtPath, cfg.Worktree.Link)

	if err := runStartMode(cmd.Context(), cfg, workDir, project, reader); err != nil {
		return err
	}

	// The chdir in enterStartWorktree only affected this process. The user's
	// shell is still wherever they invoked `corvex start` from — typically the
	// main repo, not the worktree. Spell out the next step so they don't
	// accidentally run `corvex run` from the wrong directory and write
	// generated code to main.
	fmt.Printf("\nReady to execute. From a new prompt, run:\n")
	fmt.Printf("  cd %s\n", wtPath)
	fmt.Printf("  corvex run %s\n\n", project)
	return nil
}
