package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/sandbox"
	"github.com/giovannialves/corvex/internal/types"
)

type mockSandbox struct {
	mu           sync.Mutex
	prepareErr   error
	runFn        func(ctx context.Context, req sandbox.RunRequest) (*sandbox.RunResult, error)
	cleanupErr   error
	available    bool
	prepareCalls int
	cleanupCalls int
	runCalls     []sandbox.RunRequest
}

func (m *mockSandbox) Prepare(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prepareCalls++
	return m.prepareErr
}

func (m *mockSandbox) Run(ctx context.Context, req sandbox.RunRequest) (*sandbox.RunResult, error) {
	m.mu.Lock()
	m.runCalls = append(m.runCalls, req)
	m.mu.Unlock()
	if m.runFn != nil {
		return m.runFn(ctx, req)
	}
	return &sandbox.RunResult{Stdout: "", ExitCode: 0}, nil
}

func (m *mockSandbox) Cleanup(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupCalls++
	return m.cleanupErr
}

func (m *mockSandbox) IsAvailable(_ context.Context) bool {
	return m.available
}

type mockCommandProvider struct {
	mockProvider
	buildCommandFn    func(req types.ExecuteRequest) (string, []string, map[string]string)
	parseFullOutputFn func(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error)
}

func (m *mockCommandProvider) BuildCommand(req types.ExecuteRequest) (string, []string, map[string]string) {
	if m.buildCommandFn != nil {
		return m.buildCommandFn(req)
	}
	return "test-bin", []string{"-p", req.Prompt}, nil
}

func (m *mockCommandProvider) ParseFullOutput(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error) {
	if m.parseFullOutputFn != nil {
		return m.parseFullOutputFn(stdout, exitCode, elapsed)
	}
	return &types.ExecuteResult{Output: stdout, ExitCode: exitCode, DurationMs: elapsed.Milliseconds()}, nil
}

func TestBuildWorkerPrompt_BasicTask(t *testing.T) {
	t.Parallel()
	task := &types.Task{
		ID:          "S01",
		Title:       "Basic Task",
		Description: "Do something basic",
	}

	prompt := buildWorkerPrompt(task, "", nil, "", "")

	if !strings.Contains(prompt, "## Current Task: S01 — Basic Task") {
		t.Error("prompt missing current task header")
	}
	if !strings.Contains(prompt, "Do something basic") {
		t.Error("prompt missing description")
	}
	if strings.Contains(prompt, "## Agent Instructions") {
		t.Error("prompt should not contain agent instructions when empty")
	}
	if strings.Contains(prompt, "## Project Context") {
		t.Error("prompt should not contain project context when empty")
	}
	if strings.Contains(prompt, "## Previous Work") {
		t.Error("prompt should not contain previous work when empty")
	}
	if strings.Contains(prompt, "## Previous Attempt Failed") {
		t.Error("prompt should not contain diagnosis when empty")
	}
}

func TestBuildWorkerPrompt_WithAnchorContext(t *testing.T) {
	t.Parallel()
	task := &types.Task{ID: "S02", Title: "Task", Description: "desc"}
	anchor := "## Completed Work\n\n### S01 — First\nDone."

	prompt := buildWorkerPrompt(task, anchor, nil, "", "")

	if !strings.Contains(prompt, "## Previous Work") {
		t.Error("prompt missing previous work section")
	}
	if !strings.Contains(prompt, anchor) {
		t.Error("prompt missing anchor context content")
	}
}

func TestBuildWorkerPrompt_WithContextDocs(t *testing.T) {
	t.Parallel()
	task := &types.Task{ID: "S01", Title: "Task", Description: "desc"}
	docs := []string{"doc1 content", "doc2 content"}

	prompt := buildWorkerPrompt(task, "", docs, "", "")

	if !strings.Contains(prompt, "## Project Context") {
		t.Error("prompt missing project context section")
	}
	if !strings.Contains(prompt, "doc1 content") {
		t.Error("prompt missing first doc")
	}
	if !strings.Contains(prompt, "doc2 content") {
		t.Error("prompt missing second doc")
	}
}

