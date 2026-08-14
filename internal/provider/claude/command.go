package claude

// Command construction for the Claude CLI: request validation, the argv the
// child process is started with, the environment it inherits, and the MCP
// config materialised alongside it.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

func validateRequest(req types.ExecuteRequest) error {
	if req.Model == "" {
		return fmt.Errorf("claude cli: model is required")
	}
	if req.Prompt == "" {
		return fmt.Errorf("claude cli: prompt is required")
	}
	return nil
}

func buildArgs(req types.ExecuteRequest) []string {
	args := []string{
		"-p", req.Prompt,
		"--model", req.Model,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
	}

	for _, tool := range req.AllowedTools {
		args = append(args, "--allowedTools", tool)
	}

	for _, tool := range req.DisallowedTools {
		args = append(args, "--disallowedTools", tool)
	}

	return args
}

func configureCmd(cmd *exec.Cmd, req types.ExecuteRequest) {
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	if len(req.Env) > 0 {
		cmd.Env = mergeEnv(os.Environ(), req.Env)
	}
}

func mergeEnv(base []string, extra map[string]string) []string {
	env := make([]string, len(base), len(base)+len(extra))
	copy(env, base)
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// BuildCommand implements provider.CommandBuilder.
func (c *ClaudeCLI) BuildCommand(req types.ExecuteRequest) (string, []string, map[string]string) {
	args := buildArgs(req)

	if len(c.cfg.Sandbox.MCPServers) > 0 {
		if err := writeMCPConfig(c.cfg.Sandbox.MCPServers); err != nil {
			log.Warn("failed to write MCP config, continuing without MCP servers", "err", err)
		} else {
			args = append(args, "--mcp-config", mcpConfigRelPath)
		}
	}

	args = append(args, c.cfg.Sandbox.WorkerExtraArgs...)

	env := make(map[string]string)
	for k, v := range req.Env {
		env[k] = v
	}
	return c.binaryCmd, args, env
}

// writeMCPConfig materialises the Claude CLI `--mcp-config` JSON next to the
// project root so it is reachable from both local execution and the Docker
// sandbox (the project root is bind-mounted at the container workdir).
func writeMCPConfig(servers []config.MCPServerConfig) error {
	type serverEntry struct {
		Command string            `json:"command"`
		Args    []string          `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
	}
	payload := struct {
		MCPServers map[string]serverEntry `json:"mcpServers"`
	}{MCPServers: make(map[string]serverEntry, len(servers))}

	for _, s := range servers {
		if s.Name == "" || s.Command == "" {
			return fmt.Errorf("invalid MCP server: name and command are required (got name=%q command=%q)", s.Name, s.Command)
		}
		expandedArgs := make([]string, len(s.Args))
		for i, a := range s.Args {
			expandedArgs[i] = os.ExpandEnv(a)
		}
		expandedEnv := make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			expandedEnv[k] = os.ExpandEnv(v)
		}
		payload.MCPServers[s.Name] = serverEntry{
			Command: os.ExpandEnv(s.Command),
			Args:    expandedArgs,
			Env:     expandedEnv,
		}
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mcp config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(mcpConfigRelPath), 0o755); err != nil {
		return fmt.Errorf("create %s dir: %w", filepath.Dir(mcpConfigRelPath), err)
	}
	// 0o600: the materialised MCP config carries env-expanded values that may
	// include secrets (DB URLs, tokens). It must be readable only by the owner,
	// never world-readable. Chmod explicitly in case the file pre-existed with
	// looser perms (WriteFile does not tighten an existing file's mode).
	if err := os.WriteFile(mcpConfigRelPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", mcpConfigRelPath, err)
	}
	if err := os.Chmod(mcpConfigRelPath, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", mcpConfigRelPath, err)
	}
	return nil
}
