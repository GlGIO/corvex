package claude

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name     string
		req      types.ExecuteRequest
		expected []string
	}{
		{
			name: "basic request",
			req: types.ExecuteRequest{
				Prompt: "hello",
				Model:  "sonnet",
			},
			expected: []string{
				"-p", "hello",
				"--model", "sonnet",
				"--output-format", "stream-json",
				"--verbose",
				"--permission-mode", "bypassPermissions",
			},
		},
		{
			name: "with allowed tools",
			req: types.ExecuteRequest{
				Prompt:       "read the code",
				Model:        "sonnet",
				AllowedTools: []string{"Read", "Glob", "Grep"},
			},
			expected: []string{
				"-p", "read the code",
				"--model", "sonnet",
				"--output-format", "stream-json",
				"--verbose",
				"--permission-mode", "bypassPermissions",
				"--allowedTools", "Read",
				"--allowedTools", "Glob",
				"--allowedTools", "Grep",
			},
		},
		{
			name: "with disallowed tools",
			req: types.ExecuteRequest{
				Prompt:          "do work",
				Model:           "sonnet",
				DisallowedTools: []string{"Write", "Bash"},
			},
			expected: []string{
				"-p", "do work",
				"--model", "sonnet",
				"--output-format", "stream-json",
				"--verbose",
				"--permission-mode", "bypassPermissions",
				"--disallowedTools", "Write",
				"--disallowedTools", "Bash",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildArgs(tt.req)
			if len(got) != len(tt.expected) {
				t.Fatalf("args length: got %d, want %d\ngot:  %v\nwant: %v", len(got), len(tt.expected), got, tt.expected)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("arg[%d]: got %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestValidateRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     types.ExecuteRequest
		wantErr string
	}{
		{
			name:    "missing model",
			req:     types.ExecuteRequest{Prompt: "hello"},
			wantErr: "model is required",
		},
		{
			name:    "missing prompt",
			req:     types.ExecuteRequest{Model: "sonnet"},
			wantErr: "prompt is required",
		},
		{
			name: "valid request",
			req:  types.ExecuteRequest{Prompt: "hello", Model: "sonnet"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequest(tt.req)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseNDJSONLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantEvents []types.StreamEvent
		wantErr    bool
	}{
		{
			name:       "system line ignored",
			line:       `{"type":"system","subtype":"init","session_id":"abc"}`,
			wantEvents: nil,
		},
		{
			name: "assistant text",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello world"}]}}`,
			wantEvents: []types.StreamEvent{
				{Type: types.EventText, Content: "Hello world"},
			},
		},
		{
			name: "assistant tool_use",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"main.go"}}]}}`,
			wantEvents: []types.StreamEvent{
				{Type: types.EventToolUse, Tool: "Read", File: "main.go", Content: "main.go"},
			},
		},
		{
			name: "tool_result",
			line: `{"type":"tool_result","tool_use_id":"t1","content":"file contents here"}`,
			wantEvents: []types.StreamEvent{
				{Type: types.EventToolResult, Content: "file contents here"},
			},
		},
		{
			name: "result done",
			line: `{"type":"result","subtype":"success","result":"All done","total_cost_usd":0.01,"total_input_tokens":500,"total_output_tokens":200,"duration_ms":3000}`,
			wantEvents: []types.StreamEvent{
				{Type: types.EventDone, Content: "All done"},
			},
		},
		{
			name: "assistant mixed content",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me read"},{"type":"tool_use","id":"t2","name":"Bash","input":{}}]}}`,
			wantEvents: []types.StreamEvent{
				{Type: types.EventText, Content: "Let me read"},
				{Type: types.EventToolUse, Tool: "Bash"},
			},
		},
		{
			name:    "invalid json",
			line:    `not json`,
			wantErr: true,
		},
		{
			name:       "unknown type ignored",
			line:       `{"type":"unknown_new_type","data":"something"}`,
			wantEvents: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := parseNDJSONLine([]byte(tt.line))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(events) != len(tt.wantEvents) {
				t.Fatalf("events count: got %d, want %d\ngot:  %+v\nwant: %+v", len(events), len(tt.wantEvents), events, tt.wantEvents)
			}
			for i, want := range tt.wantEvents {
				got := events[i]
				if got.Type != want.Type {
					t.Errorf("event[%d].Type: got %q, want %q", i, got.Type, want.Type)
				}
				if got.Content != want.Content {
					t.Errorf("event[%d].Content: got %q, want %q", i, got.Content, want.Content)
				}
				if got.Tool != want.Tool {
					t.Errorf("event[%d].Tool: got %q, want %q", i, got.Tool, want.Tool)
				}
				if got.File != want.File {
					t.Errorf("event[%d].File: got %q, want %q", i, got.File, want.File)
				}
			}
		})
	}
}

