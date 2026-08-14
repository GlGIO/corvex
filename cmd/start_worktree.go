package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/giovannialves/corvex/internal/ops"
)

// enterStartWorktree makes wtPath this process's working directory, creating the
// worktree first when it does not exist yet, and announces the next step either
// way. git's own worktree-add chatter goes straight to the real streams so the
// user sees it as it happens.
func enterStartWorktree(reader *bufio.Reader, gitRoot, wtPath, project string) error {
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		baseBranch := promptBaseBranch(reader)
		res, setupErr := ops.SetupWorktree(gitRoot, wtPath, project, baseBranch, os.Stdout, os.Stderr)
		if setupErr != nil {
			return startWorktreeError(setupErr, project)
		}
		if res.ReusedWorkspaceDir {
			fmt.Printf("  Using the worktree's existing .corvex (it is tracked in this repo)\n")
		}
		fmt.Printf("\n✓ Worktree ready at %s\n", wtPath)
		fmt.Printf("  Next (after this command finishes): cd %s && corvex run %s\n\n", wtPath, project)
	} else {
		fmt.Printf("✓ Using existing worktree at %s\n", wtPath)
		fmt.Printf("  Next (after this command finishes): cd %s && corvex run %s\n\n", wtPath, project)
	}

	if err := os.Chdir(wtPath); err != nil {
		return fmt.Errorf("entering worktree: %w", err)
	}
	return nil
}

// startWorktreeError phrases a worktree-setup failure. An unborn HEAD is the one
// case worth a recovery command instead of a raw git error, so ops reports the
// fact and this spells out the two commands that unstick the user.
func startWorktreeError(err error, project string) error {
	var noCommits *ops.NoCommitsError
	if errors.As(err, &noCommits) {
		return fmt.Errorf("setting up worktree: this repository has no commits yet — run `git -C %s commit --allow-empty -m \"initial commit\"` and retry `corvex start %s`",
			noCommits.GitRoot, project)
	}
	return fmt.Errorf("setting up worktree: %w", err)
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
