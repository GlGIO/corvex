package sandbox

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
)

func newDockerSandbox(image string) *DockerSandbox {
	return NewDockerSandbox(config.SandboxConfig{
		Type:    "docker",
		Image:   image,
		Mount:   "./:/app",
		WorkDir: "/app",
	})
}

func TestDockerSandbox_BuildRunArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		image     string
		workDir   string
		contName  string
		mount     string
		wantParts []string
	}{
		{
			name:     "full args with mount and workdir",
			image:    "node:20-slim",
			workDir:  "/app",
			contName: "corvex-abc123",
			mount:    "/host:/container",
			wantParts: []string{
				"run", "-d",
				"--name", "corvex-abc123",
				"-v", "/host:/container",
				"-w", "/app",
				"node:20-slim", "tail", "-f", "/dev/null",
			},
		},
		{
			name:     "no mount",
			image:    "alpine:latest",
			workDir:  "/work",
			contName: "corvex-test",
			mount:    "",
			wantParts: []string{
				"run", "-d",
				"--name", "corvex-test",
				"-w", "/work",
				"alpine:latest", "tail", "-f", "/dev/null",
			},
		},
		{
			name:     "no workdir",
			image:    "alpine:latest",
			workDir:  "",
			contName: "corvex-test",
			mount:    "/a:/b",
			wantParts: []string{
				"run", "-d",
				"--name", "corvex-test",
				"-v", "/a:/b",
				"alpine:latest", "tail", "-f", "/dev/null",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ds := &DockerSandbox{
				image:         tt.image,
				workDir:       tt.workDir,
				containerName: tt.contName,
			}
			got := ds.buildRunArgs(tt.mount)
			if len(got) != len(tt.wantParts) {
				t.Fatalf("buildRunArgs() len = %d, want %d\ngot:  %v\nwant: %v", len(got), len(tt.wantParts), got, tt.wantParts)
			}
			for i, w := range tt.wantParts {
				if got[i] != w {
					t.Errorf("buildRunArgs()[%d] = %q, want %q", i, got[i], w)
				}
			}
		})
	}
}

func TestDockerSandbox_BuildExecArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contName string
		req      RunRequest
		wantLen  int
		wantHead string
		wantCmd  []string
	}{
		{
			name:     "no env",
			contName: "corvex-test",
			req: RunRequest{
				Command: []string{"echo", "hello"},
			},
			wantHead: "exec",
			wantCmd:  []string{"corvex-test", "echo", "hello"},
		},
		{
			name:     "with single env",
			contName: "corvex-test",
			req: RunRequest{
				Command: []string{"sh", "-c", "echo $FOO"},
				Env:     map[string]string{"FOO": "bar"},
			},
			wantHead: "exec",
			wantCmd:  []string{"corvex-test", "sh", "-c", "echo $FOO"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ds := &DockerSandbox{containerName: tt.contName}
			got := ds.buildExecArgs(tt.req)

			if got[0] != tt.wantHead {
				t.Errorf("buildExecArgs()[0] = %q, want %q", got[0], tt.wantHead)
			}

			// Verify the container name and command are at the end
			tail := got[len(got)-len(tt.wantCmd):]
			for i, w := range tt.wantCmd {
				if tail[i] != w {
					t.Errorf("buildExecArgs() tail[%d] = %q, want %q", i, tail[i], w)
				}
			}
		})
	}
}

func TestDockerSandbox_BuildExecArgs_WithEnv(t *testing.T) {
	t.Parallel()

	ds := &DockerSandbox{containerName: "corvex-test"}
	req := RunRequest{
		Command: []string{"cmd"},
		Env:     map[string]string{"KEY1": "val1"},
	}
	got := ds.buildExecArgs(req)

	found := false
	for i, a := range got {
		if a == "-e" && i+1 < len(got) && got[i+1] == "KEY1=val1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("buildExecArgs() missing -e KEY1=val1, got %v", got)
	}
}

func TestDockerSandbox_IsAvailable_NoDocker(t *testing.T) {
	t.Parallel()

	ds := newDockerSandbox("alpine:latest")
	ds.cmdRunner = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "__corvex_no_such_binary__")
	}

	if ds.IsAvailable(context.Background()) {
		t.Error("IsAvailable() = true, want false when docker is absent")
	}
}

