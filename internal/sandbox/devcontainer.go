package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
)

// DevcontainerSandbox delegates lifecycle to the official `devcontainer` CLI
// (https://github.com/devcontainers/cli), so any repo with a working
// `.devcontainer/devcontainer.json` becomes a usable Worker sandbox without
// Corvex re-implementing the spec.
//
// Lifecycle:
//
//	Prepare  → `devcontainer up --workspace-folder <workDir>`
//	Run      → `devcontainer exec --workspace-folder <workDir> -- <cmd...>`
//	Cleanup  → no-op; the user manages container lifecycle outside Corvex.
type DevcontainerSandbox struct {
	workDir   string
	prepared  bool
	cmdRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func NewDevcontainerSandbox(cfg config.SandboxConfig) *DevcontainerSandbox {
	wd := cfg.WorkDir
	if wd == "" {
		wd, _ = os.Getwd()
	}
	return &DevcontainerSandbox{
		workDir:   wd,
		cmdRunner: exec.CommandContext,
	}
}

func (d *DevcontainerSandbox) Prepare(ctx context.Context) error {
	dcJSON := filepath.Join(d.workDir, ".devcontainer", "devcontainer.json")
	if _, err := os.Stat(dcJSON); err != nil {
		// Some repos place devcontainer.json directly at the workspace root.
		alt := filepath.Join(d.workDir, ".devcontainer.json")
		if _, altErr := os.Stat(alt); altErr != nil {
			return fmt.Errorf("devcontainer sandbox: no devcontainer.json found at %s or %s", dcJSON, alt)
		}
	}

	cmd := d.cmdRunner(ctx, "devcontainer", "up", "--workspace-folder", d.workDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("devcontainer up: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	d.prepared = true
	return nil
}

func (d *DevcontainerSandbox) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if !d.prepared {
		return nil, ErrNotPrepared
	}

	args := []string{"exec", "--workspace-folder", d.workDir}
	// `devcontainer exec` forwards remaining args directly to the command;
	// it does not need an explicit separator, but adding one keeps things
	// unambiguous when a command contains flags starting with `--`.
	args = append(args, req.Command...)

	cmd := d.cmdRunner(ctx, "devcontainer", args...)
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
		return nil, fmt.Errorf("devcontainer exec: %w", err)
	}

	return &RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}, nil
}

func (d *DevcontainerSandbox) Cleanup(_ context.Context) error {
	return nil
}

func (d *DevcontainerSandbox) IsAvailable(ctx context.Context) bool {
	cmd := d.cmdRunner(ctx, "devcontainer", "--version")
	return cmd.Run() == nil
}
