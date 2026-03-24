package hooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func setupHooksDir(t *testing.T, scripts map[string]string) string {
	t.Helper()

	workDir := t.TempDir()
	hooksDir := filepath.Join(workDir, ".corvex", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatal(err)
	}

	for name, content := range scripts {
		path := filepath.Join(hooksDir, name+".sh")
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}

	return workDir
}

func TestNewRunner(t *testing.T) {
	t.Parallel()

	t.Run("sets default timeout when zero", func(t *testing.T) {
		t.Parallel()
		r := NewRunner("/tmp/proj", 0)
		if r.Timeout != DefaultTimeout {
			t.Errorf("Timeout = %v, want %v", r.Timeout, DefaultTimeout)
		}
	})

	t.Run("uses provided timeout", func(t *testing.T) {
		t.Parallel()
		r := NewRunner("/tmp/proj", 5*time.Second)
		if r.Timeout != 5*time.Second {
			t.Errorf("Timeout = %v, want %v", r.Timeout, 5*time.Second)
		}
	})

	t.Run("sets HooksDir correctly", func(t *testing.T) {
		t.Parallel()
		r := NewRunner("/tmp/proj", 0)
		want := filepath.Join("/tmp/proj", ".corvex", "hooks")
		if r.HooksDir != want {
			t.Errorf("HooksDir = %q, want %q", r.HooksDir, want)
		}
	})
}

func TestRunnerRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts require unix")
	}

	tests := []struct {
		name       string
		scripts    map[string]string
		hookName   string
		env        HookEnv
		timeout    time.Duration
		wantNil    bool
		wantStdout string
		wantStderr string
		wantExit   int
		wantErr    error
	}{
		{
			name:     "missing hook returns nil nil",
			scripts:  nil,
			hookName: PreTask,
			wantNil:  true,
		},
		{
			name:       "existing hook succeeds",
			scripts:    map[string]string{PreTask: "#!/bin/sh\necho hello"},
			hookName:   PreTask,
			wantStdout: "hello\n",
		},
		{
			name:     "hook failure returns ErrHookFailed",
			scripts:  map[string]string{PostTask: "#!/bin/sh\nexit 1"},
			hookName: PostTask,
			wantExit: 1,
			wantErr:  ErrHookFailed,
		},
		{
			name:     "hook exit code 2",
			scripts:  map[string]string{OnFailure: "#!/bin/sh\nexit 2"},
			hookName: OnFailure,
			wantExit: 2,
			wantErr:  ErrHookFailed,
		},
		{
			name:     "hook timeout returns ErrHookTimeout",
			scripts:  map[string]string{PreTask: "#!/bin/sh\nsleep 2"},
			hookName: PreTask,
			timeout:  150 * time.Millisecond,
			wantErr:  ErrHookTimeout,
		},
		{
			name: "env vars injected",
			scripts: map[string]string{
				OnSuccess: "#!/bin/sh\necho \"$CORVEX_TASK_ID $CORVEX_PROJECT $CORVEX_STATUS\"",
			},
			hookName:   OnSuccess,
			env:        HookEnv{TaskID: "S01", Project: "myproject", Status: "PASSED"},
			wantStdout: "S01 myproject PASSED\n",
		},
		{
			name:       "stderr captured",
			scripts:    map[string]string{PreTask: "#!/bin/sh\necho err >&2"},
			hookName:   PreTask,
			wantStderr: "err\n",
		},
		{
			name: "stdout and stderr combined",
			scripts: map[string]string{
				PreTask: "#!/bin/sh\necho out\necho err >&2",
			},
			hookName:   PreTask,
			wantStdout: "out\n",
			wantStderr: "err\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			workDir := setupHooksDir(t, tt.scripts)
			timeout := tt.timeout
			if timeout == 0 {
				timeout = 5 * time.Second
			}

			runner := NewRunner(workDir, timeout)
			res, err := runner.Run(context.Background(), tt.hookName, tt.env)

			if tt.wantNil {
				if res != nil {
					t.Errorf("result = %+v, want nil", res)
				}
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
				return
			}

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want error wrapping %v", err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}

			if res == nil {
				t.Fatal("result is nil, want non-nil")
			}

			if tt.wantStdout != "" && res.Stdout != tt.wantStdout {
				t.Errorf("Stdout = %q, want %q", res.Stdout, tt.wantStdout)
			}
			if tt.wantStderr != "" && res.Stderr != tt.wantStderr {
				t.Errorf("Stderr = %q, want %q", res.Stderr, tt.wantStderr)
			}
			if tt.wantExit != 0 && res.ExitCode != tt.wantExit {
				t.Errorf("ExitCode = %d, want %d", res.ExitCode, tt.wantExit)
			}
		})
	}
}

func TestRunnerExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts require unix")
	}
	t.Parallel()

	scripts := map[string]string{
		PreTask: "#!/bin/sh\necho ok",
	}
	workDir := setupHooksDir(t, scripts)
	runner := NewRunner(workDir, DefaultTimeout)

	t.Run("returns true for existing hook", func(t *testing.T) {
		t.Parallel()
		if !runner.Exists(PreTask) {
			t.Error("Exists(pre-task) = false, want true")
		}
	})

	t.Run("returns false for missing hook", func(t *testing.T) {
		t.Parallel()
		if runner.Exists(PostTask) {
			t.Error("Exists(post-task) = true, want false")
		}
	})

	t.Run("returns false for different hook name", func(t *testing.T) {
		t.Parallel()
		if runner.Exists("nonexistent") {
			t.Error("Exists(nonexistent) = true, want false")
		}
	})
}

func TestRunnerWorkDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts require unix")
	}
	t.Parallel()

	workDir := setupHooksDir(t, map[string]string{
		PreTask: "#!/bin/sh\npwd",
	})

	runner := NewRunner(workDir, 5*time.Second)
	res, err := runner.Run(context.Background(), PreTask, HookEnv{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := res.Stdout
	if len(got) == 0 {
		t.Fatal("Stdout is empty, expected working directory")
	}
	// pwd output should match workDir (resolve symlinks for macOS /private/tmp)
	gotResolved, _ := filepath.EvalSymlinks(got[:len(got)-1]) // strip newline
	wantResolved, _ := filepath.EvalSymlinks(workDir)
	if gotResolved != wantResolved {
		t.Errorf("working dir = %q, want %q", gotResolved, wantResolved)
	}
}

func TestRunnerContextCancelled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts require unix")
	}
	t.Parallel()

	workDir := setupHooksDir(t, map[string]string{
		PreTask: "#!/bin/sh\nsleep 60",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	runner := NewRunner(workDir, 5*time.Second)
	_, err := runner.Run(ctx, PreTask, HookEnv{})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestHookConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"PreTask", PreTask, "pre-task"},
		{"PostTask", PostTask, "post-task"},
		{"OnSuccess", OnSuccess, "on-success"},
		{"OnFailure", OnFailure, "on-failure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}
