package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// waitForGate polls until the run under test has opened its gate file.
func waitForGate(t *testing.T, repo, runID, stepID string) gate.Pending {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		p, err := gate.Read(repo, runID, stepID)
		if err == nil {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("gate %s/%s never appeared on disk", runID, stepID)
	return gate.Pending{}
}

// TestRun_HumanGate_BlocksAndIsReleasedByAnotherDecider is the phase's core
// promise, at the orchestrator level: the run does not abort, it holds — and it
// holds until something that is not the run says yes.
//
// The decider here is another goroutine talking only through the filesystem, so
// nothing but the on-disk format connects the two halves. The end-to-end version
// with two real processes lives in e2e; this one is what makes the mechanism
// debuggable when it breaks.
func TestRun_HumanGate_BlocksAndIsReleasedByAnotherDecider(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-gate-block"
	setupProject(t, dir, project, gateTasksMD())
	gitCommitAll(t, dir, "add gate tasks")

	events := make(chan Event, 200)
	var seen []Event
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			mu.Lock()
			seen = append(seen, ev)
			mu.Unlock()
		}
	}()

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.AutoCommit = true

	const runID = "run_8f21"
	var statuses []run.Status
	var statusMu sync.Mutex

	orch := New(Options{
		Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events,
		Identity: activity.Identity{RunID: runID, Recipe: "gate-demo"},
		Repo:     dir,
		GatePoll: 5 * time.Millisecond,
		SetRunStatus: func(s run.Status) error {
			statusMu.Lock()
			defer statusMu.Unlock()
			statuses = append(statuses, s)
			return nil
		},
	})

	runDone := make(chan error, 1)
	go func() { runDone <- orch.Run(context.Background(), project) }()

	p := waitForGate(t, dir, runID, "S01")

	// The assertion that makes this test worth having: the run must still be
	// holding. Without it the test passes even for a gate that never waits —
	// the decider would find the file, approve it, and the run would already
	// have walked past. Measured: with a 5 ms poll, 250 ms is fifty chances to
	// notice a decision that has not been written.
	select {
	case err := <-runDone:
		t.Fatalf("the run finished without waiting for the gate: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	// The decider talks to the run only through the filesystem.
	if _, err := gate.Decide(dir, runID, "S01", gate.Decision{
		Verdict: gate.Approved, DecidedAt: time.Now().UTC(),
		Acked: gate.RequiredLabels(p.Evidence),
	}); err != nil {
		t.Fatalf("the decider failed: %v", err)
	}

	var err error
	select {
	case err = <-runDone:
	case <-time.After(15 * time.Second):
		t.Fatal("the run never noticed the approval")
	}
	close(events)
	<-done
	if err != nil {
		t.Fatalf("Run after approval = %v, want nil", err)
	}

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	st := map[string]types.TaskStatus{}
	for _, tk := range tasks {
		st[tk.ID] = tk.Status
	}
	if st["S01"] != types.StatusPassed {
		t.Errorf("gate S01 = %s, want PASSED after approval", st["S01"])
	}
	if st["S02"] != types.StatusPassed {
		t.Errorf("S02 = %s, want PASSED (the gate released it)", st["S02"])
	}

	// The run reported itself parked while it waited, and running again after.
	statusMu.Lock()
	defer statusMu.Unlock()
	if len(statuses) < 2 || statuses[0] != run.StatusParked || statuses[1] != run.StatusRunning {
		t.Errorf("status transitions = %v, want parked then running", statuses)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawPending, sawDecided bool
	for _, ev := range seen {
		switch ev.Type {
		case EventGatePending:
			sawPending = true
		case EventGateDecided:
			sawDecided = true
			if strings.Contains(ev.Message, "/") || strings.Contains(ev.Message, "Users") {
				t.Errorf("gate event message leaks a path: %q", ev.Message)
			}
		}
	}
	if !sawPending || !sawDecided {
		t.Errorf("ledger events: pending=%v decided=%v, want both", sawPending, sawDecided)
	}
}

// TestRun_HumanGate_RejectionFailsTheStep: rejecting is not a special case. It
// fails the step, and the scheduler's existing cascade skips its dependents.
func TestRun_HumanGate_RejectionFailsTheStep(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-gate-reject"
	setupProject(t, dir, project, gateTasksMD())
	gitCommitAll(t, dir, "add gate tasks")

	events := make(chan Event, 200)
	go func() {
		for range events {
		}
	}()

	cfg := config.Default()
	cfg.Project.Name = project

	const runID = "run_beef"
	orch := New(Options{
		Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events,
		Identity: activity.Identity{RunID: runID},
		Repo:     dir,
		GatePoll: 5 * time.Millisecond,
	})

	go func() {
		waitForGate(t, dir, runID, "S01")
		_, _ = gate.Decide(dir, runID, "S01", gate.Decision{
			Verdict: gate.Rejected, DecidedAt: time.Now().UTC(), Reason: "migration is wrong",
		})
	}()

	err := orch.Run(context.Background(), project)
	close(events)
	if err == nil {
		t.Fatal("a rejected gate must fail the run")
	}

	tasks, _, _ := task.ParseTasksFile(filepath.Join(dir, ".corvex", "tasks", project, "tasks.md"))
	st := map[string]types.TaskStatus{}
	for _, tk := range tasks {
		st[tk.ID] = tk.Status
	}
	if st["S01"] != types.StatusFailed {
		t.Errorf("rejected gate S01 = %s, want FAILED", st["S01"])
	}
	if st["S02"] != types.StatusSkipped {
		t.Errorf("S02 = %s, want SKIPPED (it depended on the rejected step)", st["S02"])
	}
}

// TestRun_HumanGate_CancellationDoesNotHangForever: a parked run must still
// answer Ctrl-C. Without this the gate would be a way to make a run unkillable.
func TestRun_HumanGate_CancellationDoesNotHangForever(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	project := "test-gate-cancel"
	setupProject(t, dir, project, gateTasksMD())
	gitCommitAll(t, dir, "add gate tasks")

	events := make(chan Event, 200)
	go func() {
		for range events {
		}
	}()

	cfg := config.Default()
	cfg.Project.Name = project

	const runID = "run_cafe"
	orch := New(Options{
		Config: cfg, Provider: &mockProvider{}, WorkDir: dir, Events: events,
		Identity: activity.Identity{RunID: runID},
		Repo:     dir,
		GatePoll: 5 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitForGate(t, dir, runID, "S01")
		cancel()
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- orch.Run(ctx, project) }()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("a cancelled run must not report success")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a parked run ignored cancellation — the gate made it unkillable")
	}
	close(events)
}
