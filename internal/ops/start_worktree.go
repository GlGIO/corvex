package ops

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	return SetupWorktreeOn(gitRoot, wtPath, "feat/"+feature, baseBranch, stdout, stderr)
}

// SetupWorktreeOn is SetupWorktree with the branch named by the caller.
//
// `corvex start` names branches `feat/<project>` because that is what it knows:
// a project name and a convention. A run dispatched from the board knows more —
// the work item's id and kind — and the branch a repository's own rules call for
// is `hotfix/73960-…` or `feature/59440-…`, not `feat/incident`. A worktree
// created with the wrong branch name is not a naming quibble: the branch is what
// `ship` reads to decide the PR's target, so the convention IS the routing.
func SetupWorktreeOn(gitRoot, wtPath, branch, baseBranch string, stdout, stderr io.Writer) (SetupWorktreeResult, error) {
	res := SetupWorktreeResult{Branch: branch}

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

// Worktree is one checkout of a repository: where it is and what branch it
// holds. It is what a screen coordinating "one worktree per feature" lists.
type Worktree struct {
	Path string `json:"path"`
	// Branch is the short name ("hotfix/73960-x"), empty for a detached HEAD.
	Branch string `json:"branch"`
	// Main marks the repository's own checkout — the one that is not a linked
	// worktree. It is the only one `git worktree remove` refuses, and the one a
	// dispatch lands in when no feature worktree is chosen.
	Main bool `json:"main"`
	gone bool
}

// ListWorktrees enumerates the checkouts git itself knows for a repository.
//
// git is asked rather than the directory convention being globbed, because the
// convention (`<repo>-<feature>` beside the repo) is what `corvex start`
// produces and NOT what a person produces. A worktree somebody created by hand
// somewhere else is as real as one corvex made, and a directory that merely
// looks like the convention — a leftover after `git worktree remove`, a copied
// tree — is not a worktree at all. `git worktree list` is the answer to both.
func ListWorktrees(workDir string) ([]Worktree, error) {
	gitRoot, err := FindGitRoot(workDir)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("git", "-C", gitRoot, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var list []Worktree
	var cur *Worktree
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
			cur = &list[len(list)-1]
			cur.Main = len(list) == 1 // git lists the main checkout first
		case cur == nil:
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case strings.HasPrefix(line, "prunable"):
			cur.gone = true
		}
	}
	// A worktree whose directory was deleted stays in git's list, marked
	// `prunable`, until somebody runs `git worktree prune`. Nothing can run in
	// it, and offering it put a checkout in the picker that fails on dispatch.
	live := list[:0]
	for _, w := range list {
		if !w.gone {
			live = append(live, w)
		}
	}
	return live, nil
}