func TestParseFixtureFile(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "testdata", "provider", "claude-stream-output.jsonl")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 8 {
		t.Fatalf("expected 8 fixture lines, got %d", len(lines))
	}

	var allEvents []types.StreamEvent
	for i, line := range lines {
		events, err := parseNDJSONLine([]byte(line))
		if err != nil {
			t.Fatalf("line %d parse error: %v", i, err)
		}
		allEvents = append(allEvents, events...)
	}

	expectedTypes := []types.StreamEventType{
		types.EventText,       // "I'll analyze..."
		types.EventToolUse,    // Read main.go
		types.EventToolResult, // file contents
		types.EventToolUse,    // Write main.go
		types.EventToolResult, // "File written successfully"
		types.EventText,       // "Done! I've updated..."
		types.EventDone,       // result
	}

	if len(allEvents) != len(expectedTypes) {
		t.Fatalf("total events: got %d, want %d\nevents: %+v", len(allEvents), len(expectedTypes), allEvents)
	}

	for i, wantType := range expectedTypes {
		if allEvents[i].Type != wantType {
			t.Errorf("event[%d].Type: got %q, want %q", i, allEvents[i].Type, wantType)
		}
	}

	if allEvents[1].Tool != "Read" {
		t.Errorf("expected Read tool, got %q", allEvents[1].Tool)
	}
	if allEvents[1].File != "main.go" {
		t.Errorf("expected main.go file, got %q", allEvents[1].File)
	}
	if allEvents[3].Tool != "Write" {
		t.Errorf("expected Write tool, got %q", allEvents[3].Tool)
	}
}

func TestResultLineMetrics(t *testing.T) {
	line := `{"type":"result","subtype":"success","result":"Done","total_cost_usd":0.0042,"total_input_tokens":1250,"total_output_tokens":380,"duration_ms":4500}`

	var rl resultLine
	if err := json.Unmarshal([]byte(line), &rl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if rl.TotalInputTokens != 1250 {
		t.Errorf("input tokens: got %d, want 1250", rl.TotalInputTokens)
	}
	if rl.TotalOutputTokens != 380 {
		t.Errorf("output tokens: got %d, want 380", rl.TotalOutputTokens)
	}
	if rl.TotalCostUSD != 0.0042 {
		t.Errorf("cost: got %f, want 0.0042", rl.TotalCostUSD)
	}
	if rl.DurationMs != 4500 {
		t.Errorf("duration: got %d, want 4500", rl.DurationMs)
	}
}

func TestMergeEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root"}
	extra := map[string]string{
		"CORVEX_TASK": "S01",
		"DEBUG":       "true",
	}

	result := mergeEnv(base, extra)

	if len(result) != 4 {
		t.Fatalf("expected 4 env vars, got %d", len(result))
	}
	if result[0] != "PATH=/usr/bin" || result[1] != "HOME=/root" {
		t.Error("base env vars not preserved")
	}

	extraFound := map[string]bool{}
	for _, v := range result[2:] {
		extraFound[v] = true
	}
	if !extraFound["CORVEX_TASK=S01"] || !extraFound["DEBUG=true"] {
		t.Errorf("extra env vars not found in result: %v", result)
	}
}

func TestNewCLI(t *testing.T) {
	cfg := config.Default()
	cli := New(cfg)

	if cli.Name() != "claude-cli" {
		t.Errorf("name: got %q, want %q", cli.Name(), "claude-cli")
	}

	models := cli.Models()
	if len(models) == 0 {
		t.Fatal("expected non-empty models list")
	}

	found := false
	for _, m := range models {
		if m == "sonnet" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'sonnet' in models: %v", models)
	}
}

