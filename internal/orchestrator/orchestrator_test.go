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

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// taskReportBlock is appended to mock Worker outputs so they satisfy the
// TASK-REPORT/HANDOFF contract enforced by executeTask (CH-03).
const taskReportBlock = "\n\nTASK-REPORT:\nSUMMARY: implemented the task.\nDECISIONS:\n- took the straightforward approach\nHANDOFF: state is ready for the next task"

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
` + "```\n" + `
### O que fazer
Do the second thing.

### Critérios de sucesso
- [ ] Second criterion passes

### Arquivos
- **Criar:** ` + "`test-file-2.txt`" + `
`

// dagViolationTasksMD models the show-lqip-buffer corruption: S02 is PASSED but
// depends on S01 which is still PENDING. The executor must refuse to run.
const dagViolationTasksMD = `---
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
` + "```\n" + `
### O que fazer
Do the first thing.

---

## S02 — Second Task ✅ PASSED

` + "```yaml\n" +
	`type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
Do the second thing.
`

// runningOnResumeTasksMD models an interrupted previous run: S01 was left in
// RUNNING. The executor must reset it to PENDING and re-pick it.
const runningOnResumeTasksMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
---

## S01 — First Task 🔄 RUNNING

` + "```yaml\n" +
	`type: general
depends_on: []
` + "```\n" + `
### O que fazer
Do the first thing.

### Critérios de sucesso
- [ ] First criterion passes

### Arquivos
- **Criar:** ` + "`test-file-1.txt`" + `
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

// diamondTasksMD models a diamond DAG: S01 → {S02, S03} → S04. S02 and S03 are
// independent of each other; S04 depends on both. Used to prove that a failure
// in S02 skips only S04 (its dependent) while S03 still runs.
const diamondTasksMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
  S02: [S01]
  S03: [S01]
  S04: [S02, S03]
---

## S01 — Root ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: []
` + "```\n" + `
### O que fazer
Root task.

---

## S02 — Left (will fail) ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
Left branch.

---

## S03 — Right (independent) ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
Right branch.

---

## S04 — Join (depends on both) ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S02, S03]
` + "```\n" + `
### O que fazer
Join branch.
`

func TestRun_DependencyFailureSkipsDependentsContinuesIndependent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-diamond"
	setupProject(t, dir, project, diamondTasksMD)
	gitCommitAll(t, dir, "add diamond tasks")

	events := make(chan Event, 200)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				// S02 always fails review; everything else passes.
				if strings.Contains(req.Prompt, "## Task: S02 ") {
					return &types.ExecuteResult{Output: "Broken.\nCATEGORY: incomplete\nVERDICT: FAIL"}, nil
				}
				return &types.ExecuteResult{Output: "Looks good.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "task completed" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	// AutoCommit on: each PASS checkpoints, so the retry-time recovery reset
	// can't revert prior tasks' committed statuses.
	cfg.Execution.AutoCommit = true
	cfg.Execution.MaxRetries = 1

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})

	err := orch.Run(context.Background(), project)
	close(events)

	if err == nil {
		t.Fatal("expected a summary failure error, got nil")
	}
	if !strings.Contains(err.Error(), "S02") {
		t.Errorf("error %q should name the failed task S02", err)
	}
	if !strings.Contains(err.Error(), "S04") {
		t.Errorf("error %q should name the skipped dependent S04", err)
	}

	// Independent branch S03 must have run; dependent S04 must have been skipped.
	var s03Passed, s04Skipped bool
	for ev := range events {
		if ev.Type == EventTaskComplete && ev.TaskID == "S03" && ev.Status == types.StatusPassed {
			s03Passed = true
		}
		if ev.Type == EventTaskComplete && ev.TaskID == "S04" && ev.Status == types.StatusSkipped {
			s04Skipped = true
		}
	}
	if !s03Passed {
		t.Error("independent task S03 should have run to PASSED despite S02 failing")
	}
	if !s04Skipped {
		t.Error("S04 (depends on failed S02) should have been SKIPPED")
	}

	// Final on-disk statuses.
	tasks, _, perr := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	if perr != nil {
		t.Fatalf("parse tasks: %v", perr)
	}
	statuses := map[string]types.TaskStatus{}
	for _, tk := range tasks {
		statuses[tk.ID] = tk.Status
	}
	if statuses["S01"] != types.StatusPassed {
		t.Errorf("S01 = %s, want PASSED", statuses["S01"])
	}
	if statuses["S02"] != types.StatusFailed {
		t.Errorf("S02 = %s, want FAILED", statuses["S02"])
	}
	if statuses["S03"] != types.StatusPassed {
		t.Errorf("S03 = %s, want PASSED", statuses["S03"])
	}
	if statuses["S04"] != types.StatusSkipped {
		t.Errorf("S04 = %s, want SKIPPED", statuses["S04"])
	}
}

