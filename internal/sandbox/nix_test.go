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

func newNixSandboxWith(workDir string) *NixSandbox {
	return NewNixSandbox(config.SandboxConfig{
		Profile: "nix",
		WorkDir: workDir,
	})
}

func writeFlake(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write flake.nix: %v", err)
	}
}

func TestNixSandbox_IsAvailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		runner    func(ctx context.Context, name string, args ...string) *exec.Cmd
		available bool
	}{
		{
			name: "nix present",
			runner: func(ctx context.Context, name string, args ...string) *exec.Cmd {
				return exec.CommandContext(ctx, "echo", "nix (Nix) 2.20.0")
			},
			available: true,
		},
		{
			name: "nix absent",
			runner: func(ctx context.Context, name string, args ...string) *exec.Cmd {
				return exec.CommandContext(ctx, "__corvex_no_such_binary__")
			},
			available: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newNixSandboxWith(t.TempDir())
			s.cmdRunner = tt.runner
			if got := s.IsAvailable(context.Background()); got != tt.available {
				t.Errorf("IsAvailable() = %v, want %v", got, tt.available)
			}
		})
	}
}

func TestNixSandbox_Prepare_RequiresFlake(t *testing.T) {
	t.Parallel()

	s := newNixSandboxWith(t.TempDir())
	if err := s.Prepare(context.Background()); err == nil {
		t.Fatal("Prepare() expected error when flake.nix is missing, got nil")
	}
	if s.prepared {
		t.Error("prepared should remain false when Prepare fails")
	}
}

func TestNixSandbox_Prepare_FlakePresent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFlake(t, dir)

	s := newNixSandboxWith(dir)
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !s.prepared {
		t.Error("prepared should be true after Prepare()")
	}
}

func TestNixSandbox_Run_NotPrepared(t *testing.T) {
	t.Parallel()

	s := newNixSandboxWith(t.TempDir())
	_, err := s.Run(context.Background(), RunRequest{Command: []string{"true"}})
	if !errors.Is(err, ErrNotPrepared) {
		t.Errorf("Run() error = %v, want ErrNotPrepared", err)
	}
}

func TestNixSandbox_Run_WrapsCommandWithNixDevelop(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFlake(t, dir)

	s := newNixSandboxWith(dir)
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	var capturedBin string
	var capturedArgs []string
	s.cmdRunner = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		capturedBin = name
		capturedArgs = args
		// Return a harmless command to keep Run() happy.
		return exec.CommandContext(ctx, "echo", "ok")
	}

	_, err := s.Run(context.Background(), RunRequest{
		Command: []string{"claude", "--model", "sonnet"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if capturedBin != "nix" {
		t.Errorf("invoked binary = %q, want %q", capturedBin, "nix")
	}
	want := []string{"develop", "--command", "claude", "--model", "sonnet"}
	if len(capturedArgs) != len(want) {
		t.Fatalf("args = %v, want %v", capturedArgs, want)
	}
	for i, w := range want {
		if capturedArgs[i] != w {
			t.Errorf("args[%d] = %q, want %q", i, capturedArgs[i], w)
		}
	}
}

func TestNixSandbox_Run_PropagatesExitCode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFlake(t, dir)

	s := newNixSandboxWith(dir)
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	s.cmdRunner = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo out; echo err >&2; exit 7")
	}

	got, err := s.Run(context.Background(), RunRequest{Command: []string{"whatever"}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", got.ExitCode)
	}
	if got.Stdout != "out\n" {
		t.Errorf("Stdout = %q, want %q", got.Stdout, "out\n")
	}
	if got.Stderr != "err\n" {
		t.Errorf("Stderr = %q, want %q", got.Stderr, "err\n")
	}
}

func TestNixSandbox_Cleanup_IsNoop(t *testing.T) {
	t.Parallel()
	s := newNixSandboxWith(t.TempDir())
	if err := s.Cleanup(context.Background()); err != nil {
		t.Errorf("Cleanup() error = %v", err)
	}
}
