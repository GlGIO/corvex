package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
)

// DockerSandbox executes commands inside a Docker container.
type DockerSandbox struct {
	image         string
	mount         string
	workDir       string
	containerID   string
	containerName string
	prepared      bool
	cmdRunner     func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// NewDockerSandbox creates a DockerSandbox from the provided configuration.
func NewDockerSandbox(cfg config.SandboxConfig) *DockerSandbox {
	return &DockerSandbox{
		image:     cfg.Image,
		mount:     cfg.Mount,
		workDir:   cfg.WorkDir,
		cmdRunner: exec.CommandContext,
	}
}

func (d *DockerSandbox) Prepare(ctx context.Context) error {
	name, err := generateContainerName()
	if err != nil {
		return fmt.Errorf("%w: generating container name: %v", ErrContainerFailed, err)
	}
	d.containerName = name

	mount, err := resolveMountPath(d.mount)
	if err != nil {
		return fmt.Errorf("%w: resolving mount path: %v", ErrContainerFailed, err)
	}

	args := d.buildRunArgs(mount)
	cmd := d.cmdRunner(ctx, "docker", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: docker run: %s", ErrContainerFailed, strings.TrimSpace(stderr.String()))
	}

	d.containerID = strings.TrimSpace(stdout.String())
	d.prepared = true
	return nil
}

func (d *DockerSandbox) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if !d.prepared {
		return nil, ErrNotPrepared
	}

	args := d.buildExecArgs(req)
	cmd := d.cmdRunner(ctx, "docker", args...)

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
		return nil, fmt.Errorf("%w: docker exec: %v", ErrContainerFailed, err)
	}

	return &RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}, nil
}

func (d *DockerSandbox) Cleanup(ctx context.Context) error {
	if d.containerID == "" {
		return nil
	}

	cmd := d.cmdRunner(ctx, "docker", "rm", "-f", d.containerID)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: docker rm: %s", ErrContainerFailed, strings.TrimSpace(stderr.String()))
	}

	d.containerID = ""
	d.containerName = ""
	d.prepared = false
	return nil
}

func (d *DockerSandbox) IsAvailable(ctx context.Context) bool {
	cmd := d.cmdRunner(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	return cmd.Run() == nil
}

func (d *DockerSandbox) buildRunArgs(mount string) []string {
	args := []string{
		"run", "-d",
		"--name", d.containerName,
	}
	if mount != "" {
		args = append(args, "-v", mount)
	}
	if d.workDir != "" {
		args = append(args, "-w", d.workDir)
	}
	args = append(args, d.image, "tail", "-f", "/dev/null")
	return args
}

func (d *DockerSandbox) buildExecArgs(req RunRequest) []string {
	args := []string{"exec"}
	for k, v := range req.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, d.containerName)
	args = append(args, req.Command...)
	return args
}

func generateContainerName() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "corvex-" + hex.EncodeToString(b), nil
}

func resolveMountPath(mount string) (string, error) {
	if mount == "" {
		return "", nil
	}

	parts := strings.SplitN(mount, ":", 2)
	hostPath := parts[0]

	if !filepath.IsAbs(hostPath) {
		abs, err := filepath.Abs(hostPath)
		if err != nil {
			return "", err
		}
		hostPath = abs
	}

	if len(parts) == 2 {
		return hostPath + ":" + parts[1], nil
	}
	return hostPath, nil
}