func TestRun_CostCeilingAbortsImmediately(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-cost-abort"
	setupProject(t, dir, project, testTasksMD) // S01 → S02
	gitCommitAll(t, dir, "add tasks")

	events := make(chan Event, 100)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "All good.\nVERDICT: PASS"}, nil
			}
			// Worker burns more than the ceiling on the very first task.
			return &types.ExecuteResult{Output: "done" + taskReportBlock, CostUSD: 10}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false
	cfg.Execution.MaxCostUSD = 5 // first task's $10 blows this

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})

	err := orch.Run(context.Background(), project)
	close(events)

	if err == nil {
		t.Fatal("expected cost-ceiling abort error, got nil")
	}
	if !strings.Contains(err.Error(), "ceiling") {
		t.Errorf("error %q should mention the cost ceiling", err)
	}
	// A fatal cost abort must NOT be downgraded to the skippable-failure summary.
	if strings.Contains(err.Error(), "run completed with failures") {
		t.Errorf("cost ceiling breach was swallowed as a task failure: %q", err)
	}

	// Only S01 should have been attempted before the abort.
	for ev := range events {
		if ev.Type == EventTaskStart && ev.TaskID == "S02" {
			t.Error("S02 should never start after a cost-ceiling abort on S01")
		}
	}
}

// fanOutTasksMD: S01 root, then S02/S03/S04 all depend only on S01 — a single
// wide level that exercises the parallel scheduler.
const fanOutTasksMD = `---
generated_by: test
dag:
  S01: []
  S02: [S01]
  S03: [S01]
  S04: [S01]
---

## S01 — Root ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: []
` + "```\n" + `
### O que fazer
Root.

---

## S02 — A ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
A.

---

## S03 — B ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
B.

---

## S04 — C ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
C.
`

func TestRun_ParallelExecution(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-parallel"
	setupProject(t, dir, project, fanOutTasksMD)
	gitCommitAll(t, dir, "add fan-out tasks")

	events := make(chan Event, 500)
	go func() { // drain so emit never blocks
		for range events {
		}
	}()

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "Good.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "done" + taskReportBlock, CostUSD: 0.01}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true
	cfg.Execution.Parallel = true
	cfg.Execution.MaxParallel = 3

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})
	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("parallel Run() error = %v", err)
	}
	close(events)

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	for _, tk := range tasks {
		if tk.Status != types.StatusPassed {
			t.Errorf("task %s = %s, want PASSED", tk.ID, tk.Status)
		}
	}
}

func commandTasksMD(cmd1, cmd2 string) string {
	return "---\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
		"## S01 — Gate ⬜ PENDING\n\n```yaml\ntype: general\nkind: command\ncommand: " + cmd1 + "\n```\n\n### O que fazer\ngate\n\n---\n\n" +
		"## S02 — After ⬜ PENDING\n\n```yaml\ntype: general\nkind: command\ncommand: " + cmd2 + "\ndepends_on: [S01]\n```\n\n### O que fazer\nafter\n"
}

func TestRun_CommandStage_Passes(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-cmd-pass"
	setupProject(t, dir, project, commandTasksMD(`"true"`, `"true"`))
	gitCommitAll(t, dir, "add cmd tasks")

	events := make(chan Event, 100)
	go func() {
		for range events {
		}
	}()
	mock := &mockProvider{} // must NOT be called for command stages

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})
	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	mock.mu.Lock()
	calls := len(mock.calls)
	mock.mu.Unlock()
	if calls != 0 {
		t.Errorf("command stages must not call the LLM provider, got %d calls", calls)
	}

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	for _, tk := range tasks {
		if tk.Status != types.StatusPassed {
			t.Errorf("task %s = %s, want PASSED", tk.ID, tk.Status)
		}
	}
}

