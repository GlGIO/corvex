package sandbox

import (
	"testing"

	"github.com/giovannialves/corvex/internal/config"
)

func TestNewSandbox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfg      config.SandboxConfig
		wantType string
	}{
		{
			name:     "local type",
			cfg:      config.SandboxConfig{Type: "local"},
			wantType: "*sandbox.LocalSandbox",
		},
		{
			name:     "empty type defaults to local",
			cfg:      config.SandboxConfig{Type: ""},
			wantType: "*sandbox.LocalSandbox",
		},
		{
			name:     "unknown type falls back to local",
			cfg:      config.SandboxConfig{Type: "k8s"},
			wantType: "*sandbox.LocalSandbox",
		},
		{
			name: "docker type returns DockerSandbox or LocalSandbox",
			cfg: config.SandboxConfig{
				Type:  "docker",
				Image: "alpine:latest",
			},
			wantType: "*sandbox.DockerSandbox|*sandbox.LocalSandbox",
		},
		{
			name:     "nix profile returns NixSandbox or LocalSandbox",
			cfg:      config.SandboxConfig{Profile: "nix"},
			wantType: "*sandbox.NixSandbox|*sandbox.LocalSandbox",
		},
		{
			name:     "unknown profile falls back to type",
			cfg:      config.SandboxConfig{Profile: "bsd-jail", Type: "local"},
			wantType: "*sandbox.LocalSandbox",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sb := NewSandbox(tt.cfg)
			if sb == nil {
				t.Fatal("NewSandbox() returned nil")
			}

			switch tt.wantType {
			case "*sandbox.LocalSandbox":
				if _, ok := sb.(*LocalSandbox); !ok {
					t.Errorf("NewSandbox() type = %T, want *LocalSandbox", sb)
				}
			case "*sandbox.DockerSandbox":
				if _, ok := sb.(*DockerSandbox); !ok {
					t.Errorf("NewSandbox() type = %T, want *DockerSandbox", sb)
				}
			case "*sandbox.DockerSandbox|*sandbox.LocalSandbox":
				_, isLocal := sb.(*LocalSandbox)
				_, isDocker := sb.(*DockerSandbox)
				if !isLocal && !isDocker {
					t.Errorf("NewSandbox() type = %T, want *LocalSandbox or *DockerSandbox", sb)
				}
			case "*sandbox.NixSandbox|*sandbox.LocalSandbox":
				_, isLocal := sb.(*LocalSandbox)
				_, isNix := sb.(*NixSandbox)
				if !isLocal && !isNix {
					t.Errorf("NewSandbox() type = %T, want *LocalSandbox or *NixSandbox", sb)
				}
			}
		})
	}
}
