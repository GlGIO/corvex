package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

func TestRun_FullFlow(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-project"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	events := make(chan Event, 100)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "All good.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "task completed" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false

	orch := New(Options{
		Config:   cfg,
		Provider: mock,
		WorkDir:  dir,
		Events:   events,
	})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	var eventTypes []EventType
	for ev := range events {
		eventTypes = append(eventTypes, ev.Type)
	}

	if !containsEvent(eventTypes, EventRecoveryCheck) {
		t.Error("missing EventRecoveryCheck")
	}
	if !containsEvent(eventTypes, EventDAGResolved) {
		t.Error("missing EventDAGResolved")
	}
	if !containsEvent(eventTypes, EventDone) {
		t.Error("missing EventDone")
	}

	taskStarts := countEvents(eventTypes, EventTaskStart)
	if taskStarts != 2 {
		t.Errorf("expected 2 EventTaskStart, got %d", taskStarts)
	}

	mock.mu.Lock()
	callCount := len(mock.calls)
	mock.mu.Unlock()
	if callCount < 4 {
		t.Errorf("expected at least 4 provider calls (2 worker + 2 reviewer), got %d", callCount)
	}
}

func TestRun_AllTasksAlreadyPassed(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-passed"
	setupProject(t, dir, project, allPassedTasksMD)
	gitCommitAll(t, dir, "add passed tasks")

	events := make(chan Event, 100)
	mock := &mockProvider{}

	cfg := config.Default()
	cfg.Project.Name = project

	orch := New(Options{
		Config:   cfg,
		Provider: mock,
		WorkDir:  dir,
		Events:   events,
	})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	mock.mu.Lock()
	callCount := len(mock.calls)
	mock.mu.Unlock()
	if callCount != 0 {
		t.Errorf("expected 0 provider calls when all tasks passed, got %d", callCount)
	}

	var eventTypes []EventType
	for ev := range events {
		eventTypes = append(eventTypes, ev.Type)
	}
	if !containsEvent(eventTypes, EventDone) {
		t.Error("missing EventDone")
	}
	if containsEvent(eventTypes, EventTaskStart) {
		t.Error("should not have EventTaskStart when all tasks passed")
	}
}

func TestRun_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-cancel"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	ctx, cancel := context.WithCancel(context.Background())
	mock := &mockProvider{
		executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
			cancel()
			return &types.ExecuteResult{Output: "done\nVERDICT: PASS"}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false

	orch := New(Options{
		Config:   cfg,
		Provider: mock,
		WorkDir:  dir,
	})

	err := orch.Run(ctx, project)
	if err == nil {
		t.Fatal("Run() expected context cancelled error, got nil")
	}
	if err != context.Canceled {
		if !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	}
}

func TestRun_PostRunHookFires(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-postrun"
	setupProject(t, dir, project, testTasksMD)
	// post-run hook writes a marker with the run status.
	hooksDir := filepath.Join(dir, ".corvex", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "postrun-marker.txt")
	script := "#!/bin/sh\necho \"status=$CORVEX_STATUS project=$CORVEX_PROJECT\" > " + marker + "\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "post-run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, dir, "add tasks + post-run hook")

	events := make(chan Event, 200)
	go func() {
		for range events {
		}
	}()
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "ok.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "done" + taskReportBlock}, nil
		},
	}
	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})
	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("post-run hook did not run (no marker): %v", err)
	}
	if !strings.Contains(string(data), "status=passed") {
		t.Errorf("post-run marker = %q, want status=passed", string(data))
	}
	if !strings.Contains(string(data), "project="+project) {
		t.Errorf("post-run marker = %q, want project=%s", string(data), project)
	}
}