func TestBuildWorkerPrompt_WithAgentPrompt(t *testing.T) {
	t.Parallel()
	task := &types.Task{ID: "S01", Title: "Task", Description: "desc"}
	agent := "You are a database specialist."

	prompt := buildWorkerPrompt(task, "", nil, agent, "")

	if !strings.Contains(prompt, "## Agent Instructions") {
		t.Error("prompt missing agent instructions section")
	}
	if !strings.Contains(prompt, agent) {
		t.Error("prompt missing agent prompt content")
	}
}

func TestBuildWorkerPrompt_WithDiagnosis(t *testing.T) {
	t.Parallel()
	task := &types.Task{ID: "S01", Title: "Task", Description: "desc"}
	diag := "Missing import for fmt package"

	prompt := buildWorkerPrompt(task, "", nil, "", diag)

	if !strings.Contains(prompt, "## Previous Attempt Failed") {
		t.Error("prompt missing diagnosis section")
	}
	if !strings.Contains(prompt, diag) {
		t.Error("prompt missing diagnosis content")
	}
}

func TestBuildWorkerPrompt_AllCombined(t *testing.T) {
	t.Parallel()
	task := &types.Task{
		ID:          "S03",
		Title:       "Full Task",
		Description: "Complete implementation",
		Criteria:    []string{"Tests pass", "No lint errors"},
		Files: types.TaskFiles{
			Create: []string{"new.go"},
			Modify: []string{"existing.go"},
		},
	}

	prompt := buildWorkerPrompt(task, "anchor ctx", []string{"doc1"}, "agent prompt", "prev error")

	expectedOrder := []string{
		"## Agent Instructions",
		"## Project Context",
		"## Previous Work",
		"## Current Task: S03",
		"### Description",
		"### Success Criteria",
		"### Files",
		"## Previous Attempt Failed",
		"## Instructions",
	}

	lastIdx := -1
	for _, section := range expectedOrder {
		idx := strings.Index(prompt, section)
		if idx == -1 {
			t.Errorf("prompt missing section %q", section)
			continue
		}
		if idx <= lastIdx {
			t.Errorf("section %q at index %d is not after previous section at index %d", section, idx, lastIdx)
		}
		lastIdx = idx
	}

	if !strings.Contains(prompt, "- [ ] Tests pass") {
		t.Error("prompt missing criterion checkbox")
	}
	if !strings.Contains(prompt, "- Create: new.go") {
		t.Error("prompt missing create file")
	}
	if !strings.Contains(prompt, "- Modify: existing.go") {
		t.Error("prompt missing modify file")
	}
}

func TestExecute_NoAllowedTools(t *testing.T) {
	t.Parallel()
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "done"}, nil
		},
	}
	w := NewWorker(mock, "test-model", "/tmp", nil)
	task := &types.Task{ID: "S01", Title: "Task", Description: "desc"}

	if _, err := w.Execute(context.Background(), task, "", nil, "", ""); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	if len(mock.calls[0].AllowedTools) != 0 {
		t.Errorf("AllowedTools = %v, want empty (no restrictions)", mock.calls[0].AllowedTools)
	}
}

func TestLoadContextDocs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "DESIGN.md"), []byte("design content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	docs := loadContextDocs(dir, []string{"*.md"})

	if len(docs) != 2 {
		t.Fatalf("loadContextDocs() returned %d docs, want 2", len(docs))
	}

	combined := strings.Join(docs, " ")
	if !strings.Contains(combined, "readme content") {
		t.Error("missing README.md content")
	}
	if !strings.Contains(combined, "design content") {
		t.Error("missing DESIGN.md content")
	}
}

func TestLoadContextDocs_Empty(t *testing.T) {
	t.Parallel()
	docs := loadContextDocs("/tmp", nil)
	if docs != nil {
		t.Errorf("loadContextDocs(nil) = %v, want nil", docs)
	}

	docs = loadContextDocs("/tmp", []string{})
	if docs != nil {
		t.Errorf("loadContextDocs(empty) = %v, want nil", docs)
	}
}

func TestLoadContextDocs_EmptyFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "whitespace.md"), []byte("   \n  "), 0o644); err != nil {
		t.Fatal(err)
	}

	docs := loadContextDocs(dir, []string{"*.md"})
	if len(docs) != 0 {
		t.Errorf("loadContextDocs() returned %d docs for empty files, want 0", len(docs))
	}
}

