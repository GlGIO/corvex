package orchestrator

// The wave barrier reads the control file. Two claims, and the second is the one
// that decides where the read point goes: a pause holds the NEXT wave, and it
// does not interrupt the step already in flight.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/types"
)

// TestPause_HoldsTheNextWaveWithoutInterruptingTheStepInFlight is the whole
// mechanism end to end, with the pause arriving from outside — written to disk
// while S01's provider call is open, exactly as a second `corvex run pause`
// would write it.
//
// What it proves, in order:
//
//  1. S01 finishes anyway. The step is a paid-for provider call and pausing must
//     never throw one away;
//  2. S02 does not start while the file is there. That is the barrier doing its
//     job — without the read point the run would simply walk on;
//  3. the run says `paused` out loud while it is held, so a reader is not left
//     with `alive` and nothing else;
//  4. clearing the file resumes it, and the run finishes both steps.
func TestPause_HoldsTheNextWaveWithoutInterruptingTheStepInFlight(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	const project, runID = "test-pause", "run_0f01"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add pause tasks")

	var mu sync.Mutex
	var executed []string
	var statuses []run.Status

	pausedOnDisk := make(chan struct{})
	reportedPaused := make(chan struct{})
	var onceDisk, onceStatus sync.Once

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "Looks good.\nVERDICT: PASS"}, nil
			}
			id := "S01"
			if strings.Contains(req.Prompt, "## Current Task: S02 ") {
				id = "S02"
			}
			mu.Lock()
			executed = append(executed, id)
			mu.Unlock()
			// The pause lands mid-step, once, from a process that is not this
			// run. If the read point were inside the step, this is where the
			// work would be lost — the assertion below is that S01 still
			// returns.
			onceDisk.Do(func() {
				if err := run.RequestPause(dir, runID, time.Now()); err != nil {
					t.Errorf("RequestPause: %v", err)
				}
				close(pausedOnDisk)
			})
			return &types.ExecuteResult{Output: "task completed" + taskReportBlock}, nil
		},
	}

	// The resumer stands in for the human: it waits until the run has actually
	// reported itself paused, checks that the next wave has not started, and only
	// then clears the file. Checking before the report would be checking nothing.
	go func() {
		select {
		case <-reportedPaused:
		case <-time.After(20 * time.Second):
			t.Error("the run never reported `paused`; the barrier did not read the control file")
			_ = run.ClearPause(dir, runID)
			return
		}
		mu.Lock()
		ran := append([]string(nil), executed...)
		mu.Unlock()
		for _, id := range ran {
			if id == "S02" {
				t.Error("S02 started while a pause was standing: the barrier let the next wave through")
			}
		}
		if err := run.ClearPause(dir, runID); err != nil {
			t.Errorf("ClearPause: %v", err)
		}
	}()

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.MaxRetries = 1

	orch := New(Options{
		Config: cfg, Provider: mock, WorkDir: dir,
		Repo:      dir,
		Identity:  activity.Identity{RunID: runID},
		PausePoll: 10 * time.Millisecond,
		SetRunStatus: func(s run.Status) error {
			mu.Lock()
			statuses = append(statuses, s)
			mu.Unlock()
			if s == run.StatusPaused {
				onceStatus.Do(func() { close(reportedPaused) })
			}
			return nil
		},
	})

	if err := orch.Run(context.Background(), project); err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-pausedOnDisk

	mu.Lock()
	ran := append([]string(nil), executed...)
	reported := append([]run.Status(nil), statuses...)
	mu.Unlock()

	if len(ran) != 2 || ran[0] != "S01" || ran[1] != "S02" {
		t.Fatalf("executed %v, want [S01 S02]: the run must finish both steps once resumed", ran)
	}
	if !containsStatus(reported, run.StatusPaused) {
		t.Errorf("statuses %v never included %q: a held run that reports nothing is `alive` and lying",
			reported, run.StatusPaused)
	}
	if !containsStatus(reported, run.StatusRunning) {
		t.Errorf("statuses %v never went back to %q after the resume", reported, run.StatusRunning)
	}
	if _, paused, _ := run.PauseRequested(dir, runID); paused {
		t.Error("the control file is still on disk after the resume")
	}
}

