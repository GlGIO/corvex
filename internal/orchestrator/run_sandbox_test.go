package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

func TestRun_WithSandbox(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-sandbox"
	setupProject(t, dir, project, allPassedTasksMD)
	gitCommitAll(t, dir, "add tasks")

	sb := &mockSandbox{available: true}
	events := make(chan Event, 100)

	cfg := config.Default()
	cfg.Project.Name = project

	orch := New(Options{
		Config:   cfg,
		Provider: &mockProvider{},
		WorkDir:  dir,
		Events:   events,
		Sandbox:  sb,
	})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	sb.mu.Lock()
	prepCalls := sb.prepareCalls
	cleanCalls := sb.cleanupCalls
	sb.mu.Unlock()

	if prepCalls != 1 {
		t.Errorf("Prepare called %d times, want 1", prepCalls)
	}
	if cleanCalls != 1 {
		t.Errorf("Cleanup called %d times, want 1", cleanCalls)
	}
}

func TestRun_SandboxPrepareError(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-sandbox-err"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	sb := &mockSandbox{prepareErr: fmt.Errorf("docker not available")}

	cfg := config.Default()
	cfg.Project.Name = project

	orch := New(Options{
		Config:   cfg,
		Provider: &mockProvider{},
		WorkDir:  dir,
		Sandbox:  sb,
	})

	err := orch.Run(context.Background(), project)
	if err == nil {
		t.Fatal("expected error when sandbox prepare fails")
	}
	if !strings.Contains(err.Error(), "docker not available") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "docker not available")
	}
	if !strings.Contains(err.Error(), "preparing sandbox") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "preparing sandbox")
	}

	sb.mu.Lock()
	cleanCalls := sb.cleanupCalls
	sb.mu.Unlock()
	if cleanCalls != 0 {
		t.Errorf("Cleanup called %d times, want 0 (prepare failed)", cleanCalls)
	}
}

func TestRun_SandboxCleanupOnCancel(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-sandbox-cancel"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	sb := &mockSandbox{available: true}

	ctx, cancel := context.WithCancel(context.Background())
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
			}
			cancel()
			return &types.ExecuteResult{Output: "done" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false

	orch := New(Options{
		Config:   cfg,
		Provider: mock,
		WorkDir:  dir,
		Sandbox:  sb,
	})

	_ = orch.Run(ctx, project)

	sb.mu.Lock()
	cleanCalls := sb.cleanupCalls
	sb.mu.Unlock()

	if cleanCalls != 1 {
		t.Errorf("Cleanup called %d times, want 1 (should cleanup on cancel)", cleanCalls)
	}
}

func TestRun_SandboxEventsEmitted(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-sandbox-events"
	setupProject(t, dir, project, allPassedTasksMD)
	gitCommitAll(t, dir, "add tasks")

	sb := &mockSandbox{available: true}
	events := make(chan Event, 100)

	cfg := config.Default()
	cfg.Project.Name = project

	orch := New(Options{
		Config:   cfg,
		Provider: &mockProvider{},
		WorkDir:  dir,
		Events:   events,
		Sandbox:  sb,
	})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	var eventTypes []EventType
	for ev := range events {
		eventTypes = append(eventTypes, ev.Type)
	}

	if !containsEvent(eventTypes, EventSandboxPrepare) {
		t.Error("missing EventSandboxPrepare")
	}
	if !containsEvent(eventTypes, EventSandboxCleanup) {
		t.Error("missing EventSandboxCleanup")
	}

	prepIdx := firstEventIndex(eventTypes, EventSandboxPrepare)
	cleanIdx := firstEventIndex(eventTypes, EventSandboxCleanup)
	if prepIdx >= cleanIdx {
		t.Errorf("EventSandboxPrepare(%d) should come before EventSandboxCleanup(%d)", prepIdx, cleanIdx)
	}

	recoveryIdx := firstEventIndex(eventTypes, EventRecoveryCheck)
	if prepIdx >= recoveryIdx {
		t.Errorf("EventSandboxPrepare(%d) should come before EventRecoveryCheck(%d)", prepIdx, recoveryIdx)
	}
}