func TestRun_CommandStage_FailsAndSkipsDependents(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-cmd-fail"
	setupProject(t, dir, project, commandTasksMD(`"exit 1"`, `"true"`))
	gitCommitAll(t, dir, "add cmd tasks")

	events := make(chan Event, 100)
	go func() {
		for range events {
		}
	}()

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true

	orch := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events})
	err := orch.Run(context.Background(), project)
	close(events)
	if err == nil {
		t.Fatal("expected failure when a command stage exits non-zero")
	}

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	st := map[string]types.TaskStatus{}
	for _, tk := range tasks {
		st[tk.ID] = tk.Status
	}
	if st["S01"] != types.StatusFailed {
		t.Errorf("S01 = %s, want FAILED", st["S01"])
	}
	if st["S02"] != types.StatusSkipped {
		t.Errorf("S02 = %s, want SKIPPED (depends on failed S01)", st["S02"])
	}
}

func gateTasksMD() string {
	return "---\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
		"## S01 — Approve release ⬜ PENDING\n\n```yaml\ntype: general\nkind: human-gate\n```\n\n### O que fazer\ngate\n\n---\n\n" +
		"## S02 — Ship ⬜ PENDING\n\n```yaml\ntype: general\nkind: command\ncommand: \"true\"\ndepends_on: [S01]\n```\n\n### O que fazer\nship\n"
}

func TestRun_HumanGate_StopsWithoutApproval(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-gate-stop"
	setupProject(t, dir, project, gateTasksMD())
	gitCommitAll(t, dir, "add gate tasks")

	events := make(chan Event, 100)
	go func() {
		for range events {
		}
	}()

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true

	orch := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events})
	err := orch.Run(context.Background(), project)
	close(events)
	if err == nil || !strings.Contains(err.Error(), "human-gate") {
		t.Fatalf("expected a human-gate stop error, got %v", err)
	}

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	st := map[string]types.TaskStatus{}
	for _, tk := range tasks {
		st[tk.ID] = tk.Status
	}
	// Gate stays PENDING (so a re-run with --approve-gates proceeds); S02 never ran.
	if st["S01"] != types.StatusPending {
		t.Errorf("gate S01 = %s, want PENDING (left for re-run)", st["S01"])
	}
	if st["S02"] != types.StatusPending {
		t.Errorf("S02 = %s, want PENDING (gate not passed)", st["S02"])
	}
}

func TestRun_HumanGate_ApprovedProceeds(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-gate-go"
	setupProject(t, dir, project, gateTasksMD())
	gitCommitAll(t, dir, "add gate tasks")

	events := make(chan Event, 100)
	go func() {
		for range events {
		}
	}()

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true

	orch := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events, ApproveGates: true})
	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() with ApproveGates error = %v", err)
	}
	close(events)

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	for _, tk := range tasks {
		if tk.Status != types.StatusPassed {
			t.Errorf("task %s = %s, want PASSED (gate approved)", tk.ID, tk.Status)
		}
	}
}

func TestRun_MissingHandoffFailsTask(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-handoff-missing"
	setupProject(t, dir, project, testTasksMD) // S01 → S02
	gitCommitAll(t, dir, "add tasks")

	events := make(chan Event, 200)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "All good.\nVERDICT: PASS"}, nil
			}
			// Worker passes review but never emits a TASK-REPORT/HANDOFF.
			return &types.ExecuteResult{Output: "I finished the task."}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir, Events: events})
	err := orch.Run(context.Background(), project)
	close(events)

	if err == nil {
		t.Fatal("expected failure when the worker never emits a TASK-REPORT, got nil")
	}
	if !strings.Contains(err.Error(), "TASK-REPORT") {
		t.Errorf("error %q should mention the missing TASK-REPORT", err)
	}

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	for _, tk := range tasks {
		if tk.ID == "S01" && tk.Status != types.StatusFailed {
			t.Errorf("S01 = %s, want FAILED (no handoff)", tk.Status)
		}
	}
}