func TestDockerSandbox_IsAvailable_DockerPresent(t *testing.T) {
	t.Parallel()

	ds := newDockerSandbox("alpine:latest")
	ds.cmdRunner = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", "24.0.0")
	}

	if !ds.IsAvailable(context.Background()) {
		t.Error("IsAvailable() = false, want true when docker is present")
	}
}

func TestDockerSandbox_Run_NotPrepared(t *testing.T) {
	t.Parallel()

	ds := newDockerSandbox("alpine:latest")
	_, err := ds.Run(context.Background(), RunRequest{Command: []string{"echo"}})
	if err == nil {
		t.Fatal("Run() expected error, got nil")
	}
	if !errors.Is(err, ErrNotPrepared) {
		t.Errorf("Run() error = %v, want ErrNotPrepared", err)
	}
}

func TestDockerSandbox_Cleanup_Idempotent(t *testing.T) {
	t.Parallel()

	ds := newDockerSandbox("alpine:latest")
	// containerID is empty, Cleanup should be a no-op
	if err := ds.Cleanup(context.Background()); err != nil {
		t.Errorf("Cleanup() with no container error = %v", err)
	}
}

func TestDockerSandbox_Prepare_MockSuccess(t *testing.T) {
	t.Parallel()

	ds := newDockerSandbox("alpine:latest")
	ds.cmdRunner = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", "abc123containerid")
	}

	if err := ds.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !ds.prepared {
		t.Error("prepared should be true after Prepare()")
	}
	if ds.containerID != "abc123containerid" {
		t.Errorf("containerID = %q, want %q", ds.containerID, "abc123containerid")
	}
	if !strings.HasPrefix(ds.containerName, "corvex-") {
		t.Errorf("containerName = %q, want prefix corvex-", ds.containerName)
	}
}

func TestDockerSandbox_Cleanup_WithContainer(t *testing.T) {
	t.Parallel()

	ds := newDockerSandbox("alpine:latest")
	ds.containerID = "test-container-id"
	ds.containerName = "corvex-test"
	ds.prepared = true

	ds.cmdRunner = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", "removed")
	}

	if err := ds.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if ds.containerID != "" {
		t.Errorf("containerID = %q, want empty after cleanup", ds.containerID)
	}
	if ds.containerName != "" {
		t.Errorf("containerName = %q, want empty after cleanup", ds.containerName)
	}
	if ds.prepared {
		t.Error("prepared should be false after cleanup")
	}
}

func TestGenerateContainerName(t *testing.T) {
	t.Parallel()

	re := regexp.MustCompile(`^corvex-[0-9a-f]{8}$`)

	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		name, err := generateContainerName()
		if err != nil {
			t.Fatalf("generateContainerName() error = %v", err)
		}
		if !re.MatchString(name) {
			t.Errorf("generateContainerName() = %q, want format corvex-XXXXXXXX (8 hex chars)", name)
		}
		if seen[name] {
			t.Errorf("generateContainerName() produced duplicate: %q", name)
		}
		seen[name] = true
	}
}

func TestResolveMountPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mount   string
		wantAbs bool
		wantErr bool
	}{
		{
			name:  "empty mount",
			mount: "",
		},
		{
			name:    "absolute path with target",
			mount:   "/host/path:/container/path",
			wantAbs: true,
		},
		{
			name:    "relative path with target",
			mount:   "./src:/app/src",
			wantAbs: true,
		},
		{
			name:    "absolute path without target",
			mount:   "/host/path",
			wantAbs: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveMountPath(tt.mount)

			if tt.wantErr {
				if err == nil {
					t.Fatal("resolveMountPath() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveMountPath() error = %v", err)
			}

			if tt.mount == "" {
				if got != "" {
					t.Errorf("resolveMountPath(%q) = %q, want empty", tt.mount, got)
				}
				return
			}

			if tt.wantAbs {
				hostPart := strings.SplitN(got, ":", 2)[0]
				if !filepath.IsAbs(hostPart) {
					t.Errorf("resolveMountPath(%q) host part = %q, want absolute path", tt.mount, hostPart)
				}
			}

			if strings.Contains(tt.mount, ":") {
				parts := strings.SplitN(got, ":", 2)
				if len(parts) != 2 {
					t.Errorf("resolveMountPath(%q) = %q, want host:container format", tt.mount, got)
				}
			}
		})
	}
}
