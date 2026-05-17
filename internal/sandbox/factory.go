package sandbox

import (
	"context"

	"github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/config"
)

// NewSandbox creates a Sandbox based on the configuration.
//
// Selection order:
//  1. Profile (if set) takes precedence over Type: "nix" → NixSandbox.
//     If the profile's runtime is unavailable, falls back to local.
//  2. Type: "docker" → DockerSandbox (falls back to local when unavailable),
//     "local" or "" → LocalSandbox.
//  3. Unknown values fall back to local with a warning.
func NewSandbox(cfg config.SandboxConfig) Sandbox {
	switch cfg.Profile {
	case "nix":
		ns := NewNixSandbox(cfg)
		if ns.IsAvailable(context.Background()) {
			return ns
		}
		log.Warn("nix not available, falling back to local sandbox")
		return NewLocalSandbox(cfg)
	case "":
		// no profile — fall through to Type
	default:
		log.Warn("unknown sandbox profile, falling back to type-based selection", "profile", cfg.Profile)
	}

	switch cfg.Type {
	case "docker":
		ds := NewDockerSandbox(cfg)
		if ds.IsAvailable(context.Background()) {
			return ds
		}
		log.Warn("docker not available, falling back to local sandbox")
		return NewLocalSandbox(cfg)

	case "local", "":
		return NewLocalSandbox(cfg)

	default:
		log.Warn("unknown sandbox type, falling back to local", "type", cfg.Type)
		return NewLocalSandbox(cfg)
	}
}