func TestLoadAgentPrompt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "backend.md"), []byte("You are a backend expert."), 0o644); err != nil {
		t.Fatal(err)
	}

	routing := map[string]string{
		"backend": "agents/backend.md",
	}

	got := loadAgentPrompt(dir, routing, types.TypeBackend)
	if got != "You are a backend expert." {
		t.Errorf("loadAgentPrompt() = %q, want %q", got, "You are a backend expert.")
	}
}

func TestLoadAgentPrompt_NoMatch(t *testing.T) {
	t.Parallel()
	routing := map[string]string{
		"backend": "agents/backend.md",
	}

	got := loadAgentPrompt("/tmp", routing, types.TypeFrontend)
	if got != "" {
		t.Errorf("loadAgentPrompt() = %q, want empty string", got)
	}
}

func TestLoadAgentPrompt_NilRouting(t *testing.T) {
	t.Parallel()
	got := loadAgentPrompt("/tmp", nil, types.TypeGeneral)
	if got != "" {
		t.Errorf("loadAgentPrompt(nil routing) = %q, want empty string", got)
	}
}

func TestLoadAgentPrompt_MissingFile(t *testing.T) {
	t.Parallel()
	routing := map[string]string{
		"general": "agents/missing.md",
	}
	got := loadAgentPrompt("/tmp", routing, types.TypeGeneral)
	if got != "" {
		t.Errorf("loadAgentPrompt(missing file) = %q, want empty string", got)
	}
}

func TestExecute_ModelAndWorkDir(t *testing.T) {
	t.Parallel()
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "done"}, nil
		},
	}
	w := NewWorker(mock, "sonnet", "/my/workdir", nil)
	task := &types.Task{ID: "S01", Title: "Task", Description: "desc"}

	if _, err := w.Execute(context.Background(), task, "", nil, "", ""); err != nil {
		t.Fatal(err)
	}

	req := mock.calls[0]
	if req.Model != "sonnet" {
		t.Errorf("Model = %q, want %q", req.Model, "sonnet")
	}
	if req.WorkDir != "/my/workdir" {
		t.Errorf("WorkDir = %q, want %q", req.WorkDir, "/my/workdir")
	}
}

func TestWorkerExecute_ViaSandbox(t *testing.T) {
	t.Parallel()

	sb := &mockSandbox{
		runFn: func(_ context.Context, req sandbox.RunRequest) (*sandbox.RunResult, error) {
			return &sandbox.RunResult{
				Stdout:   "raw sandbox stdout",
				ExitCode: 0,
			}, nil
		},
	}

	prov := &mockCommandProvider{
		mockProvider: mockProvider{
			executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
				t.Error("provider.Execute should not be called when sandbox is used")
				return nil, fmt.Errorf("should not be called")
			},
		},
		parseFullOutputFn: func(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "parsed: " + stdout, ExitCode: exitCode, DurationMs: elapsed.Milliseconds()}, nil
		},
	}

	w := NewWorker(prov, "sonnet", "/tmp", sb)
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	result, err := w.Execute(context.Background(), task, "", nil, "", "")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Output != "parsed: raw sandbox stdout" {
		t.Errorf("Output = %q, want %q", result.Output, "parsed: raw sandbox stdout")
	}

	sb.mu.Lock()
	runCount := len(sb.runCalls)
	sb.mu.Unlock()
	if runCount != 1 {
		t.Errorf("sandbox.Run called %d times, want 1", runCount)
	}
}

func TestWorkerExecute_FallbackDirect(t *testing.T) {
	t.Parallel()

	sb := &mockSandbox{}
	prov := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: "direct output"}, nil
		},
	}

	w := NewWorker(prov, "sonnet", "/tmp", sb)
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	result, err := w.Execute(context.Background(), task, "", nil, "", "")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Output != "direct output" {
		t.Errorf("Output = %q, want %q", result.Output, "direct output")
	}

	sb.mu.Lock()
	runCount := len(sb.runCalls)
	sb.mu.Unlock()
	if runCount != 0 {
		t.Errorf("sandbox.Run should not be called for non-CommandBuilder provider, got %d calls", runCount)
	}
}

