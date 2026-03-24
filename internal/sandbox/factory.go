package sandbox

import (
	"context"

	"github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/config"
)

// NewSandbox creates a Sandbox based on the configuration type.
// For "docker", it checks availability and falls back to local if Docker
// is not reachable. Unknown types also fall back to local with a warning.
func NewSandbox(cfg config.SandboxConfig) Sandbox {
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
