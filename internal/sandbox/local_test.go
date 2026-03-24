package sandbox

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
)

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
}

func newPreparedLocal(t *testing.T) *LocalSandbox {
	t.Helper()
	s := NewLocalSandbox(config.SandboxConfig{WorkDir: t.TempDir()})
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	return s
}

func TestLocalSandbox_IsAvailable(t *testing.T) {
	t.Parallel()
	s := NewLocalSandbox(config.SandboxConfig{})
	if !s.IsAvailable(context.Background()) {
		t.Error("IsAvailable() = false, want true")
	}
}

func TestLocalSandbox_Prepare(t *testing.T) {
	t.Parallel()
	s := NewLocalSandbox(config.SandboxConfig{})
	if s.prepared {
		t.Fatal("prepared should be false before Prepare()")
	}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !s.prepared {
		t.Error("prepared should be true after Prepare()")
	}
}

func TestLocalSandbox_Run(t *testing.T) {
	skipWindows(t)

	tests := []struct {
		name       string
		command    []string
		env        map[string]string
		prepare    bool
		wantOut    string
		wantExit   int
		wantErr    bool
		wantErrIs  error
		wantStderr string
	}{
		{
			name:     "simple echo",
			command:  []string{"echo", "hello"},
			prepare:  true,
			wantOut:  "hello\n",
			wantExit: 0,
		},
		{
			name:     "exit code 1",
			command:  []string{"sh", "-c", "echo fail >&2; exit 1"},
			prepare:  true,
			wantOut:  "",
			wantExit: 1,
			wantStderr: "fail\n",
		},
		{
			name:    "with env var",
			command: []string{"sh", "-c", "echo $CORVEX_TEST_VAR"},
			env:     map[string]string{"CORVEX_TEST_VAR": "hello_from_env"},
			prepare: true,
			wantOut: "hello_from_env\n",
		},
		{
			name:      "not prepared",
			command:   []string{"echo", "nope"},
			prepare:   false,
			wantErr:   true,
			wantErrIs: ErrNotPrepared,
		},
		{
			name:    "invalid command",
			command: []string{"__corvex_nonexistent_cmd__"},
			prepare: true,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := NewLocalSandbox(config.SandboxConfig{WorkDir: t.TempDir()})
			if tt.prepare {
				if err := s.Prepare(context.Background()); err != nil {
					t.Fatalf("Prepare() error = %v", err)
				}
			}

			got, err := s.Run(context.Background(), RunRequest{
				Command: tt.command,
				Env:     tt.env,
			})

			if tt.wantErr {
				if err == nil {
					t.Fatal("Run() expected error, got nil")
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Errorf("Run() error = %v, want %v", err, tt.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("Run() unexpected error = %v", err)
			}
			if got.ExitCode != tt.wantExit {
				t.Errorf("ExitCode = %d, want %d", got.ExitCode, tt.wantExit)
			}
			if tt.wantOut != "" && got.Stdout != tt.wantOut {
				t.Errorf("Stdout = %q, want %q", got.Stdout, tt.wantOut)
			}
			if tt.wantStderr != "" && got.Stderr != tt.wantStderr {
				t.Errorf("Stderr = %q, want %q", got.Stderr, tt.wantStderr)
			}
		})
	}
}

func TestLocalSandbox_Cleanup(t *testing.T) {
	t.Parallel()
	s := newPreparedLocal(t)
	if err := s.Cleanup(context.Background()); err != nil {
		t.Errorf("Cleanup() error = %v", err)
	}
}

func TestLocalSandbox_WorkDir(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	dir := t.TempDir()
	// Resolve symlinks (macOS /var → /private/var)
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}

	s := NewLocalSandbox(config.SandboxConfig{WorkDir: dir})
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	got, runErr := s.Run(context.Background(), RunRequest{Command: []string{"pwd"}})
	if runErr != nil {
		t.Fatalf("Run() error = %v", runErr)
	}
	actual := strings.TrimSpace(got.Stdout)
	if actual != dir && actual != realDir {
		t.Errorf("pwd = %q, want %q or %q", actual, dir, realDir)
	}
}

func TestMergeEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		base  []string
		extra map[string]string
		want  int
	}{
		{
			name:  "empty extra",
			base:  []string{"A=1"},
			extra: nil,
			want:  1,
		},
		{
			name:  "adds extra",
			base:  []string{"A=1"},
			extra: map[string]string{"B": "2", "C": "3"},
			want:  3,
		},
		{
			name:  "both empty",
			base:  nil,
			extra: nil,
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mergeEnv(tt.base, tt.extra)
			if len(got) != tt.want {
				t.Errorf("mergeEnv() len = %d, want %d", len(got), tt.want)
			}
		})
	}
}
