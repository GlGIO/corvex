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
	"strings"

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

	if req.JSONSchema != "" {
		args = append(args, "--json-schema", req.JSONSchema)
	}

	return args
}

func configureCmd(cmd *exec.Cmd, req types.ExecuteRequest) {
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	if len(req.Env) > 0 {
		cmd.Env = mergeEnv(os.Environ(), req.Env, req.DenyEnv)
	}
}

// mergeEnv builds the child's environment: this process's, minus the names
// custody holds back (F9), plus what the caller adds. Deny wins over both.
func mergeEnv(base []string, extra map[string]string, deny []string) []string {
	held := make(map[string]struct{}, len(deny))
	for _, name := range deny {
		if name = strings.TrimSpace(name); name != "" {
			held[name] = struct{}{}
		}
	}
	env := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		if name, _, ok := strings.Cut(kv, "="); ok {
			if _, blocked := held[name]; blocked {
				continue
			}
		}
		env = append(env, kv)
	}
	for k, v := range extra {
		if _, blocked := held[k]; blocked {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}

// argsFor is the ONE assembly of the CLI invocation: the request's own flags,
// the MCP config when the repository declares servers, and whatever extra args
// the sandbox config adds.
//
// It exists because there were two assemblies and only one of them was complete.
// `BuildCommand` (the sandboxed path) added `--mcp-config` and the extra args;
// `ExecuteWithProgress` — which is the path a LOCAL sandbox actually takes, and
// local is the default — called buildArgs and stopped there. So a repository
// that declared an MCP server got it materialised and passed in Docker and got
// NOTHING locally: the preflight said the dependency was satisfied (it is
// declared), the agent had no such tool, and the failure looked like a model
// that would not use the database it was told to use.
//
// MEASURED before the fix, with a stub recording the argv: zero occurrences of
// `--mcp-config` on a local run with one server declared, and no `mcp.json`
// written anywhere.
func (c *ClaudeCLI) argsFor(req types.ExecuteRequest) []string {
	args := buildArgs(req)
	if c.cfg == nil {
		return args
	}
	// `req.AllowMCP` is the enforcement point of a rule that had none: only the
	// worker gets the declared servers. Fixing the missing `--mcp-config` on
	// the local path without this would have handed the REVIEWER a production
	// database connection it never needed — measured on the first run after
	// that fix: two invocations carried the flag, and the second was the judge.
	//
	// `--strict-mcp-config`, and a config passed on EVERY call, is the other
	// half of that rule — the half that was missing, and the half that made the
	// first half nearly decorative.
	//
	// MEASURED on the first real incident run: the worker called
	// `mcp__nb2bPRD__query` 28 times and `mcp__SmartCarePRD__query` 4 times,
	// and corvex had declared exactly one server — SmartCarePRD. `--mcp-config`
	// ADDS to whatever the Claude CLI already loads for that directory, and
	// what it loads is the person's own `~/.claude.json`, which on a developer
	// machine holds every production database they have ever connected to.
	//
	// So the custody story had a hole in the shape of the whole thing: the
	// worker reached databases nobody declared, and the planner and the
	// reviewer — which are given no `--mcp-config` precisely so they cannot
	// reach production — were reaching it through the same door. Withholding a
	// flag is not a restriction when the default is "load everything".
	//
	// The fix is one rule with no inference in it: every call gets an explicit
	// config and `--strict-mcp-config`. The worker's config holds the declared
	// servers; everyone else's holds none. "Only use the servers in this file"
	// with an empty file is zero servers, stated rather than hoped for.
	// Two files, never one rewritten per call. The worker and the reviewer of a
	// fan-out run at the same time; a single path rewritten by whoever started
	// last would hand the worker an empty config, and it would do so silently —
	// the agent would simply not have the tool it was told to use, which is the
	// exact failure this whole area already produced once.
	path, servers := mcpNoneRelPath, []config.MCPServerConfig(nil)
	if req.AllowMCP {
		path, servers = mcpConfigRelPath, c.cfg.Sandbox.MCPServers
	}
	if err := writeMCPConfig(path, servers); err != nil {
		// Fail CLOSED. The old line here said "continuing without MCP servers"
		// and dropped both flags — which, now that the flags are what holds the
		// agent to a declared set, means continuing with EVERY server on the
		// machine. A guard whose failure path is the thing it guards against is
		// not a guard. `--strict-mcp-config` still goes on: with no config to
		// read from, the widest it can be is nothing.
		log.Warn("could not write the MCP config: holding this call to no MCP servers at all", "path", path, "err", err)
		return append(append(args, "--strict-mcp-config"), c.cfg.Sandbox.WorkerExtraArgs...)
	}
	args = append(args, "--mcp-config", path, "--strict-mcp-config")
	return append(args, c.cfg.Sandbox.WorkerExtraArgs...)
}

// BuildCommand implements provider.CommandBuilder.
func (c *ClaudeCLI) BuildCommand(req types.ExecuteRequest) (string, []string, map[string]string) {
	args := c.argsFor(req)

	env := make(map[string]string)
	for k, v := range req.Env {
		env[k] = v
	}
	return c.binaryCmd, args, env
}

// writeMCPConfig materialises the Claude CLI `--mcp-config` JSON next to the
// project root so it is reachable from both local execution and the Docker
// sandbox (the project root is bind-mounted at the container workdir).
func writeMCPConfig(path string, servers []config.MCPServerConfig) error {
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

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s dir: %w", filepath.Dir(path), err)
	}
	// 0o600: the materialised MCP config carries env-expanded values that may
	// include secrets (DB URLs, tokens). It must be readable only by the owner,
	// never world-readable. Chmod explicitly in case the file pre-existed with
	// looser perms (WriteFile does not tighten an existing file's mode).
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}