func TestExecuteValidation(t *testing.T) {
	cfg := config.Default()
	cli := New(cfg)

	_, err := cli.Execute(context.Background(), types.ExecuteRequest{})
	if err == nil {
		t.Fatal("expected validation error for empty request")
	}
}

func TestStreamValidation(t *testing.T) {
	cfg := config.Default()
	cli := New(cfg)

	_, err := cli.Stream(context.Background(), types.ExecuteRequest{})
	if err == nil {
		t.Fatal("expected validation error for empty request")
	}
}

func TestStreamWithFakeScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script not available on Windows")
	}

	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "provider", "claude-stream-output.jsonl"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fake-claude")
	script := "#!/bin/sh\ncat " + fixturePath + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfg := config.Default()
	cli := New(cfg)
	cli.binaryCmd = scriptPath

	ctx := context.Background()
	ch, err := cli.Stream(ctx, types.ExecuteRequest{
		Prompt: "test",
		Model:  "sonnet",
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var events []types.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	if len(events) == 0 {
		t.Fatal("expected events from stream")
	}

	var hasText, hasToolUse, hasDone bool
	for _, ev := range events {
		switch ev.Type {
		case types.EventText:
			hasText = true
		case types.EventToolUse:
			hasToolUse = true
		case types.EventDone:
			hasDone = true
		}
	}

	if !hasText {
		t.Error("no text events received")
	}
	if !hasToolUse {
		t.Error("no tool_use events received")
	}
	if !hasDone {
		t.Error("no done event received")
	}
}

func TestExecuteWithFakeScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script not available on Windows")
	}

	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "provider", "claude-stream-output.jsonl"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fake-claude")
	script := "#!/bin/sh\ncat " + fixturePath + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfg := config.Default()
	cli := New(cfg)
	cli.binaryCmd = scriptPath

	result, err := cli.Execute(context.Background(), types.ExecuteRequest{
		Prompt: "test",
		Model:  "sonnet",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if result.Output == "" {
		t.Error("expected non-empty output")
	}
	if result.TokensIn != 1250 {
		t.Errorf("input tokens: got %d, want 1250", result.TokensIn)
	}
	if result.TokensOut != 380 {
		t.Errorf("output tokens: got %d, want 380", result.TokensOut)
	}
	if result.CostUSD != 0.0042 {
		t.Errorf("cost: got %f, want 0.0042", result.CostUSD)
	}
	if result.DurationMs != 4500 {
		t.Errorf("duration: got %d, want 4500", result.DurationMs)
	}
}

func TestExecuteNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script not available on Windows")
	}

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fail-claude")
	script := "#!/bin/sh\necho 'error occurred' >&2\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfg := config.Default()
	cli := New(cfg)
	cli.binaryCmd = scriptPath

	result, err := cli.Execute(context.Background(), types.ExecuteRequest{
		Prompt: "test",
		Model:  "sonnet",
	})
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "error occurred") {
		t.Errorf("expected stderr in error, got: %v", err)
	}
	if result.ExitCode != 1 {
		t.Errorf("exit code: got %d, want 1", result.ExitCode)
	}
}

func TestContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script not available on Windows")
	}

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "slow-claude")
	script := "#!/bin/sh\nsleep 60\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfg := config.Default()
	cli := New(cfg)
	cli.binaryCmd = scriptPath

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := cli.Stream(ctx, types.ExecuteRequest{
		Prompt: "test",
		Model:  "sonnet",
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	cancel()

	eventCount := 0
	for range ch {
		eventCount++
	}
	// Channel should close quickly after cancel, with at most an error event
	if eventCount > 1 {
		t.Errorf("expected at most 1 event after cancel, got %d", eventCount)
	}
}

func TestToolInputSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   toolInput
		want string
	}{
		{"empty", toolInput{}, ""},
		{"file path wins", toolInput{FilePath: "a.go", Pattern: "x"}, "a.go"},
		{"bash command", toolInput{Command: "npm test"}, "$ npm test"},
		{"glob pattern", toolInput{Pattern: "src/**/*.ts"}, "src/**/*.ts"},
		{"grep with path", toolInput{Pattern: "qrcode", Path: "app/"}, "qrcode in app/"},
		{"path only", toolInput{Path: "internal/"}, "internal/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.in.summary(); got != tt.want {
				t.Errorf("summary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseAssistant_ToolUseContentRendersRichSummary(t *testing.T) {
	t.Parallel()

	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[
		{"type":"tool_use","name":"Glob","input":{"pattern":"app/**/*.vue"}},
		{"type":"tool_use","name":"Bash","input":{"command":"npm test"}},
		{"type":"tool_use","name":"Grep","input":{"pattern":"qrcode","path":"app/"}},
		{"type":"tool_use","name":"Read","input":{"file_path":"package.json"}}
	]}}`)

	events, err := parseAssistant(line)
	if err != nil {
		t.Fatalf("parseAssistant error = %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4", len(events))
	}

	want := []struct {
		tool    string
		file    string
		content string
	}{
		{"Glob", "", "app/**/*.vue"},
		{"Bash", "", "$ npm test"},
		{"Grep", "", "qrcode in app/"},
		{"Read", "package.json", "package.json"},
	}
	for i, w := range want {
		ev := events[i]
		if ev.Tool != w.tool {
			t.Errorf("event[%d].Tool = %q, want %q", i, ev.Tool, w.tool)
		}
		if ev.File != w.file {
			t.Errorf("event[%d].File = %q, want %q", i, ev.File, w.file)
		}
		if ev.Content != w.content {
			t.Errorf("event[%d].Content = %q, want %q", i, ev.Content, w.content)
		}
	}
}

func TestExecuteWithProgress_InvokesCallback(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cli := New(cfg)

	// Fake a Claude CLI run that emits an assistant message with one tool_use
	// followed by a result line. The real binary is replaced with `printf`
	// streaming the canned NDJSON.
	canned := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"package.json"}}]}}
{"type":"result","subtype":"success","is_error":false,"result":"hi","total_input_tokens":10,"total_output_tokens":2,"total_cost_usd":0.01,"duration_ms":42}`

	cli.cmdRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "printf", "%s\n", canned)
	}

	var captured []types.StreamEvent
	result, err := cli.ExecuteWithProgress(context.Background(),
		types.ExecuteRequest{Prompt: "x", Model: "sonnet"},
		func(ev types.StreamEvent) { captured = append(captured, ev) },
	)
	if err != nil {
		t.Fatalf("ExecuteWithProgress error = %v", err)
	}

	if len(captured) == 0 {
		t.Fatal("expected at least one event to reach the callback")
	}
	var sawToolUse bool
	for _, ev := range captured {
		if ev.Type == types.EventToolUse && ev.Tool == "Read" && ev.File == "package.json" {
			sawToolUse = true
		}
	}
	if !sawToolUse {
		t.Errorf("expected EventToolUse Read package.json in callback events; got %+v", captured)
	}

	// Cost / tokens still come through.
	if result.CostUSD != 0.01 || result.TokensIn != 10 || result.TokensOut != 2 {
		t.Errorf("usage tracking lost: %+v", result)
	}
}

