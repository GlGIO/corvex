package ops

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// NoCommitsError reports a repository whose HEAD is unborn. A fresh `git init`
// has no commits, so every base ref — even "main" — is invalid and
// `git worktree add` fails with a cryptic "fatal: invalid reference". Detecting
// it up front lets the caller spell out the recovery; ops only names the repo.
type NoCommitsError struct {
	// GitRoot is the repository that has no commits yet.
	GitRoot string
}

func (e *NoCommitsError) Error() string {
	return fmt.Sprintf("repository %s has no commits yet", e.GitRoot)
}

// SetupWorktreeResult describes what SetupWorktree did, for callers that want
// to report it.
type SetupWorktreeResult struct {
	// Branch is the branch the worktree was created on.
	Branch string
	// ReusedWorkspaceDir is true when the worktree already carried its own
	// .corvex so no symlink was created — see SetupWorktree.
	ReusedWorkspaceDir bool
}

// SetupWorktree creates a git worktree at wtPath on a new branch feat/<feature>
// based on baseBranch, then links the main repo's .corvex into it.
//
// git's own progress is forwarded to stdout and stderr as given. The two stay
// separate because `git worktree add` splits its output across them ("HEAD is
// now at ..." on one, "Preparing worktree ..." on the other); pass io.Discard
// to silence either.
func SetupWorktree(gitRoot, wtPath, feature, baseBranch string, stdout, stderr io.Writer) (SetupWorktreeResult, error) {
	res := SetupWorktreeResult{Branch: "feat/" + feature}

	if err := exec.Command("git", "-C", gitRoot, "rev-parse", "--verify", "HEAD").Run(); err != nil {
		return res, &NoCommitsError{GitRoot: gitRoot}
	}

	c := exec.Command("git", "-C", gitRoot, "worktree", "add", wtPath, "-b", res.Branch, baseBranch)
	c.Stdout = stdout
	c.Stderr = stderr
	if err := c.Run(); err != nil {
		return res, fmt.Errorf("git worktree add: %w", err)
	}

	corvexDst := filepath.Join(wtPath, ".corvex")

	// If `.corvex` already exists in the worktree, don't symlink over it. This
	// happens when `.corvex` is tracked in the repo (the checkout materialises
	// it) or when a previous run left a symlink. The worktree's own `.corvex`
	// is usable as-is; forcing a symlink would fail with "file exists".
	if _, err := os.Lstat(corvexDst); err == nil {
		res.ReusedWorkspaceDir = true
		return res, nil
	}

	if err := os.Symlink(filepath.Join(gitRoot, ".corvex"), corvexDst); err != nil {
		return res, fmt.Errorf("creating .corvex symlink: %w", err)
	}
	return res, nil
}
