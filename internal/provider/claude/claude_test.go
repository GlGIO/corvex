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

	result := mergeEnv(base, extra, nil)

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

func TestParseFullOutput_NoResultLine_ReturnsError(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cli := New(cfg)

	// exit 0 but no result line → error
	stdout := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"partial"}]}}`

	_, err := cli.ParseFullOutput(stdout, 0, time.Second)
	if err == nil {
		t.Fatal("expected error when exit 0 and no result line, got nil")
	}
	if !strings.Contains(err.Error(), "no result line") {
		t.Errorf("error %q does not mention 'no result line'", err.Error())
	}
}

func TestParseFullOutput_NoResultLine_NonZeroExitNoNewError(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cli := New(cfg)

	// exit non-zero and no result line → existing behavior (no new error)
	stdout := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"partial"}]}}`

	_, err := cli.ParseFullOutput(stdout, 1, time.Second)
	if err != nil {
		t.Fatalf("expected nil error for non-zero exit without result line, got %v", err)
	}
}

func TestExecuteWithProgress_NoResultLine_ReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script not available on Windows")
	}

	cfg := config.Default()
	cli := New(cfg)

	// emit assistant text but no result line; exit 0
	canned := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"partial"}]}}`

	cli.cmdRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "printf", "%s\n", canned)
	}

	_, err := cli.ExecuteWithProgress(context.Background(),
		types.ExecuteRequest{Prompt: "x", Model: "sonnet"},
		nil,
	)
	if err == nil {
		t.Fatal("expected error when exit 0 and no result line, got nil")
	}
	if !strings.Contains(err.Error(), "no result line") {
		t.Errorf("error %q does not mention 'no result line'", err.Error())
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

	// The custody pair is on EVERY call, including one that asks for nothing:
	// `--mcp-config` without `--strict-mcp-config` adds to the machine's own
	// servers rather than replacing them, so "no servers" has to be stated.
	want := []string{"-p", "do work", "--model", "sonnet", "--output-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions", "--mcp-config", mcpNoneRelPath, "--strict-mcp-config"}
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

	baseLen := 12 // the nine base args plus the custody pair (--mcp-config <file> --strict-mcp-config)
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
		"--mcp-config", mcpNoneRelPath, "--strict-mcp-config",
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

	// AllowMCP is the worker's flag: the declared servers exist for the role
	// that reads production to do the work, and the reviewer builds its request
	// without it.
	_, args, _ := cli.BuildCommand(types.ExecuteRequest{Prompt: "x", Model: "sonnet", AllowMCP: true})

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

// A repository that declares no servers still gets an explicit empty set.
//
// This used to assert the opposite — no servers, no flag — and that was the
// hole: a repository with nothing declared was the case where the agent
// silently inherited every MCP server on the machine. "Declared nothing" has to
// mean "gets nothing", and only a file plus `--strict-mcp-config` says that.
func TestBuildCommand_NoMCPServersStillMeansAnExplicitEmptySet(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Default()
	cli := New(cfg)

	// AllowMCP is the worker's flag: the declared servers exist for the role
	// that reads production to do the work, and the reviewer builds its request
	// without it.
	_, args, _ := cli.BuildCommand(types.ExecuteRequest{Prompt: "x", Model: "sonnet", AllowMCP: true})

	if !hasFlagValue(args, "--mcp-config", mcpConfigRelPath) || !hasFlag(args, "--strict-mcp-config") {
		t.Fatalf("a repository that declares nothing must still be told it gets nothing: %v", args)
	}
	data, err := os.ReadFile(mcpConfigRelPath)
	if err != nil {
		t.Fatalf("reading %s: %v", mcpConfigRelPath, err)
	}
	var payload struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("%s is not JSON: %s", mcpConfigRelPath, data)
	}
	if len(payload.MCPServers) != 0 {
		t.Errorf("the config carries %d server(s) for a repository that declared none: %s", len(payload.MCPServers), data)
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

	cli.BuildCommand(types.ExecuteRequest{Prompt: "x", Model: "sonnet", AllowMCP: true})

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

	// AllowMCP is the worker's flag: the declared servers exist for the role
	// that reads production to do the work, and the reviewer builds its request
	// without it.
	_, args, _ := cli.BuildCommand(types.ExecuteRequest{Prompt: "x", Model: "sonnet", AllowMCP: true})

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
		`{"type":"result","subtype":"success","result":"done","total_cost_usd":0,"total_input_tokens":0,"total_output_tokens":0,"duration_ms":0}`,
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

	// exitCode=1: non-zero exit preserves existing behavior (no "no result line" error).
	result, err := cli.ParseFullOutput("", 1, time.Second)
	if err != nil {
		t.Fatalf("ParseFullOutput() error = %v", err)
	}

	if result.Output != "" {
		t.Errorf("Output = %q, want empty", result.Output)
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
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
		`{"type":"result","subtype":"success","result":"done","total_cost_usd":0,"total_input_tokens":0,"total_output_tokens":0,"duration_ms":0}`,
	}, "\n")

	result, err := cli.ParseFullOutput(stdout, 0, time.Second)
	if err != nil {
		t.Fatalf("ParseFullOutput() error = %v", err)
	}

	if result.Output != "valid output" {
		t.Errorf("Output = %q, want %q", result.Output, "valid output")
	}
}

func TestWriteMCPConfig_Perms0600(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	servers := []config.MCPServerConfig{{
		Name:    "postgres",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-postgres", "postgres://localhost/db"},
	}}
	if err := writeMCPConfig(mcpConfigRelPath, servers); err != nil {
		t.Fatalf("writeMCPConfig() error = %v", err)
	}

	info, err := os.Stat(mcpConfigRelPath)
	if err != nil {
		t.Fatalf("stat mcp.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mcp.json mode = %o, want 600 (secrets must not be world-readable)", perm)
	}
}

// The MCP servers reach the WORKER and nobody else, on every path.
//
// Two defects met here, and the second was created by fixing the first:
//
//   - `ExecuteWithProgress` — the path a LOCAL sandbox takes, and local is the
//     default — assembled its own args and left the MCP config out. A repository
//     that declared a production database got it in Docker and got nothing
//     locally, while the preflight reported the dependency satisfied because it
//     was DECLARED. The failure then looked like a model refusing to use a tool
//     it had never been given.
//   - Handing both paths the same assembly gave the REVIEWER the servers too.
//     The rule that only the worker gets them had been in the config
//     documentation since MCP landed and had no enforcement point anywhere.
func TestArgsFor_OnlyTheWorkerGetsTheDeclaredServers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Default()
	cfg.Sandbox.MCPServers = []config.MCPServerConfig{{Name: "prd", Command: "/bin/echo"}}
	cfg.Sandbox.WorkerExtraArgs = []string{"--fallback-model", "sonnet"}
	cli := New(cfg)

	carries := func(args []string, flag string) bool {
		for _, a := range args {
			if a == flag {
				return true
			}
		}
		return false
	}

	worker := cli.argsFor(types.ExecuteRequest{Prompt: "x", Model: "sonnet", AllowMCP: true})
	if !carries(worker, "--mcp-config") {
		t.Errorf("the worker did not receive the declared servers: %v", worker)
	}
	if !carries(worker, "--fallback-model") {
		t.Errorf("the sandbox's extra args were dropped: %v", worker)
	}

	if !carries(worker, "--strict-mcp-config") {
		t.Errorf("the worker's declared servers are additive to the machine's own: %v", worker)
	}

	// The judge reads a diff. It does not need production, and a credential
	// handed to something that does not need it is blast radius nobody measured.
	//
	// This check used to read `if carries(reviewer, "--mcp-config")` — the
	// reviewer was correct precisely BY NOT carrying the flag. That was the
	// belief the first real run refuted: the Claude CLI loads the machine's own
	// MCP configuration when none is given, so the flagless reviewer had the
	// widest access of anyone. The claim now is the one that holds: it is
	// pointed at an empty set and told that set is the only one.
	reviewer := cli.argsFor(types.ExecuteRequest{Prompt: "you are a code reviewer", Model: "sonnet"})
	if !hasFlagValue(reviewer, "--mcp-config", mcpNoneRelPath) || !carries(reviewer, "--strict-mcp-config") {
		t.Errorf("the reviewer is not held to an empty server set: %v", reviewer)
	}
	if hasFlagValue(reviewer, "--mcp-config", mcpConfigRelPath) {
		t.Errorf("the reviewer was handed the production servers: %v", reviewer)
	}

	// And both assemblies are the same one: the sandboxed path and the local
	// path cannot disagree about what the agent was given.
	_, built, _ := cli.BuildCommand(types.ExecuteRequest{Prompt: "x", Model: "sonnet", AllowMCP: true})
	if strings.Join(built, " ") != strings.Join(worker, " ") {
		t.Errorf("the two paths build different commands:\n docker: %v\n local:  %v", built, worker)
	}
}

// Withholding a flag is not a restriction when the default is "load
// everything".
//
// MEASURED on the first real incident run, in the ledger: the worker called
// `mcp__nb2bPRD__query` 28 times and `mcp__SmartCarePRD__query` 4 times — and
// the repository had declared exactly ONE server, SmartCarePRD. `--mcp-config`
// ADDS to whatever the Claude CLI already loads for that working directory, and
// what it loads is the person's own `~/.claude.json`, which on a developer
// machine holds every production database they have ever connected to.
//
// So the rule "only the worker reaches production" — which internal/step
// enforces by giving the reviewer a request with AllowMCP false — was being
// enforced against a door that was never the way in. The planner and the
// reviewer got no `--mcp-config` and reached production anyway.
//
// The fix has no inference in it: every call carries an explicit config and
// `--strict-mcp-config`. These two tests are the two halves of that sentence.
func TestBuildCommand_TheReviewerIsGivenAnEmptyServerSetAndToldToUseOnlyIt(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg := config.Default()
	cfg.Sandbox.MCPServers = []config.MCPServerConfig{
		{Name: "SmartCarePRD", Command: "mcp-server-postgres", Args: []string{"postgres://prod/db"}},
	}
	cli := New(cfg)

	_, args, _ := cli.BuildCommand(types.ExecuteRequest{Prompt: "julgue", Model: "sonnet", AllowMCP: false})

	if !hasFlagValue(args, "--mcp-config", mcpNoneRelPath) {
		t.Fatalf("the reviewer was given no explicit config, so the CLI loads the machine's own servers: %v", args)
	}
	if !hasFlag(args, "--strict-mcp-config") {
		t.Errorf("the reviewer's call does not say --strict-mcp-config, so the empty file is only one of the sources: %v", args)
	}
	// And the file it points at really is empty — the name is not the promise.
	data, err := os.ReadFile(mcpNoneRelPath)
	if err != nil {
		t.Fatalf("reading %s: %v", mcpNoneRelPath, err)
	}
	var payload struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("%s is not JSON: %s", mcpNoneRelPath, data)
	}
	if len(payload.MCPServers) != 0 {
		t.Errorf("the reviewer's config carries %d server(s): %s", len(payload.MCPServers), data)
	}
	// The reviewer must not even be pointed at the worker's file.
	if hasFlagValue(args, "--mcp-config", mcpConfigRelPath) {
		t.Error("the reviewer was handed the worker's server list")
	}
}

func TestBuildCommand_TheWorkerGetsTheDeclaredServersAndNothingElse(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg := config.Default()
	cfg.Sandbox.MCPServers = []config.MCPServerConfig{
		{Name: "SmartCarePRD", Command: "mcp-server-postgres", Args: []string{"postgres://prod/db"}},
	}
	cli := New(cfg)

	_, args, _ := cli.BuildCommand(types.ExecuteRequest{Prompt: "trabalhe", Model: "sonnet", AllowMCP: true})

	if !hasFlagValue(args, "--mcp-config", mcpConfigRelPath) {
		t.Fatalf("the worker did not get the declared servers: %v", args)
	}
	// Strict on the worker too, and for the same reason: `--mcp-config` adds,
	// so without this the worker reaches every database on the machine and the
	// `requires: - mcp:` preflight can be satisfied by a server nobody
	// declared.
	if !hasFlag(args, "--strict-mcp-config") {
		t.Errorf("the worker's call does not say --strict-mcp-config, so it also reaches undeclared servers: %v", args)
	}
	// Two files, never one rewritten per call: a fan-out runs a worker and a
	// reviewer at the same time, and a shared path would hand one of them the
	// other's list.
	if mcpNoneRelPath == mcpConfigRelPath {
		t.Fatal("the worker and the reviewer share one config path")
	}
}

// hasFlagValue reports whether `flag value` appear next to each other.
func hasFlagValue(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// The failure path of the guard is not the thing the guard prevents.
//
// The line that handles "could not write the config" used to log
// "continuing without MCP servers" and drop both flags. That sentence was true
// of the old design and became a lie in the new one: without the flags the CLI
// loads every server on the machine, so the branch taken when the guard breaks
// was the branch with the widest access in the program.
func TestArgsFor_AnUnwritableConfigStillHoldsTheCallToNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// `.corvex` exists as a FILE, so creating the directory under it fails.
	if err := os.WriteFile(".corvex", []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Sandbox.MCPServers = []config.MCPServerConfig{
		{Name: "PRD", Command: "mcp-server-postgres", Args: []string{"postgres://prod/db"}},
	}
	cli := New(cfg)

	for _, tc := range []struct {
		name  string
		allow bool
	}{{"worker", true}, {"reviewer", false}} {
		args := cli.argsFor(types.ExecuteRequest{Prompt: "x", Model: "sonnet", AllowMCP: tc.allow})
		if !hasFlag(args, "--strict-mcp-config") {
			t.Errorf("%s: the call that could not write its config was let out unrestricted: %v", tc.name, args)
		}
		if hasFlag(args, "--mcp-config") {
			t.Errorf("%s: a config was named that does not exist: %v", tc.name, args)
		}
	}
}

// TestApplyResultLine_TheShapeTheRealCLIPrints is pinned to a result line
// recorded from claude 2.1.292 (session ids redacted), not to a hand-written
// one: every hand-written fixture in this repo used total_input_tokens /
// total_output_tokens, which the real CLI does not print, and so every real run
// reached the ledger with zero tokens while the tests were green.
func TestApplyResultLine_TheShapeTheRealCLIPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "result_line_2.1.292.json"))
	if err != nil {
		t.Fatal(err)
	}
	var res resultLine
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	got := &types.ExecuteResult{}
	applyResultLine(got, res)
	if got.TokensIn != 20+23110+22887 || got.TokensOut != 522 {
		t.Errorf("tokens = %d in / %d out, want 46017 / 522 (input + cache creation + cache read)", got.TokensIn, got.TokensOut)
	}
	if !strings.Contains(string(got.Structured), `"verdict"`) {
		t.Errorf("structured_output was not carried: %q", got.Structured)
	}
	if got.APIErrorStatus != 0 {
		t.Errorf("api_error_status null must read as 0, got %d", got.APIErrorStatus)
	}
}

func TestBuildArgs_JSONSchemaOnlyWhenAsked(t *testing.T) {
	without := strings.Join(buildArgs(types.ExecuteRequest{Prompt: "p", Model: "m"}), " ")
	if strings.Contains(without, "--json-schema") {
		t.Error("--json-schema passed when no schema was asked for")
	}
	with := buildArgs(types.ExecuteRequest{Prompt: "p", Model: "m", JSONSchema: `{"type":"object"}`})
	for i, a := range with {
		if a == "--json-schema" && i+1 < len(with) && with[i+1] == `{"type":"object"}` {
			return
		}
	}
	t.Errorf("--json-schema missing or detached from its value: %v", with)
}