func TestBuildCommand_BasicArgs(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cli := New(cfg)

	req := types.ExecuteRequest{
		Prompt: "do work",
		Model:  "sonnet",
	}

	bin, args, env := cli.BuildCommand(req)

	if bin != cli.binaryCmd {
		t.Errorf("bin = %q, want %q", bin, cli.binaryCmd)
	}

	want := []string{"-p", "do work", "--model", "sonnet", "--output-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions"}
	if len(args) != len(want) {
		t.Fatalf("args length: got %d, want %d\ngot:  %v\nwant: %v", len(args), len(want), args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}

	if len(env) != 0 {
		t.Errorf("env = %v, want empty map", env)
	}
}

func TestBuildCommand_WithExtraArgs(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Sandbox.WorkerExtraArgs = []string{"--dangerously-skip-permissions"}
	cli := New(cfg)

	req := types.ExecuteRequest{
		Prompt: "do work",
		Model:  "sonnet",
	}

	_, args, _ := cli.BuildCommand(req)

	baseLen := 9 // -p, prompt, --model, sonnet, --output-format, stream-json, --verbose, --permission-mode, bypassPermissions
	if len(args) != baseLen+1 {
		t.Fatalf("args length: got %d, want %d\nargs: %v", len(args), baseLen+1, args)
	}
	if args[len(args)-1] != "--dangerously-skip-permissions" {
		t.Errorf("last arg = %q, want %q", args[len(args)-1], "--dangerously-skip-permissions")
	}
}

func TestBuildCommand_WithAllowedTools(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cli := New(cfg)

	req := types.ExecuteRequest{
		Prompt:       "do work",
		Model:        "sonnet",
		AllowedTools: []string{"Read", "Write"},
	}

	_, args, _ := cli.BuildCommand(req)

	want := []string{
		"-p", "do work",
		"--model", "sonnet",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", "Read",
		"--allowedTools", "Write",
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestBuildCommand_WithMCPServers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := config.Default()
	cfg.Sandbox.MCPServers = []config.MCPServerConfig{
		{
			Name:    "postgres",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-postgres", "postgres://localhost/db"},
		},
		{
			Name:    "playwright",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-playwright"},
			Env:     map[string]string{"DEBUG": "1"},
		},
	}
	cli := New(cfg)

	_, args, _ := cli.BuildCommand(types.ExecuteRequest{Prompt: "x", Model: "sonnet"})

	// args should contain --mcp-config .corvex/mcp.json
	var found bool
	for i, a := range args {
		if a == "--mcp-config" && i+1 < len(args) && args[i+1] == ".corvex/mcp.json" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected --mcp-config .corvex/mcp.json in args, got %v", args)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".corvex", "mcp.json"))
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}

	var got struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal mcp.json: %v\nraw: %s", err, data)
	}

	pg, ok := got.MCPServers["postgres"]
	if !ok {
		t.Fatalf("missing postgres entry, got %+v", got.MCPServers)
	}
	if pg.Command != "npx" || len(pg.Args) != 3 {
		t.Errorf("postgres entry wrong: %+v", pg)
	}

	pw, ok := got.MCPServers["playwright"]
	if !ok {
		t.Fatalf("missing playwright entry")
	}
	if pw.Env["DEBUG"] != "1" {
		t.Errorf("playwright env = %v, want DEBUG=1", pw.Env)
	}
}

func TestBuildCommand_NoMCPServers_NoFlag(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cli := New(cfg)

	_, args, _ := cli.BuildCommand(types.ExecuteRequest{Prompt: "x", Model: "sonnet"})

	for _, a := range args {
		if a == "--mcp-config" {
			t.Errorf("--mcp-config should not appear when MCPServers is empty; args = %v", args)
		}
	}
}

func TestBuildCommand_MCPExpandsEnvVars(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CORVEX_TEST_DB_PASSWORD", "s3cret!")
	t.Setenv("CORVEX_TEST_DB_HOST", "db.internal")

	cfg := config.Default()
	cfg.Sandbox.MCPServers = []config.MCPServerConfig{
		{
			Name:    "postgres",
			Command: "${CORVEX_TEST_BIN:-npx}",
			Args:    []string{"-y", "@modelcontextprotocol/server-postgres", "postgres://app:${CORVEX_TEST_DB_PASSWORD}@${CORVEX_TEST_DB_HOST}/app"},
			Env:     map[string]string{"DB_PASSWORD": "${CORVEX_TEST_DB_PASSWORD}"},
		},
	}
	cli := New(cfg)

	cli.BuildCommand(types.ExecuteRequest{Prompt: "x", Model: "sonnet"})

	data, err := os.ReadFile(filepath.Join(dir, ".corvex", "mcp.json"))
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	got := string(data)

	if !strings.Contains(got, "s3cret!") {
		t.Errorf("expected expanded password in args; got:\n%s", got)
	}
	if !strings.Contains(got, "db.internal") {
		t.Errorf("expected expanded host in args; got:\n%s", got)
	}
	if !strings.Contains(got, `"DB_PASSWORD": "s3cret!"`) {
		t.Errorf("expected expanded env value; got:\n%s", got)
	}
	// The literal `${...}` form must not survive into the materialised config.
	if strings.Contains(got, "${CORVEX_TEST_DB_PASSWORD}") {
		t.Errorf("unexpanded ${...} leaked into mcp.json:\n%s", got)
	}
}

func TestBuildCommand_InvalidMCPServer_SkipsFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := config.Default()
	cfg.Sandbox.MCPServers = []config.MCPServerConfig{
		{Name: "missing-command"}, // Command empty → invalid
	}
	cli := New(cfg)

	_, args, _ := cli.BuildCommand(types.ExecuteRequest{Prompt: "x", Model: "sonnet"})

	for _, a := range args {
		if a == "--mcp-config" {
			t.Errorf("--mcp-config should not appear when MCP write fails; args = %v", args)
		}
	}
}