func TestWorkerExecute_NilSandbox(t *testing.T) {
	t.Parallel()

	called := false
	prov := &mockCommandProvider{
		mockProvider: mockProvider{
			executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
				called = true
				return &types.ExecuteResult{Output: "direct output"}, nil
			},
		},
	}

	w := NewWorker(prov, "sonnet", "/tmp", nil)
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	result, err := w.Execute(context.Background(), task, "", nil, "", "")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !called {
		t.Error("provider.Execute should be called when sandbox is nil")
	}
	if result.Output != "direct output" {
		t.Errorf("Output = %q, want %q", result.Output, "direct output")
	}
}

func TestCollectAuthEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA-test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret-test")
	t.Setenv("AWS_SESSION_TOKEN", "token-test")
	t.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("AWS_PROFILE", "dev")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("CORVEX_TASK", "S01")
	t.Setenv("UNRELATED_VAR", "should-not-appear")

	env := collectAuthEnv()

	expected := map[string]string{
		"ANTHROPIC_API_KEY":      "test-key",
		"CLAUDE_CODE_USE_BEDROCK": "1",
		"AWS_ACCESS_KEY_ID":      "AKIA-test",
		"AWS_SECRET_ACCESS_KEY":  "secret-test",
		"AWS_SESSION_TOKEN":      "token-test",
		"AWS_DEFAULT_REGION":     "us-east-1",
		"AWS_REGION":             "us-west-2",
		"AWS_PROFILE":            "dev",
		"OPENAI_API_KEY":         "sk-test",
		"CORVEX_TASK":            "S01",
	}
	for k, v := range expected {
		if env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, env[k], v)
		}
	}

	if _, ok := env["UNRELATED_VAR"]; ok {
		t.Error("UNRELATED_VAR should not be collected")
	}
}

func TestCollectAuthEnv_NoMatch(t *testing.T) {
	t.Setenv("TOTALLY_UNRELATED", "value1")
	t.Setenv("ANOTHER_RANDOM", "value2")

	env := collectAuthEnv()

	if _, ok := env["TOTALLY_UNRELATED"]; ok {
		t.Error("TOTALLY_UNRELATED should not be in auth env")
	}
	if _, ok := env["ANOTHER_RANDOM"]; ok {
		t.Error("ANOTHER_RANDOM should not be in auth env")
	}
}

func TestWorkerExecute_SandboxError(t *testing.T) {
	t.Parallel()

	sb := &mockSandbox{
		runFn: func(_ context.Context, _ sandbox.RunRequest) (*sandbox.RunResult, error) {
			return nil, fmt.Errorf("container crashed")
		},
	}

	prov := &mockCommandProvider{}

	w := NewWorker(prov, "sonnet", "/tmp", sb)
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	_, err := w.Execute(context.Background(), task, "", nil, "", "")
	if err == nil {
		t.Fatal("expected error from sandbox")
	}
	if !strings.Contains(err.Error(), "container crashed") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "container crashed")
	}
	if !strings.Contains(err.Error(), "sandbox execution") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "sandbox execution")
	}
}

func TestWorkerExecute_NonZeroExitCode(t *testing.T) {
	t.Parallel()

	sb := &mockSandbox{
		runFn: func(_ context.Context, _ sandbox.RunRequest) (*sandbox.RunResult, error) {
			return &sandbox.RunResult{
				Stdout:   "partial output",
				Stderr:   "permission denied",
				ExitCode: 1,
			}, nil
		},
	}

	prov := &mockCommandProvider{
		parseFullOutputFn: func(stdout string, exitCode int, elapsed time.Duration) (*types.ExecuteResult, error) {
			return &types.ExecuteResult{Output: stdout, ExitCode: exitCode, DurationMs: elapsed.Milliseconds()}, nil
		},
	}

	w := NewWorker(prov, "sonnet", "/tmp", sb)
	task := &types.Task{ID: "S01", Title: "Test", Description: "desc"}

	result, err := w.Execute(context.Background(), task, "", nil, "", "")
	if err == nil {
		t.Fatal("expected error for non-zero exit code")
	}
	if !strings.Contains(err.Error(), "exit code 1") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "exit code 1")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error = %q, want to contain stderr", err.Error())
	}
	if result == nil {
		t.Fatal("result should not be nil even on non-zero exit")
	}
	if result.Output != "partial output" {
		t.Errorf("Output = %q, want %q", result.Output, "partial output")
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
}