// TestPause_UnpausedRunIsNotSlowedDown is the negative control for the barrier:
// with no control file, the read point must be a no-op — not a wait, not a
// status write. Without it, "the run finished" would prove nothing about whether
// the file was ever consulted, because a barrier that always returns nil passes
// the positive test too.
func TestPause_UnpausedRunIsNotSlowedDown(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	const project, runID = "test-nopause", "run_0f02"
	setupProject(t, dir, project, testTasksMD)
	gitCommitAll(t, dir, "add tasks")

	var mu sync.Mutex
	var statuses []run.Status

	mock := &mockProvider{
		executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
			if strings.Contains(req.Prompt, "code reviewer") {
				return &types.ExecuteResult{Output: "Looks good.\nVERDICT: PASS"}, nil
			}
			return &types.ExecuteResult{Output: "task completed" + taskReportBlock}, nil
		},
	}

	cfg := config.Default()
	cfg.Project.Name = project
	cfg.Execution.MaxRetries = 1

	orch := New(Options{
		Config: cfg, Provider: mock, WorkDir: dir,
		Repo:     dir,
		Identity: activity.Identity{RunID: runID},
		// Long enough that a spurious pause would show up as a hang rather than
		// as a flake.
		PausePoll: 30 * time.Second,
		SetRunStatus: func(s run.Status) error {
			mu.Lock()
			statuses = append(statuses, s)
			mu.Unlock()
			return nil
		},
	})

	done := make(chan error, 1)
	go func() { done <- orch.Run(context.Background(), project) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the run hung with no control file on disk: the barrier is pausing on absence")
	}

	mu.Lock()
	reported := append([]run.Status(nil), statuses...)
	mu.Unlock()
	if containsStatus(reported, run.StatusPaused) {
		t.Errorf("statuses %v report %q for a run nobody paused", reported, run.StatusPaused)
	}
}

// TestPause_WithoutIdentityIsANoOp: a run with no repo and no id has no
// addressable control file, and the barrier must not go looking for one — the
// same refusal the gate makes rather than blocking on something no second
// process could answer.
func TestPause_WithoutIdentityIsANoOp(t *testing.T) {
	o := &Orchestrator{opts: Options{}}
	if err := o.waitWhilePausedOnDisk(context.Background()); err != nil {
		t.Fatalf("waitWhilePausedOnDisk on an unidentified run: %v", err)
	}
}

// TestPause_CancelledContextWinsOverAStandingPause: Ctrl-C on a paused run has
// to get out. A pause loop that only watches the file would hold the process
// until somebody found the file to delete.
func TestPause_CancelledContextWinsOverAStandingPause(t *testing.T) {
	dir := t.TempDir()
	const runID = "run_0f03"
	if err := run.RequestPause(dir, runID, time.Now()); err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	var mu sync.Mutex
	var statuses []run.Status
	o := &Orchestrator{opts: Options{
		Repo: dir, Identity: activity.Identity{RunID: runID}, PausePoll: time.Millisecond,
		SetRunStatus: func(s run.Status) error {
			mu.Lock()
			statuses = append(statuses, s)
			mu.Unlock()
			return nil
		},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	done := make(chan error, 1)
	go func() { done <- o.waitWhilePausedOnDisk(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("waitWhilePausedOnDisk returned nil on a cancelled context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled context did not break the pause wait")
	}

	mu.Lock()
	reported := append([]run.Status(nil), statuses...)
	mu.Unlock()
	// The one that matters: `running` must NOT be written on the way out. The
	// identity watcher has already recorded `canceling` for this run, and putting
	// `running` back on top of it resurrects exactly the lie LivenessCanceling
	// was built against — a run unwinding that reads as one that is working.
	if containsStatus(reported, run.StatusRunning) {
		t.Errorf("statuses %v put %q back after a cancel", reported, run.StatusRunning)
	}
	if !containsStatus(reported, run.StatusPaused) {
		t.Errorf("statuses %v never reported %q while it was held", reported, run.StatusPaused)
	}
}

func containsStatus(all []run.Status, want run.Status) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}