func TestBuildCommand_EnvForwarding(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cli := New(cfg)

	req := types.ExecuteRequest{
		Prompt: "do work",
		Model:  "sonnet",
		Env: map[string]string{
			"FOO": "bar",
			"BAZ": "qux",
		},
	}

	_, _, env := cli.BuildCommand(req)

	if env["FOO"] != "bar" {
		t.Errorf("env[FOO] = %q, want %q", env["FOO"], "bar")
	}
	if env["BAZ"] != "qux" {
		t.Errorf("env[BAZ] = %q, want %q", env["BAZ"], "qux")
	}
	if len(env) != 2 {
		t.Errorf("env has %d entries, want 2", len(env))
	}
}

func TestParseFullOutput_ValidNDJSON(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cli := New(cfg)

	stdout := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":" World"}]}}`,
	}, "\n")

	result, err := cli.ParseFullOutput(stdout, 0, 5*time.Second)
	if err != nil {
		t.Fatalf("ParseFullOutput() error = %v", err)
	}

	if result.Output != "Hello World" {
		t.Errorf("Output = %q, want %q", result.Output, "Hello World")
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestParseFullOutput_WithResultLine(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cli := New(cfg)

	stdout := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done"}]}}`,
		`{"type":"result","subtype":"success","result":"All done","total_cost_usd":0.05,"total_input_tokens":1000,"total_output_tokens":500,"duration_ms":3000}`,
	}, "\n")

	result, err := cli.ParseFullOutput(stdout, 0, 5*time.Second)
	if err != nil {
		t.Fatalf("ParseFullOutput() error = %v", err)
	}

	if result.Output != "Done" {
		t.Errorf("Output = %q, want %q", result.Output, "Done")
	}
	if result.TokensIn != 1000 {
		t.Errorf("TokensIn = %d, want 1000", result.TokensIn)
	}
	if result.TokensOut != 500 {
		t.Errorf("TokensOut = %d, want 500", result.TokensOut)
	}
	if result.CostUSD != 0.05 {
		t.Errorf("CostUSD = %f, want 0.05", result.CostUSD)
	}
	if result.DurationMs != 3000 {
		t.Errorf("DurationMs = %d, want 3000 (from result line)", result.DurationMs)
	}
}

func TestParseFullOutput_EmptyOutput(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cli := New(cfg)

	result, err := cli.ParseFullOutput("", 0, time.Second)
	if err != nil {
		t.Fatalf("ParseFullOutput() error = %v", err)
	}

	if result.Output != "" {
		t.Errorf("Output = %q, want empty", result.Output)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestParseFullOutput_MalformedLines(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cli := New(cfg)

	stdout := strings.Join([]string{
		`not json at all`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"valid"}]}}`,
		`{broken json`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":" output"}]}}`,
	}, "\n")

	result, err := cli.ParseFullOutput(stdout, 0, time.Second)
	if err != nil {
		t.Fatalf("ParseFullOutput() error = %v", err)
	}

	if result.Output != "valid output" {
		t.Errorf("Output = %q, want %q", result.Output, "valid output")
	}
}