func TestRun_HandoffPopulatesAnchor(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-handoff-anchor"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	events := make(chan Event, 200)
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "Good.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "TASK-REPORT:\nSUMMARY: built it.\nDECISIONS:\n- chose approach A\nHANDOFF: call NewThing() to reuse this."}, nil
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

	state, err := anchor.Load(filepath.Join(dir, ".corvex", "tasks", project, "anchor.yaml"))
	if err != nil {
		t.Fatalf("loading anchor: %v", err)
	}
	if len(state.Completed) == 0 {
		t.Fatal("anchor has no completed tasks")
	}
	first := state.Completed[0]
	if first.Summary != "built it." {
		t.Errorf("Summary = %q, want the worker's report summary", first.Summary)
	}
	if len(first.Decisions) == 0 || first.Decisions[0] != "chose approach A" {
		t.Errorf("Decisions = %v, want the report's decisions", first.Decisions)
	}
	// After S01 the next task is S02, so its handoff context must be recorded.
	if !strings.Contains(state.NextTaskContext, "NewThing") {
		t.Errorf("NextTaskContext = %q, want the worker's HANDOFF", state.NextTaskContext)
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

func TestRun_DAGIntegrityViolationAborts(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-dag-violation"
	setupProject(t, dir, project, dagViolationTasksMD)
	gitCommitAll(t, dir, "add tasks with stale-dep PASSED")

	events := make(chan Event, 100)
	mock := &mockProvider{}
	cfg := config.Default()
	cfg.Project.Name = project

	orch := New(Options{
		Config: cfg, Provider: mock, WorkDir: dir, Events: events,
	})

	err := orch.Run(context.Background(), project)
	close(events)

	if err == nil {
		t.Fatal("expected DAG integrity error, got nil")
	}
	if !strings.Contains(err.Error(), "DAG integrity violation") {
		t.Errorf("error %q missing 'DAG integrity violation'", err)
	}
	if !strings.Contains(err.Error(), "S02") || !strings.Contains(err.Error(), "S01") {
		t.Errorf("error %q should name both offending task and dep", err)
	}

	mock.mu.Lock()
	callCount := len(mock.calls)
	mock.mu.Unlock()
	if callCount != 0 {
		t.Errorf("expected 0 provider calls on integrity failure, got %d", callCount)
	}
}

func TestRun_ResetsRunningTaskOnResume(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-running-reset"
	tasksPath := setupProject(t, dir, project, runningOnResumeTasksMD)
	gitCommitAll(t, dir, "add interrupted task")

	events := make(chan Event, 200)
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
		Config: cfg, Provider: mock, WorkDir: dir, Events: events,
	})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(events)

	// Provider should have been called for S01 (worker + review = at least 1).
	mock.mu.Lock()
	callCount := len(mock.calls)
	mock.mu.Unlock()
	if callCount == 0 {
		t.Error("expected provider calls (S01 should re-run after RUNNING reset), got 0")
	}

	// Final state in tasks.md: S01 must be PASSED, not RUNNING.
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		t.Fatalf("parsing final tasks.md: %v", err)
	}
	var s01 *types.Task
	for i := range tasks {
		if tasks[i].ID == "S01" {
			s01 = &tasks[i]
		}
	}
	if s01 == nil {
		t.Fatal("S01 missing from final tasks.md")
	}
	if s01.Status != types.StatusPassed {
		t.Errorf("S01 final status = %q, want PASSED (RUNNING should have been reset and re-run)", s01.Status)
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

func TestRun_FailedAttemptsCostTriggersAbort(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-cost-abort"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	// Worker returns success with $0.02; reviewer returns FAIL with $0.02.
	// Per-attempt cost = $0.04. After attempt 1 total = $0.08 > $0.05 ceiling.
	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "VERDICT: FAIL", CostUSD: 0.02}, nil
			}
			return &types.ExecuteResult{Output: "task work done", CostUSD: 0.02}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = false
	cfg.Execution.MaxCostUSD = 0.05
	cfg.Execution.MaxRetries = 5

	orch := New(Options{
		Config:   cfg,
		Provider: mock,
		WorkDir:  dir,
	})

	err := orch.Run(context.Background(), project)
	if err == nil {
		t.Fatal("expected cost ceiling error, got nil")
	}
	if !strings.Contains(err.Error(), "run aborted: cumulative cost") {
		t.Errorf("error %q should contain 'run aborted: cumulative cost'", err.Error())
	}
}

const singleTaskMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
---

## S01 — Only Task ⬜ PENDING

