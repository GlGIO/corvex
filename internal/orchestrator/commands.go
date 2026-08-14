package orchestrator

import (
	"context"
	"fmt"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/types"
)

// CommandType identifies a runtime control message sent from the UI to the
// running orchestrator loop.
type CommandType string

const (
	// CmdPause asks the orchestrator to halt before starting the next task.
	// Already-running work continues to completion. Followed by CmdResume.
	CmdPause CommandType = "pause"
	// CmdResume clears a pause and lets the next ready task start.
	CmdResume CommandType = "resume"
	// CmdSkip marks a task as SKIPPED and excludes it from the run.
	// Currently honoured for tasks that have not yet started (PENDING).
	CmdSkip CommandType = "skip"
	// CmdRetry resets a FAILED task back to PENDING so the loop picks it up
	// again on the next iteration.
	CmdRetry CommandType = "retry"
)

// Command is the message envelope drained by the orchestrator between
// tasks. TaskID is required for skip/retry; it is ignored for pause/resume.
type Command struct {
	Type   CommandType
	TaskID string
}

// drainCommands processes any commands queued on o.commands without blocking.
// Pause flips a flag (consumed by waitWhilePaused); skip marks a task to be
// short-circuited; retry resets a FAILED task back to PENDING.
func (o *Orchestrator) drainCommands(ctx context.Context, s *schedule) {
	if o.commands == nil {
		return
	}
	for {
		select {
		case cmd, ok := <-o.commands:
			if !ok {
				o.commands = nil
				return
			}
			o.applyCommand(cmd, s.run.TasksPath, s.tasks, s.run.Completed)
		case <-ctx.Done():
			return
		default:
			return
		}
	}
}

// waitWhilePaused blocks until a CmdResume arrives or the context cancels.
// While paused, it still applies non-pause commands as they arrive.
func (o *Orchestrator) waitWhilePaused(ctx context.Context, s *schedule) error {
	if !o.isPaused() {
		return nil
	}
	o.emit(Event{Type: EventError, Message: "paused — press p to resume"})
	for o.isPaused() {
		if o.commands == nil {
			return fmt.Errorf("paused but no command channel attached")
		}
		select {
		case cmd, ok := <-o.commands:
			if !ok {
				o.commands = nil
				return nil
			}
			o.applyCommand(cmd, s.run.TasksPath, s.tasks, s.run.Completed)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (o *Orchestrator) applyCommand(cmd Command, tasksPath string, tasks []types.Task, completed map[string]bool) {
	switch cmd.Type {
	case CmdPause:
		o.setPaused(true)
	case CmdResume:
		o.setPaused(false)
	case CmdSkip:
		if cmd.TaskID == "" {
			return
		}
		o.skip[cmd.TaskID] = true
	case CmdRetry:
		if cmd.TaskID == "" {
			return
		}
		for i := range tasks {
			if tasks[i].ID == cmd.TaskID && tasks[i].Status == types.StatusFailed {
				tasks[i].Status = types.StatusPending
				if err := o.book.SetStatus(tasksPath, cmd.TaskID, types.StatusPending); err != nil {
					charmbraceletlog.Warn("retry: updating task status", "task", cmd.TaskID, "err", err)
				}
				delete(completed, cmd.TaskID)
				return
			}
		}
	}
}

// paused state lives on the orchestrator alongside the skip map; both are
// guarded by the orchestrator goroutine because Run is the only writer.
func (o *Orchestrator) isPaused() bool { return o.paused }
func (o *Orchestrator) setPaused(p bool) {
	o.paused = p
	if p {
		o.emit(Event{Type: EventError, Message: "paused"})
	} else {
		o.emit(Event{Type: EventError, Message: "resumed"})
	}
}
