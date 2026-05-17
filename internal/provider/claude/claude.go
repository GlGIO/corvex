package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

// mcpConfigRelPath is where Corvex materialises the MCP config for the Worker
// before each Claude CLI invocation. Path is relative to the project root so
// it resolves identically for local and Docker (the project root is bind-
// mounted at the container workdir).
const mcpConfigRelPath = ".corvex/mcp.json"

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

func (c *ClaudeCLI) Name() string   { return "claude-cli" }
func (c *ClaudeCLI) Models() []string { return supportedModels }

func (c *ClaudeCLI) Execute(ctx context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
	return c.ExecuteWithProgress(ctx, req, nil)
}

// ExecuteWithProgress is identical to Execute but invokes onEvent for every
// stream event (tool calls, text, errors) as it arrives. The final
// ExecuteResult still carries cost and token totals — callers get
// observability and accounting in one pass.
func (c *ClaudeCLI) ExecuteWithProgress(ctx context.Context, req types.ExecuteRequest, onEvent func(types.StreamEvent)) (*types.ExecuteResult, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}

	args := buildArgs(req)
	cmd := c.cmdRunner(ctx, c.binaryCmd, args...)
	configureCmd(cmd, req)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	start := time.Now()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude cli stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude cli start: %w", err)
	}

	result := &types.ExecuteResult{}
	var outputParts []string

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		events, parseErr := parseNDJSONLine(line)
		if parseErr != nil {
			continue
		}

		for _, ev := range events {
			if onEvent != nil {
				onEvent(ev)
			}
			switch ev.Type {
			case types.EventText:
				outputParts = append(outputParts, ev.Content)
			case types.EventDone:
				// final content already captured
			}
		}

		var raw rawLine
		if json.Unmarshal(line, &raw) == nil && raw.Type == "result" {
			var res resultLine
			if json.Unmarshal(line, &res) == nil {
				result.TokensIn = res.TotalInputTokens
				result.TokensOut = res.TotalOutputTokens
				result.CostUSD = res.TotalCostUSD
				if res.DurationMs > 0 {
					result.DurationMs = res.DurationMs
				}
			}
		}
	}

	waitErr := cmd.Wait()
	elapsed := time.Since(start)
	if result.DurationMs == 0 {
		result.DurationMs = elapsed.Milliseconds()
	}

	result.Output = strings.Join(outputParts, "")

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		return result, fmt.Errorf("claude cli exited with error: %w (stderr: %s)", waitErr, strings.TrimSpace(stderr.String()))
	}

	return result, nil
}

func (c *ClaudeCLI) Stream(ctx context.Context, req types.ExecuteRequest) (<-chan types.StreamEvent, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}

	args := buildArgs(req)
	cmd := c.cmdRunner(ctx, c.binaryCmd, args...)
	configureCmd(cmd, req)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude cli stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude cli start: %w", err)
	}

	ch := make(chan types.StreamEvent, 32)

	go func() {
		defer close(ch)

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			events, parseErr := parseNDJSONLine(line)
			if parseErr != nil {
				select {
				case ch <- types.StreamEvent{Type: types.EventError, Content: parseErr.Error()}:
				case <-ctx.Done():
					return
				}
				continue
			}

			for _, ev := range events {
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		}

		if err := cmd.Wait(); err != nil {
			select {
			case ch <- types.StreamEvent{Type: types.EventError, Content: err.Error()}:
			case <-ctx.Done():
			}
		}
	}()

	return ch, nil
}

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

type rawLine struct {
	Type string `json:"type"`
}

type messageContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type messageBody struct {
	Role    string           `json:"role"`
	Content []messageContent `json:"content"`
}

type assistantLine struct {
	Type    string      `json:"type"`
	Message messageBody `json:"message"`
}

type toolResultLine struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

type resultLine struct {
	Type             string  `json:"type"`
	Subtype          string  `json:"subtype"`
	Result           string  `json:"result"`
	TotalCostUSD     float64 `json:"total_cost_usd"`
	TotalInputTokens int     `json:"total_input_tokens"`
	TotalOutputTokens int    `json:"total_output_tokens"`
	DurationMs       int64   `json:"duration_ms"`
}

type toolInput struct {
	FilePath string `json:"file_path,omitempty"`
}

func parseNDJSONLine(line []byte) ([]types.StreamEvent, error) {
	var raw rawLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, fmt.Errorf("invalid json line: %w", err)
	}

	switch raw.Type {
	case "assistant":
		return parseAssistant(line)
	case "tool_result":
		return parseToolResult(line)
	case "result":
		return parseResult(line)
	case "system":
		return nil, nil
	default:
		return nil, nil
	}
}

func parseAssistant(line []byte) ([]types.StreamEvent, error) {
	var al assistantLine
	if err := json.Unmarshal(line, &al); err != nil {
		return nil, fmt.Errorf("parsing assistant line: %w", err)
	}

	var events []types.StreamEvent
	for _, c := range al.Message.Content {
		switch c.Type {
		case "text":
			events = append(events, types.StreamEvent{
				Type:    types.EventText,
				Content: c.Text,
			})
		case "tool_use":
			ev := types.StreamEvent{
				Type: types.EventToolUse,
				Tool: c.Name,
			}
			var ti toolInput
			if json.Unmarshal(c.Input, &ti) == nil && ti.FilePath != "" {
				ev.File = ti.FilePath
			}
			events = append(events, ev)
		}
	}

	return events, nil
}

func parseToolResult(line []byte) ([]types.StreamEvent, error) {
	var tr toolResultLine
	if err := json.Unmarshal(line, &tr); err != nil {
		return nil, fmt.Errorf("parsing tool_result line: %w", err)
	}

	return []types.StreamEvent{{
		Type:    types.EventToolResult,
		Content: tr.Content,
	}}, nil
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
	if err := os.WriteFile(mcpConfigRelPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", mcpConfigRelPath, err)
	}
	return nil
}

// ParseFullOutput implements provider.CommandBuilder.
func (c *ClaudeCLI) ParseFullOutput(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error) {
	result := &types.ExecuteResult{
		ExitCode:   exitCode,
		DurationMs: elapsed.Milliseconds(),
	}
	var outputParts []string

	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		events, err := parseNDJSONLine([]byte(trimmed))
		if err != nil {
			continue
		}
		for _, ev := range events {
			if ev.Type == types.EventText {
				outputParts = append(outputParts, ev.Content)
			}
		}
		var raw rawLine
		if json.Unmarshal([]byte(trimmed), &raw) == nil && raw.Type == "result" {
			var res resultLine
			if json.Unmarshal([]byte(trimmed), &res) == nil {
				result.TokensIn = res.TotalInputTokens
				result.TokensOut = res.TotalOutputTokens
				result.CostUSD = res.TotalCostUSD
				if res.DurationMs > 0 {
					result.DurationMs = res.DurationMs
				}
			}
		}
	}

	result.Output = strings.Join(outputParts, "")
	return result, nil
}

func parseResult(line []byte) ([]types.StreamEvent, error) {
	var rl resultLine
	if err := json.Unmarshal(line, &rl); err != nil {
		return nil, fmt.Errorf("parsing result line: %w", err)
	}

	return []types.StreamEvent{{
		Type:    types.EventDone,
		Content: rl.Result,
	}}, nil
}