` + "```yaml\n" +
	`type: general
depends_on: []
` + "```\n" + `
### O que fazer
Do the thing.

### Critérios de sucesso
- [ ] Criterion passes

### Arquivos
- **Criar:** ` + "`out.txt`" + `
`

func TestRun_IndeterminateVerdictRetriesAndEventuallyFails(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-indeterminate-fail"
	setupProject(t, dir, project, singleTaskMD)
	gitCommitAll(t, dir, "add task")

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				// return empty output — no verdict line → VerdictIndeterminate
				return &types.ExecuteResult{Output: "I looked at the code."}, nil
			}
			return &types.ExecuteResult{Output: "task done" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.MaxRetries = 1
	cfg.Execution.AutoCommit = false

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir})

	err := orch.Run(context.Background(), project)
	if err == nil {
		t.Fatal("expected error when reviewer never produces a verdict, got nil")
	}
	if !strings.Contains(err.Error(), "reviewer never produced a verdict") {
		t.Errorf("error %q should contain 'reviewer never produced a verdict'", err.Error())
	}
}

func TestRun_IndeterminateVerdictRetryDiagnosisPassedToWorker(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-indeterminate-retry"
	setupProject(t, dir, project, singleTaskMD)
	gitCommitAll(t, dir, "add task")

	var mu sync.Mutex
	var workerPrompts []string
	reviewerCall := 0

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				mu.Lock()
				n := reviewerCall
				reviewerCall++
				mu.Unlock()
				if n == 0 {
					return &types.ExecuteResult{Output: "I looked at the code."}, nil
				}
				return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
			}
			mu.Lock()
			workerPrompts = append(workerPrompts, req.Prompt)
			mu.Unlock()
			return &types.ExecuteResult{Output: "task done" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.MaxRetries = 2
	cfg.Execution.AutoCommit = false

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	mu.Lock()
	prompts := workerPrompts
	mu.Unlock()

	if len(prompts) < 2 {
		t.Fatalf("expected at least 2 worker calls (initial + retry), got %d", len(prompts))
	}
	if !strings.Contains(prompts[1], "parseable verdict") {
		t.Errorf("second worker prompt %q should contain 'parseable verdict' diagnosis", prompts[1])
	}
}

func TestRun_SpawnInvestigationProducesDiagnosisForNextWorker(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-spawn-investigation"
	setupProject(t, dir, project, singleTaskMD)
	gitCommitAll(t, dir, "add task")

	var mu sync.Mutex
	var workerPrompts []string
	reviewerCall := 0
	providerCall := 0

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			mu.Lock()
			providerCall++
			mu.Unlock()

			if strings.Contains(req.Prompt, "code reviewer") {
				mu.Lock()
				n := reviewerCall
				reviewerCall++
				mu.Unlock()
				if n == 0 {
					// First review: fail with a category to trigger spawn-investigation.
					// CATEGORY and summary must appear BEFORE VERDICT so parseVerdict
					// can find them (it searches lines[:verdictIdx]).
					return &types.ExecuteResult{
						Output: "missing error handling\nCATEGORY: wrong-approach\nVERDICT: FAIL",
					}, nil
				}
				// Second review: pass.
				return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
			}

			if strings.Contains(req.Prompt, "investigator") {
				// Investigation step: return a concrete diagnosis.
				return &types.ExecuteResult{
					Output: "root-cause: the function lacks nil checks; fix: add nil guard at line 10",
				}, nil
			}

			// Worker call.
			mu.Lock()
			workerPrompts = append(workerPrompts, req.Prompt)
			mu.Unlock()
			return &types.ExecuteResult{Output: "task done" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.MaxRetries = 2
	cfg.Execution.AutoCommit = false
	cfg.Review.Escalation = map[string]config.EscalationPolicy{
		"wrong-approach": {After: 1, Action: ActionSpawnInvestigation},
	}

	orch := New(Options{Config: cfg, Provider: mock, WorkDir: dir})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	mu.Lock()
	prompts := workerPrompts
	mu.Unlock()

	if len(prompts) < 2 {
		t.Fatalf("expected at least 2 worker calls (initial + post-investigation retry), got %d", len(prompts))
	}
	if !strings.Contains(prompts[1], "root-cause") {
		t.Errorf("second worker prompt %q should contain investigation diagnosis 'root-cause'", prompts[1])
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
