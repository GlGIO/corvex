package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
)

func newDevcontainerSandboxWith(workDir string) *DevcontainerSandbox {
	return NewDevcontainerSandbox(config.SandboxConfig{
		Profile: "devcontainer",
		WorkDir: workDir,
	})
}

func writeDevcontainerJSON(t *testing.T, dir string) {
	t.Helper()
	sub := filepath.Join(dir, ".devcontainer")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir .devcontainer: %v", err)
	}
	body := `{"image":"mcr.microsoft.com/devcontainers/base:ubuntu"}`
	if err := os.WriteFile(filepath.Join(sub, "devcontainer.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write devcontainer.json: %v", err)
	}
}

func TestDevcontainerSandbox_IsAvailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		runner    func(ctx context.Context, name string, args ...string) *exec.Cmd
		available bool
	}{
		{
			name: "cli present",
			runner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
				return exec.CommandContext(ctx, "echo", "0.55.0")
			},
			available: true,
		},
		{
			name: "cli absent",
			runner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
				return exec.CommandContext(ctx, "__corvex_no_such_binary__")
			},
			available: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newDevcontainerSandboxWith(t.TempDir())
			s.cmdRunner = tt.runner
			if got := s.IsAvailable(context.Background()); got != tt.available {
				t.Errorf("IsAvailable() = %v, want %v", got, tt.available)
			}
		})
	}
}

func TestDevcontainerSandbox_Prepare_RequiresConfig(t *testing.T) {
	t.Parallel()
	s := newDevcontainerSandboxWith(t.TempDir())
	if err := s.Prepare(context.Background()); err == nil {
		t.Fatal("Prepare() expected error when devcontainer.json is missing, got nil")
	}
}

func TestDevcontainerSandbox_Prepare_Success(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeDevcontainerJSON(t, dir)

	s := newDevcontainerSandboxWith(dir)

	var calledWith []string
	s.cmdRunner = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		calledWith = args
		return exec.CommandContext(ctx, "echo", "up ok")
	}

	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !s.prepared {
		t.Error("prepared should be true after Prepare()")
	}

	wantPrefix := []string{"up", "--workspace-folder", dir}
	if len(calledWith) < len(wantPrefix) {
		t.Fatalf("Prepare args = %v, want prefix %v", calledWith, wantPrefix)
	}
	for i, w := range wantPrefix {
		if calledWith[i] != w {
			t.Errorf("Prepare args[%d] = %q, want %q", i, calledWith[i], w)
		}
	}
}

func TestDevcontainerSandbox_Run_NotPrepared(t *testing.T) {
	t.Parallel()
	s := newDevcontainerSandboxWith(t.TempDir())
	_, err := s.Run(context.Background(), RunRequest{Command: []string{"true"}})
	if !errors.Is(err, ErrNotPrepared) {
		t.Errorf("Run() error = %v, want ErrNotPrepared", err)
	}
}

func TestDevcontainerSandbox_Run_BuildsExecArgs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeDevcontainerJSON(t, dir)

	s := newDevcontainerSandboxWith(dir)

	// First Prepare with a stub that succeeds.
	s.cmdRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", "up")
	}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	// Capture Run args.
	var captured []string
	s.cmdRunner = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		captured = args
		return exec.CommandContext(ctx, "echo", "exec ok")
	}

	if _, err := s.Run(context.Background(), RunRequest{Command: []string{"claude", "--model", "sonnet"}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []string{"exec", "--workspace-folder", dir, "claude", "--model", "sonnet"}
	if len(captured) != len(want) {
		t.Fatalf("Run args = %v, want %v", captured, want)
	}
	for i, w := range want {
		if captured[i] != w {
			t.Errorf("Run args[%d] = %q, want %q", i, captured[i], w)
		}
	}
}
