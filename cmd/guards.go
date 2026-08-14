package cmd

import (
	"fmt"

	"github.com/giovannialves/corvex/internal/ops"
)

// Preconditions every project-scoped command checks before doing work. The
// decision is in internal/ops; the sentence — with its CLI-specific hint — is
// here, because a hint that says "pass --here" only makes sense in a terminal.

// requireCorvexDir fails when the workspace has no usable .corvex directory,
// telling the user how to fix the specific case they hit.
func requireCorvexDir(workDir string) error {
	st, err := ops.InspectWorkspaceDir(workDir)
	if err != nil {
		return err
	}
	switch st.Problem {
	case ops.WorkspaceDirMissing:
		return fmt.Errorf(".corvex directory not found in %s — run 'corvex init' first (or if this is a worktree, recreate the symlink: ln -s <main-repo>/.corvex .corvex)", workDir)
	case ops.WorkspaceDirDanglingSymlink:
		return fmt.Errorf(".corvex is a symlink but points to a missing target (%w) — recreate it: rm %s && ln -s <main-repo>/.corvex %s", st.Err, st.Path, st.Path)
	}
	return nil
}

// checkWorktreeMismatch refuses to operate from the main repo when a worktree
// for the project exists elsewhere — running there would write to the wrong
// branch and bypass the worktree. `here` (the command's --here flag) overrides.
// cmdName tailors the hint (e.g. "run", "plan").
func checkWorktreeMismatch(workDir, project, cmdName string, here bool) error {
	if here {
		return nil
	}
	m := ops.CheckWorktreeMismatch(workDir, project)
	if m == nil {
		return nil
	}
	return fmt.Errorf("worktree for project %q exists at %s, but you are running from %s.\nThis would use the wrong branch/state and bypass the worktree.\n\n→ cd %s && corvex %s %s\n\nOr pass --here to use the current directory anyway", m.Project, m.WorktreePath, m.WorkDir, m.WorktreePath, cmdName, m.Project)
}
