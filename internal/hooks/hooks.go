// Package hooks executes lifecycle shell scripts around task execution.
package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	PreTask   = "pre-task"
	PostTask  = "post-task"
	OnSuccess = "on-success"
	OnFailure = "on-failure"
)

// DefaultTimeout is applied when Runner is created with zero timeout.
const DefaultTimeout = 30 * time.Second

// HookEnv carries contextual data injected as environment variables.
type HookEnv struct {
	TaskID  string
	Project string
	Status  string
}

// Result captures the output of a hook script execution.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

var (
	ErrHookTimeout = errors.New("hook timed out")
	ErrHookFailed  = errors.New("hook failed")
)

// Runner executes hook scripts from a project's .corvex/hooks/ directory.
type Runner struct {
	HooksDir string
	Timeout  time.Duration
	workDir  string
}

// NewRunner creates a Runner pointing at .corvex/hooks/ inside workDir.
func NewRunner(workDir string, timeout time.Duration) *Runner {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Runner{
		HooksDir: filepath.Join(workDir, ".corvex", "hooks"),
		Timeout:  timeout,
		workDir:  workDir,
	}
}

// Run executes the named hook script. If the script does not exist it returns
// (nil, nil) to signal a skip. Timeout and non-zero exit codes produce
// ErrHookTimeout and ErrHookFailed respectively.
func (r *Runner) Run(ctx context.Context, name string, env HookEnv) (*Result, error) {
	path := r.scriptPath(name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", path)
	cmd.Dir = r.workDir

	cmd.Env = append(os.Environ(),
		"CORVEX_TASK_ID="+env.TaskID,
		"CORVEX_PROJECT="+env.Project,
		"CORVEX_STATUS="+env.Status,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	res := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return res, fmt.Errorf("%s: %w", name, ErrHookTimeout)
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, fmt.Errorf("%s exited %d: %w", name, res.ExitCode, ErrHookFailed)
		}

		return res, fmt.Errorf("running hook %s: %w", name, err)
	}

	return res, nil
}

// Exists reports whether the named hook script is present on disk.
func (r *Runner) Exists(name string) bool {
	_, err := os.Stat(r.scriptPath(name))
	return err == nil
}

func (r *Runner) scriptPath(name string) string {
	return filepath.Join(r.HooksDir, name+".sh")
}
