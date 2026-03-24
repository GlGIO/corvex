package provider

import (
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

// CommandBuilder is an optional interface that providers may implement
// to support execution via a sandbox. When implemented, the orchestrator
// extracts the shell command and runs it through a sandbox instead of
// letting the provider spawn the process directly.
type CommandBuilder interface {
	BuildCommand(req types.ExecuteRequest) (bin string, args []string, env map[string]string)
	ParseFullOutput(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error)
}
