package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/run"
)

// Worktree represents a temporary git worktree created for parallel task
// execution (e.g. A/B model comparison).
type Worktree struct {
	// Path is the absolute filesystem path of the worktree.
	Path string
	// Branch is the local branch the worktree is checked out on.
	Branch string
}

// CreateWorktree creates a git worktree under <repoRoot>/.corvex/worktrees/<suffix>
// on a fresh branch named `corvex-ab/<suffix>` based on HEAD. The caller is
// responsible for invoking Remove when finished.
func CreateWorktree(ctx context.Context, repoRoot, suffix string) (*Worktree, error) {
	if suffix == "" {
		return nil, fmt.Errorf("worktree: suffix is required")
	}

	path := filepath.Join(repoRoot, ".corvex", "worktrees", suffix)
	branch := "corvex-ab/" + suffix

	// The worktrees directory is machine-local scratch and must be invisible to
	// the repository that contains it.
	//
	// MEASURED, not feared: with auto_commit on, the run's own checkpoint does
	// `git add -A` in the main checkout, sees `.corvex/worktrees/<suffix>` — a
	// directory with a `.git` file in it — and commits it as an EMBEDDED GIT
	// REPOSITORY. git says so in a warning nobody reads during a run:
	//     warning: adding embedded git repository: .corvex/worktrees/S02-000
	// and the commit then carries a gitlink to a checkout that is about to be
	// deleted. The A/B path has had this hole since it was written; it only
	// stayed invisible because an A/B run rarely checkpoints beside a live
	// worktree. A fan-out that isolates every item does it on every run.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("worktree: creating %s: %w", filepath.Dir(path), err)
	}
	if err := run.EnsureScratchIgnored(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("worktree: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "add", "-b", branch, path, "HEAD")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git worktree add: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	return &Worktree{Path: path, Branch: branch}, nil
}

// Remove force-removes the worktree directory and deletes its branch. Errors
// from `worktree remove` are returned; the branch delete is best-effort so
// callers can still clean up state even when the branch was already pruned.
func (w *Worktree) Remove(ctx context.Context, repoRoot string) error {
	if w == nil || w.Path == "" {
		return nil
	}

	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "remove", "--force", w.Path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree remove: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	if w.Branch != "" {
		// Best-effort branch cleanup; ignore failure (branch may already be
		// pruned, or may have been merged and the user wants it kept).
		_ = exec.CommandContext(ctx, "git", "-C", repoRoot, "branch", "-D", w.Branch).Run()
	}
	return nil
}
