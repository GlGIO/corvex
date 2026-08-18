// Package sandbox provides execution isolation for Corvex tasks,
// supporting both local and Docker-based environments.
package sandbox

import (
	"context"
	"errors"
)

var (
	ErrNotPrepared     = errors.New("sandbox not prepared; call Prepare first")
	ErrNotAvailable    = errors.New("sandbox type not available")
	ErrContainerFailed = errors.New("docker container operation failed")
)

// RunRequest describes a command to execute inside a sandbox.
type RunRequest struct {
	Command []string
	Env     map[string]string
	// DenyEnv are variable NAMES the child must not inherit from this process,
	// whatever the host environment holds (F9 credential custody).
	//
	// It has to be applied HERE, where the process is actually created, and not
	// only where the forwarded set is computed: every sandbox starts the child
	// from os.Environ(), so a variable "not forwarded" was still inherited. The
	// audit that found this walked a denied credential straight into the worker
	// on the default configuration.
	DenyEnv []string
}

// RunResult holds the output and exit code of a sandbox command execution.
type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Sandbox defines the lifecycle for an isolated execution environment.
type Sandbox interface {
	Prepare(ctx context.Context) error
	Run(ctx context.Context, req RunRequest) (*RunResult, error)
	Cleanup(ctx context.Context) error
	IsAvailable(ctx context.Context) bool
}
