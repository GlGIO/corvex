package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
)

// FindGitRoot walks up from start until it finds a directory containing .git.
func FindGitRoot(start string) (string, error) {
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

// WorktreePath returns the sibling path convention used by `corvex start`:
// <parent-of-repo>/<repo-name>-<feature>.
func WorktreePath(gitRoot, feature string) string {
	return filepath.Join(filepath.Dir(gitRoot), filepath.Base(gitRoot)+"-"+feature)
}

// FindProjectWorktree returns the conventional worktree path for a project if
// the directory already exists, or empty string otherwise. workDir can be any
// path inside (or equal to) the git working tree; FindGitRoot walks up to the
// real root before computing the sibling.
func FindProjectWorktree(workDir, project string) string {
	gitRoot, err := FindGitRoot(workDir)
	if err != nil {
		return ""
	}
	wt := WorktreePath(gitRoot, project)
	info, err := os.Stat(wt)
	if err != nil || !info.IsDir() {
		return ""
	}
	return wt
}

// WorktreeMismatch reports a project whose worktree exists somewhere other than
// the directory the caller is operating from. Both paths are absolute.
type WorktreeMismatch struct {
	Project      string
	WorktreePath string
	WorkDir      string
}

// CheckWorktreeMismatch returns a mismatch when a worktree for the project
// exists outside workDir — operating from workDir would write to the wrong
// branch/state and bypass the worktree. nil means there is nothing to warn
// about: either no worktree exists, or workDir already is it.
func CheckWorktreeMismatch(workDir, project string) *WorktreeMismatch {
	wt := FindProjectWorktree(workDir, project)
	if wt == "" {
		return nil
	}
	absWork, _ := filepath.Abs(workDir)
	absWT, _ := filepath.Abs(wt)
	if absWork == absWT {
		return nil
	}
	return &WorktreeMismatch{Project: project, WorktreePath: absWT, WorkDir: absWork}
}

// LinkWorktreePaths symlinks the configured repo-relative paths from the main
// repo (gitRoot) into the worktree (wtPath). It is idempotent: missing sources
// are skipped, and an existing destination is never overwritten. Used by
// `corvex start` to bring gitignored state (deps, dotenv) the checkout omits.
func LinkWorktreePaths(gitRoot, wtPath string, paths []string) {
	for _, rel := range paths {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		src := filepath.Join(gitRoot, rel)
		dst := filepath.Join(wtPath, rel)
		if _, err := os.Lstat(dst); err == nil {
			continue // already present — don't clobber
		}
		if _, err := os.Stat(src); err != nil {
			continue // nothing to link from
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			log.Warn("worktree link: mkdir", "path", rel, "err", err)
			continue
		}
		if err := os.Symlink(src, dst); err != nil {
			log.Warn("worktree link", "path", rel, "err", err)
			continue
		}
		log.Info("linked into worktree", "path", rel)
	}
}
