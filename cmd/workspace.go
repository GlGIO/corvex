package cmd

import "github.com/giovannialves/corvex/internal/ops"

// workspaceDir resolves the repository this invocation is standing in and
// refuses early when there is no `.corvex/` — the two lines every command that
// reads local state begins with, in one place.
func workspaceDir() (string, error) {
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return "", err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return "", err
	}
	return workDir, nil
}

// optionalWorkspaceDir is workspaceDir for the commands that answer from the
// global index and therefore work anywhere: standing outside a repository is
// not an error, it just means there is no local repository to add.
func optionalWorkspaceDir() string {
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return ""
	}
	return workDir
}
