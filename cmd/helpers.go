package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

func loadConfig() (*config.Config, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("getting working directory: %w", err)
	}

	configPath := filepath.Join(wd, ".corvex", "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return config.Default(), wd, nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("loading config: %w", err)
	}

	return cfg, wd, nil
}

func projectDir(workDir, project string) string {
	return filepath.Join(workDir, ".corvex", "tasks", project)
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

// worktreePath returns the sibling path convention used by `corvex start`:
// <parent-of-repo>/<repo-name>-<feature>.
func worktreePath(gitRoot, feature string) string {
	return filepath.Join(filepath.Dir(gitRoot), filepath.Base(gitRoot)+"-"+feature)
}

// findProjectWorktree returns the conventional worktree path for a project if
// the directory already exists, or empty string otherwise. workDir can be any
// path inside (or equal to) the git working tree; findGitRoot walks up to the
// real root before computing the sibling.
func findProjectWorktree(workDir, project string) string {
	gitRoot, err := findGitRoot(workDir)
	if err != nil {
		return ""
	}
	wt := worktreePath(gitRoot, project)
	info, err := os.Stat(wt)
	if err != nil || !info.IsDir() {
		return ""
	}
	return wt
}

func requireCorvexDir(workDir string) error {
	corvexDir := filepath.Join(workDir, ".corvex")
	if _, err := os.Stat(corvexDir); os.IsNotExist(err) {
		return fmt.Errorf(".corvex directory not found — run 'corvex init' first")
	}
	return nil
}

func statusEmoji(s types.TaskStatus) string {
	switch s {
	case types.StatusPending:
		return "⬜"
	case types.StatusRunning:
		return "🔄"
	case types.StatusPassed:
		return "✅"
	case types.StatusFailed:
		return "❌"
	case types.StatusSkipped:
		return "⏭️"
	default:
		return "?"
	}
}
