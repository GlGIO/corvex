package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

type mockProvider struct {
	mu        sync.Mutex
	executeFn func(ctx context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error)
	calls     []types.ExecuteRequest
}

func (m *mockProvider) Execute(ctx context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
	m.mu.Lock()
	m.calls = append(m.calls, req)
	m.mu.Unlock()
	if m.executeFn != nil {
		return m.executeFn(ctx, req)
	}
	return &types.ExecuteResult{Output: ""}, nil
}

func (m *mockProvider) Stream(_ context.Context, _ types.ExecuteRequest) (<-chan types.StreamEvent, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockProvider) Name() string     { return "mock" }
func (m *mockProvider) Models() []string { return []string{"test-model"} }

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s: %v", args[1:], out, err)
		}
	}
}

const testTasksMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
  S02: [S01]
---

## S01 — First Task ⬜ PENDING

` + "```yaml\n" +
	`type: general
depends_on: []
max_turns: 10
` + "```\n" + `
### O que fazer
Do the first thing.

### Critérios de sucesso
- [ ] First criterion passes

### Arquivos
- **Criar:** ` + "`test-file-1.txt`" + `

---

## S02 — Second Task ⬜ PENDING

` + "```yaml\n" +
	`type: general
depends_on: [S01]
max_turns: 10
` + "```\n" + `
### O que fazer
Do the second thing.

### Critérios de sucesso
- [ ] Second criterion passes

### Arquivos
- **Criar:** ` + "`test-file-2.txt`" + `
`

const allPassedTasksMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
  S02: [S01]
---

## S01 — First Task ✅ PASSED

` + "```yaml\n" +
	`type: general
depends_on: []
max_turns: 10
` + "```\n" + `
### O que fazer
Do the first thing.

### Critérios de sucesso
- [ ] First criterion passes

---

## S02 — Second Task ✅ PASSED

` + "```yaml\n" +
	`type: general
depends_on: [S01]
max_turns: 10
` + "```\n" + `
### O que fazer
Do the second thing.

### Critérios de sucesso
- [ ] Second criterion passes
`

func setupProject(t *testing.T, dir, project, taskContent string) string {
	t.Helper()
	tasksDir := filepath.Join(dir, ".corvex", "tasks", project)
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tasksPath := filepath.Join(tasksDir, "tasks.md")
	if err := os.WriteFile(tasksPath, []byte(taskContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return tasksPath
}

func gitCommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "add", "-A"},
		{"git", "commit", "-m", msg, "--allow-empty"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args[1:], out, err)
		}
	}
}

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
			return &types.ExecuteResult{Output: "task completed"}, nil
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
			return &types.ExecuteResult{Output: "done"}, nil
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
			return &types.ExecuteResult{Output: "done"}, nil
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
			return &types.ExecuteResult{Output: "done"}, nil
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

// helpers

func hashFileContent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func containsEvent(events []EventType, target EventType) bool {
	for _, e := range events {
		if e == target {
			return true
		}
	}
	return false
}

func countEvents(events []EventType, target EventType) int {
	count := 0
	for _, e := range events {
		if e == target {
			count++
		}
	}
	return count
}

func firstEventIndex(events []EventType, target EventType) int {
	for i, e := range events {
		if e == target {
			return i
		}
	}
	return -1
}
