package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

func TestRun_PlanningNeeded(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-plan"

	projDir := filepath.Join(dir, ".corvex", "tasks", project)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(projDir, "spec.md")
	if err := os.WriteFile(specPath, []byte("Build a CLI tool"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, dir, "add spec")

	events := make(chan Event, 100)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "project planner") {
				return &types.ExecuteResult{Output: allPassedTasksMD}, nil
			}
			return &types.ExecuteResult{Output: "done" + taskReportBlock}, nil
		},
	}

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

	var eventTypes []EventType
	for ev := range events {
		eventTypes = append(eventTypes, ev.Type)
	}

	if !containsEvent(eventTypes, EventPlanStart) {
		t.Error("missing EventPlanStart — planner should have been called")
	}
	if !containsEvent(eventTypes, EventPlanComplete) {
		t.Error("missing EventPlanComplete")
	}
}

func TestRun_NoPlanningNeeded(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-noplan"

	projDir := filepath.Join(dir, ".corvex", "tasks", project)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "tasks.md"), []byte(allPassedTasksMD), 0o644); err != nil {
		t.Fatal(err)
	}

	specContent := "Build a CLI tool"
	specPath := filepath.Join(projDir, "spec.md")
	if err := os.WriteFile(specPath, []byte(specContent), 0o644); err != nil {
		t.Fatal(err)
	}

	specHash, err := hashFileContent(specPath)
	if err != nil {
		t.Fatal(err)
	}

	anchorContent := fmt.Sprintf("project: %s\nspec_hash: %s\n", project, specHash)
	if err := os.WriteFile(filepath.Join(projDir, "anchor.yaml"), []byte(anchorContent), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, dir, "add files")

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

	var eventTypes []EventType
	for ev := range events {
		eventTypes = append(eventTypes, ev.Type)
	}

	if containsEvent(eventTypes, EventPlanStart) {
		t.Error("should not have EventPlanStart when spec hash matches")
	}
}

func TestRun_EventsEmitted(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-events"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	events := make(chan Event, 100)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
			}
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

	requiredEvents := []EventType{
		EventRecoveryCheck,
		EventDAGResolved,
		EventTaskStart,
		EventReviewStart,
		EventReviewResult,
		EventTaskComplete,
		EventDone,
	}
	for _, required := range requiredEvents {
		if !containsEvent(eventTypes, required) {
			t.Errorf("missing required event %q in sequence %v", required, eventTypes)
		}
	}

	recoveryIdx := firstEventIndex(eventTypes, EventRecoveryCheck)
	dagIdx := firstEventIndex(eventTypes, EventDAGResolved)
	taskStartIdx := firstEventIndex(eventTypes, EventTaskStart)
	doneIdx := firstEventIndex(eventTypes, EventDone)

	if !(recoveryIdx < dagIdx && dagIdx < taskStartIdx && taskStartIdx < doneIdx) {
		t.Errorf("events not in expected order: recovery(%d) < dag(%d) < task_start(%d) < done(%d)",
			recoveryIdx, dagIdx, taskStartIdx, doneIdx)
	}
}

func TestProjectPaths(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	orch := New(Options{
		Config:   cfg,
		Provider: &mockProvider{},
		WorkDir:  "/workspace",
	})

	spec, tasks, anchor := orch.projectPaths("myproject")

	wantSpec := filepath.Join("/workspace", ".corvex", "tasks", "myproject", "spec.md")
	wantTasks := filepath.Join("/workspace", ".corvex", "tasks", "myproject", "tasks.md")
	wantAnchor := filepath.Join("/workspace", ".corvex", "tasks", "myproject", "anchor.yaml")

	if spec != wantSpec {
		t.Errorf("specPath = %q, want %q", spec, wantSpec)
	}
	if tasks != wantTasks {
		t.Errorf("tasksPath = %q, want %q", tasks, wantTasks)
	}
	if anchor != wantAnchor {
		t.Errorf("anchorPath = %q, want %q", anchor, wantAnchor)
	}
}

func TestNeedsPlanning_NoSpec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := config.Default()
	orch := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir})

	needs, err := orch.needsPlanning(
		filepath.Join(dir, "nonexistent.md"),
		filepath.Join(dir, "tasks.md"),
		types.AnchorState{},
	)
	if err != nil {
		t.Fatalf("needsPlanning() error = %v", err)
	}
	if needs {
		t.Error("needsPlanning() = true, want false when spec doesn't exist")
	}
}

func TestNeedsPlanning_NoTasks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(specPath, []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	orch := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir})

	needs, err := orch.needsPlanning(specPath, filepath.Join(dir, "tasks.md"), types.AnchorState{})
	if err != nil {
		t.Fatalf("needsPlanning() error = %v", err)
	}
	if !needs {
		t.Error("needsPlanning() = false, want true when tasks.md doesn't exist")
	}
}

func TestNeedsPlanning_HashMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	tasksPath := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(specPath, []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tasksPath, []byte("tasks"), 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := hashFileContent(specPath)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	orch := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir})

	needs, err := orch.needsPlanning(specPath, tasksPath, types.AnchorState{SpecHash: hash})
	if err != nil {
		t.Fatalf("needsPlanning() error = %v", err)
	}
	if needs {
		t.Error("needsPlanning() = true, want false when hash matches")
	}
}

func TestNeedsPlanning_HashMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.md")
	tasksPath := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(specPath, []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tasksPath, []byte("tasks"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	orch := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir})

	needs, err := orch.needsPlanning(specPath, tasksPath, types.AnchorState{SpecHash: "oldhash"})
	if err != nil {
		t.Fatalf("needsPlanning() error = %v", err)
	}
	if !needs {
		t.Error("needsPlanning() = false, want true when hash mismatches")
	}
}

func TestEmit_NilChannel(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	orch := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: "/tmp"})
	orch.events = nil

	orch.emit(Event{Type: EventDone})
}
