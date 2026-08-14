package step

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/types"
)

// liveness tracks the wall-clock start of a task attempt and the time of its
// most recent stream event, so the watchdog can distinguish a long-but-alive
// task from a hung one. It is safe for concurrent use: the worker's stream
// callback writes while the watchdog goroutine reads.
type liveness struct {
	mu       sync.Mutex
	start    time.Time
	last     time.Time
	lastDesc string
}

func newLiveness() *liveness {
	now := time.Now()
	return &liveness{start: now, last: now}
}

// touch records that the provider produced output, refreshing the idle clock.
func (l *liveness) touch(desc string) {
	l.mu.Lock()
	l.last = time.Now()
	if desc != "" {
		l.lastDesc = desc
	}
	l.mu.Unlock()
}

// idleFor reports how long since the last stream event.
func (l *liveness) idleFor() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Since(l.last)
}

// elapsed reports total time since the attempt started.
func (l *liveness) elapsed() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Since(l.start)
}

// lastActivity returns a short description of the most recent stream event,
// used to tell the user where a stalled task got stuck.
func (l *liveness) lastActivity() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastDesc
}

// timeoutFlag carries the watchdog's verdict back to executeTask so a
// cancelled attempt reports *why* it died instead of a bare "context
// canceled".
type timeoutFlag struct {
	mu  sync.Mutex
	msg string
}

func newTimeoutFlag() *timeoutFlag { return &timeoutFlag{} }

func (f *timeoutFlag) set(msg string) {
	f.mu.Lock()
	if f.msg == "" {
		f.msg = msg
	}
	f.mu.Unlock()
}

func (f *timeoutFlag) reason() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.msg
}

// watchTask runs for the lifetime of a single worker attempt. It emits a warn
// event once the task crosses task_warn_minutes, and cancels the attempt when
// it exceeds the wall-clock ceiling or goes idle past the stream-idle ceiling.
// It exits as soon as the attempt finishes (watchDone closed) or the context
// is cancelled.
func (e *Executor) watchTask(
	ctx context.Context,
	cancel context.CancelFunc,
	watchDone <-chan struct{},
	taskID string,
	live *liveness,
	timedOut *timeoutFlag,
	streaming bool,
) {
	warnAt := time.Duration(e.cfg.Execution.TaskWarnMinutes) * time.Minute
	hardAt := time.Duration(e.cfg.Execution.TaskTimeoutMinutes) * time.Minute
	idleAt := time.Duration(e.cfg.Execution.StreamIdleTimeoutSeconds) * time.Second
	if !streaming {
		idleAt = 0 // no per-chunk events to measure idleness against
	}

	warned := false
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-watchDone:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := live.elapsed()

			if warnAt > 0 && !warned && elapsed >= warnAt {
				warned = true
				e.emit(event.Event{
					Type:    event.TaskWarn,
					TaskID:  taskID,
					Message: fmt.Sprintf("task running > %s — consider pausing if stuck", warnAt),
				})
			}

			if hardAt > 0 && elapsed >= hardAt {
				msg := fmt.Sprintf("task %s aborted: wall-clock timeout after %s (last activity: %s)",
					taskID, elapsed.Round(time.Second), describeLast(live))
				timedOut.set(msg)
				e.emit(event.Event{Type: event.TaskTimeout, TaskID: taskID, Message: msg})
				cancel()
				return
			}

			if idleAt > 0 && live.idleFor() >= idleAt {
				msg := fmt.Sprintf("task %s aborted: no provider output for %s (last activity: %s)",
					taskID, live.idleFor().Round(time.Second), describeLast(live))
				timedOut.set(msg)
				e.emit(event.Event{Type: event.TaskTimeout, TaskID: taskID, Message: msg})
				cancel()
				return
			}
		}
	}
}

func describeLast(live *liveness) string {
	if d := live.lastActivity(); d != "" {
		return d
	}
	return "none yet (provider produced no output)"
}

// streamSummary renders a one-line description of a stream event for the
// watchdog's "where it hung" diagnostic.
func streamSummary(se types.StreamEvent) string {
	switch se.Type {
	case types.EventToolUse:
		if se.Content != "" {
			return fmt.Sprintf("tool %s (%s)", se.Tool, se.Content)
		}
		return fmt.Sprintf("tool %s", se.Tool)
	case types.EventToolResult:
		return "tool result"
	case types.EventText:
		return "assistant text"
	case types.EventError:
		return "error event"
	case types.EventDone:
		return "result line"
	default:
		return ""
	}
}
