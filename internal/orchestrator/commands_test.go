package orchestrator

import (
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func TestApplyCommand_PauseResume(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{skip: map[string]bool{}}

	o.applyCommand(Command{Type: CmdPause}, "", nil, nil)
	if !o.paused {
		t.Error("expected paused = true after CmdPause")
	}

	o.applyCommand(Command{Type: CmdResume}, "", nil, nil)
	if o.paused {
		t.Error("expected paused = false after CmdResume")
	}
}

func TestApplyCommand_Skip(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{skip: map[string]bool{}}

	o.applyCommand(Command{Type: CmdSkip, TaskID: "S03"}, "", nil, nil)
	if !o.skip["S03"] {
		t.Error("expected S03 in skip map")
	}
}

func TestApplyCommand_SkipRequiresTaskID(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{skip: map[string]bool{}}

	o.applyCommand(Command{Type: CmdSkip}, "", nil, nil)
	if len(o.skip) != 0 {
		t.Errorf("skip map should be empty when TaskID missing, got %v", o.skip)
	}
}

func TestApplyCommand_RetryResetsFailed(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir() + "/tasks.md"
	// Write a minimal tasks.md so UpdateTaskStatus doesn't crash on missing
	// path. The orchestrator tolerates failures here (logged as warnings),
	// so even an empty file is fine for this unit test.
	tasks := []types.Task{
		{ID: "S01", Status: types.StatusFailed},
	}
	completed := map[string]bool{"S01": true}
	o := &Orchestrator{skip: map[string]bool{}}

	o.applyCommand(Command{Type: CmdRetry, TaskID: "S01"}, tmp, tasks, completed)

	if tasks[0].Status != types.StatusPending {
		t.Errorf("task[0].Status = %q, want PENDING", tasks[0].Status)
	}
	if completed["S01"] {
		t.Error("expected S01 removed from completed map")
	}
}

func TestApplyCommand_RetryIgnoresNonFailed(t *testing.T) {
	t.Parallel()
	tasks := []types.Task{
		{ID: "S01", Status: types.StatusPassed},
	}
	completed := map[string]bool{"S01": true}
	o := &Orchestrator{skip: map[string]bool{}}

	o.applyCommand(Command{Type: CmdRetry, TaskID: "S01"}, "", tasks, completed)

	if tasks[0].Status != types.StatusPassed {
		t.Errorf("task[0].Status changed to %q; retry should ignore non-FAILED", tasks[0].Status)
	}
	if !completed["S01"] {
		t.Error("S01 should remain completed when retry is ignored")
	}
}
