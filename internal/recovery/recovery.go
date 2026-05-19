// Package recovery detects dirty working trees and resets them so tasks
// can be retried from a clean state.
package recovery

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Action describes the outcome of a recovery check.
type Action int

const (
	Continue  Action = 0
	RetryTask Action = 1
)

func (a Action) String() string {
	switch a {
	case Continue:
		return "continue"
	case RetryTask:
		return "retry"
	default:
		return fmt.Sprintf("Action(%d)", int(a))
	}
}

// CheckResult carries the recovery verdict and any dirty files found.
type CheckResult struct {
	Action     Action
	DirtyFiles []string
	Message    string
}

// Manager performs recovery checks in a git working directory.
type Manager struct {
	WorkDir string
}

// NewManager creates a Manager for the given working directory.
func NewManager(workDir string) *Manager {
	return &Manager{WorkDir: workDir}
}

// Check inspects the git working tree. A clean tree yields Continue;
// a dirty tree is reset via checkout+clean and yields RetryTask.
func (m *Manager) Check() (*CheckResult, error) {
	dirty, files, err := m.isDirty()
	if err != nil {
		return nil, fmt.Errorf("checking dirty state: %w", err)
	}

	if !dirty {
		return &CheckResult{
			Action:  Continue,
			Message: "working tree clean",
		}, nil
	}

	// `git checkout .` fails with "pathspec '.' did not match any file(s)"
	// when HEAD has no tracked files (e.g. fresh repo with only an empty
	// initial commit). Skip the checkout in that case — there's nothing
	// tracked to reset — and still let `git clean -fd` remove untracked junk.
	tracked, err := m.git("ls-files")
	if err != nil {
		return nil, fmt.Errorf("listing tracked files: %w", err)
	}
	if strings.TrimSpace(tracked) != "" {
		if _, err := m.git("checkout", "."); err != nil {
			return nil, fmt.Errorf("resetting tracked files: %w", err)
		}
	}
	// Preserve `.corvex` — it's the project's config directory in the
	// main repo and a symlink in worktrees. Both are untracked by design,
	// so a vanilla `git clean -fd` would wipe them and break the next
	// `corvex run` ("`.corvex` directory not found"). `-e` accepts a
	// .gitignore-style pattern.
	if _, err := m.git("clean", "-fd", "-e", ".corvex"); err != nil {
		return nil, fmt.Errorf("cleaning untracked files: %w", err)
	}

	return &CheckResult{
		Action:     RetryTask,
		DirtyFiles: files,
		Message:    fmt.Sprintf("reset %d dirty file(s)", len(files)),
	}, nil
}

// MarkCheckpoint stages all changes and commits with a corvex checkpoint
// message. If there are no staged changes the call is a no-op.
func (m *Manager) MarkCheckpoint(taskID string) error {
	if _, err := m.git("add", "-A"); err != nil {
		return fmt.Errorf("staging files: %w", err)
	}

	_, err := m.git("diff", "--cached", "--quiet")
	if err == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if !isExitError(err, &exitErr) {
		return fmt.Errorf("checking staged changes: %w", err)
	}

	msg := fmt.Sprintf("corvex: checkpoint %s", taskID)
	if _, err := m.git("commit", "-m", msg); err != nil {
		return fmt.Errorf("committing checkpoint: %w", err)
	}

	return nil
}

func (m *Manager) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = m.WorkDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}

	return stdout.String(), nil
}

func (m *Manager) isDirty() (bool, []string, error) {
	out, err := m.git("status", "--porcelain")
	if err != nil {
		return false, nil, err
	}

	trimmed := strings.TrimRight(out, "\n\r ")
	if trimmed == "" {
		return false, nil, nil
	}

	var files []string
	for _, line := range strings.Split(trimmed, "\n") {
		if len(line) > 3 {
			files = append(files, line[3:])
		} else if len(line) > 0 {
			files = append(files, strings.TrimSpace(line))
		}
	}

	return true, files, nil
}

func isExitError(err error, target **exec.ExitError) bool {
	for err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			*target = e
			return true
		}
		if unwrapper, ok := err.(interface{ Unwrap() error }); ok {
			err = unwrapper.Unwrap()
		} else {
			return false
		}
	}
	return false
}
