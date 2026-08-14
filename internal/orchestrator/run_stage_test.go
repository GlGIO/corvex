package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

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

func TestRun_CommandLoop_PassesAfterRetries(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-loop-pass"
	// Command increments a counter and succeeds only once it reaches 2.
	yaml := "command: \"c=$(cat .ctr 2>/dev/null||echo 0);c=$((c+1));echo $c>.ctr;[ $c -ge 2 ]\"\nloop_max: 3\n"
	setupProject(t, dir, project, loopTaskMD(yaml))
	gitCommitAll(t, dir, "add loop task")

	events := make(chan Event, 100)
	go func() {
		for range events {
		}
	}()

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true

	orch := New(Options{Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events})
	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run() error = %v (loop should pass within 3 iterations)", err)
	}
	close(events)

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	if tasks[0].Status != types.StatusPassed {
		t.Errorf("S01 = %s, want PASSED (loop should succeed on iteration 2)", tasks[0].Status)
	}
}

func TestRun_CommandLoop_FailsAfterMax(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-loop-fail"
	setupProject(t, dir, project, loopTaskMD("command: \"false\"\nloop_max: 2\n"))
	gitCommitAll(t, dir, "add loop task")

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
		t.Fatal("expected failure when the loop never passes within max iterations")
	}

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	if tasks[0].Status != types.StatusFailed {
		t.Errorf("S01 = %s, want FAILED", tasks[0].Status)
	}
}
