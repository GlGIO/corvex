package cmd

import (
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/ops"
)

// The operations below live in internal/ops so the CLI and the HTTP surface run
// the same code. What is left here is a package-local alias for each one: cmd/
// keeps calling the short name while the rule has a single home. Call sites are
// free to use ops.X directly and drop the alias.

func loadConfig() (*config.Config, string, error) { return ops.LoadConfig() }

func projectDir(workDir, project string) string { return ops.ProjectDir(workDir, project) }

func projectNames(workDir string) []string { return ops.ProjectNames(workDir) }

func suggestProject(workDir, name string) string { return ops.SuggestProject(workDir, name) }

func findGitRoot(start string) (string, error) { return ops.FindGitRoot(start) }

func worktreePath(gitRoot, feature string) string { return ops.WorktreePath(gitRoot, feature) }

func linkWorktreePaths(gitRoot, wtPath string, paths []string) {
	ops.LinkWorktreePaths(gitRoot, wtPath, paths)
}
