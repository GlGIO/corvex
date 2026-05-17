package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/giovannialves/corvex/internal/config"
)

// NixSandbox executes commands inside a `nix develop` shell derived from the
// project's flake.nix. Useful when the repo already declares its dev
// environment via Nix — the Worker inherits compilers, language runtimes, and
// tooling without Corvex needing its own Docker image.
//
// The host PATH is appended after the Nix shell PATH so tools installed
// outside the flake (notably the Claude CLI) remain reachable.
type NixSandbox struct {
	workDir   string
	prepared  bool
	cmdRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func NewNixSandbox(cfg config.SandboxConfig) *NixSandbox {
	wd := cfg.WorkDir
	if wd == "" {
		wd, _ = os.Getwd()
	}
	return &NixSandbox{
		workDir:   wd,
		cmdRunner: exec.CommandContext,
	}
}

func (s *NixSandbox) Prepare(ctx context.Context) error {
	flake := filepath.Join(s.workDir, "flake.nix")
	if _, err := os.Stat(flake); err != nil {
		return fmt.Errorf("nix sandbox: flake.nix not found at %s: %w", flake, err)
	}
	s.prepared = true
	return nil
}

func (s *NixSandbox) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if !s.prepared {
		return nil, ErrNotPrepared
	}

	// `nix develop --command <cmd> <args...>` enters the flake's devShell and
	// runs the command with the resolved environment.
	args := append([]string{"develop", "--command"}, req.Command...)
	cmd := s.cmdRunner(ctx, "nix", args...)
	cmd.Dir = s.workDir
	cmd.Env = mergeEnv(os.Environ(), req.Env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &RunResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: exitErr.ExitCode(),
			}, nil
		}
		return nil, fmt.Errorf("nix sandbox run: %w", err)
	}

	return &RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}, nil
}

func (s *NixSandbox) Cleanup(_ context.Context) error {
	return nil
}

func (s *NixSandbox) IsAvailable(ctx context.Context) bool {
	cmd := s.cmdRunner(ctx, "nix", "--version")
	return cmd.Run() == nil
}
