package ops

import (
	"fmt"
	"os"
	"path/filepath"
)

// WorkspaceDirProblem says why a workspace's .corvex directory is unusable.
type WorkspaceDirProblem int

const (
	// WorkspaceDirOK means .corvex exists and resolves.
	WorkspaceDirOK WorkspaceDirProblem = iota
	// WorkspaceDirMissing means there is no .corvex entry at all.
	WorkspaceDirMissing
	// WorkspaceDirDanglingSymlink means .corvex is a symlink whose target is
	// gone — the usual shape of a worktree whose main repo moved.
	WorkspaceDirDanglingSymlink
)

// WorkspaceDirStatus reports the state of a workspace's .corvex directory. The
// caller turns it into a message; ops does not phrase hints.
type WorkspaceDirStatus struct {
	Problem WorkspaceDirProblem
	// Path is the .corvex path that was inspected.
	Path string
	// Err is the filesystem error behind WorkspaceDirDanglingSymlink, nil
	// otherwise.
	Err error
}

// InspectWorkspaceDir checks that workDir has a usable .corvex directory.
//
// Lstat doesn't follow symlinks — this distinguishes "not there at all" from
// "broken symlink pointing somewhere that no longer exists" so callers in a
// worktree can give a more actionable hint than "run corvex init". The returned
// error is reserved for an Lstat failure that is neither of those cases.
func InspectWorkspaceDir(workDir string) (WorkspaceDirStatus, error) {
	corvexDir := filepath.Join(workDir, ".corvex")
	st := WorkspaceDirStatus{Path: corvexDir}

	info, lstatErr := os.Lstat(corvexDir)
	if os.IsNotExist(lstatErr) {
		st.Problem = WorkspaceDirMissing
		return st, nil
	}
	if lstatErr != nil {
		return st, fmt.Errorf("checking .corvex: %w", lstatErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if _, err := os.Stat(corvexDir); err != nil {
			st.Problem = WorkspaceDirDanglingSymlink
			st.Err = err
			return st, nil
		}
	}
	return st, nil
}
