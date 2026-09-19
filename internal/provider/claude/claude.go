// Package claude drives the Claude CLI as a Corvex provider. This file holds
// the client itself and its static metadata; the command it runs lives in
// command.go, the process lifecycle in execute.go, and the stream-json wire
// format in protocol.go.
package claude

import (
	"context"
	"os"
	"os/exec"

	"github.com/giovannialves/corvex/internal/config"
)

// mcpConfigRelPath is where Corvex materialises the MCP config for the Worker
// before each Claude CLI invocation. Path is relative to the project root so
// it resolves identically for local and Docker (the project root is bind-
// mounted at the container workdir).
const mcpConfigRelPath = ".corvex/mcp.json"

// mcpNoneRelPath is the empty server set handed to every call that must NOT
// reach an MCP server — the planner and the reviewer. It is a FILE and not the
// absence of a flag, because the absence of `--mcp-config` does not mean "no
// servers": the Claude CLI then loads whatever the person's own configuration
// declares for that directory, which on a developer machine is every production
// database they have ever connected to. An empty file plus
// `--strict-mcp-config` says zero and means it.
const mcpNoneRelPath = ".corvex/mcp-none.json"

var supportedModels = []string{
	"opus",
	"sonnet",
	"haiku",
	"claude-sonnet-4-20250514",
	"claude-opus-4-20250514",
}

type ClaudeCLI struct {
	cfg       *config.Config
	binaryCmd string
	cmdRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func New(cfg *config.Config) *ClaudeCLI {
	bin := os.Getenv("CORVEX_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	return &ClaudeCLI{
		cfg:       cfg,
		binaryCmd: bin,
		cmdRunner: exec.CommandContext,
	}
}

func (c *ClaudeCLI) Name() string     { return "claude-cli" }
func (c *ClaudeCLI) Models() []string { return supportedModels }
