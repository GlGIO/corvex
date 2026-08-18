package step

import (
	"context"
	"fmt"
	charmbraceletlog "github.com/charmbracelet/log"
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
	stepTimeout string,
) {
	warnAt := time.Duration(e.cfg.Execution.TaskWarnMinutes) * time.Minute
	hardAt := e.hardCeiling(stepTimeout)
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
				// The watchdog only ever watches a worker attempt (its sole
				// caller is runWorker), so its warnings and kills are worker
				// lines: they describe how the worker's time was spent.
				e.emit(event.Event{
					Type:    event.TaskWarn,
					TaskID:  taskID,
					Phase:   event.PhaseWorker,
					Message: fmt.Sprintf("task running > %s — consider pausing if stuck", warnAt),
				})
			}

			if hardAt > 0 && elapsed >= hardAt {
				msg := fmt.Sprintf("task %s aborted: wall-clock timeout after %s (last activity: %s)",
					taskID, elapsed.Round(time.Second), describeLast(live))
				timedOut.set(msg)
				e.emit(event.Event{Type: event.TaskTimeout, TaskID: taskID, Phase: event.PhaseWorker, Message: msg})
				cancel()
				return
			}

			if idleAt > 0 && live.idleFor() >= idleAt {
				msg := fmt.Sprintf("task %s aborted: no provider output for %s (last activity: %s)",
					taskID, live.idleFor().Round(time.Second), describeLast(live))
				timedOut.set(msg)
				e.emit(event.Event{Type: event.TaskTimeout, TaskID: taskID, Phase: event.PhaseWorker, Message: msg})
				cancel()
				return
			}
		}
	}
}

// hardCeiling is the wall clock for one attempt: the step's own when it
// declared one, the run's otherwise.
//
// The run-wide value cannot be right for every step, and the first dogfood run
// of this repository proved it by killing a worker at 20 minutes while it was
// still working. A step that edits a package and a step that runs a suite have
// different time profiles, so the declaration belongs where the difference is.
//
// A malformed duration falls back to the run's ceiling rather than to "no
// ceiling": recipe validation already refuses it, so reaching here means
// something wrote tasks.md by hand — and the safe reading of a broken ceiling is
// the default one, never infinity.
func (e *Executor) hardCeiling(stepTimeout string) time.Duration {
	if stepTimeout != "" {
		if d, err := time.ParseDuration(stepTimeout); err == nil && d > 0 {
			return d
		}
		charmbraceletlog.Warn("unreadable step timeout; using the run's ceiling", "timeout", stepTimeout)
	}
	return time.Duration(e.cfg.Execution.TaskTimeoutMinutes) * time.Minute
}

func describeLast(live *liveness) string {
	if d := live.lastActivity(); d != "" {
		return d
	}
	return "none yet (provider produced no output)"
}

// streamSummary renders a one-line description of a stream event for the
// watchdog's "where it hung" diagnostic.
//
// The NAME of the tool and nothing else. This string ends up in the message of a
// `task_timeout` / `task_warn` line, and those lines go to activity.jsonl, which
// is committed — corvex's own auto_commit puts it in the user's git history.
// `se.Content` is the provider's summary of the tool INPUT: the absolute
// file_path for Read/Write/Edit, or the whole command line for Bash, which is
// where an exported credential travels. F5 closed that door on the `tool_use`
// line and left it open here; an adversarial audit walked through it with a
// canary and found the token in a commit.
//
// The detail is not lost, it is relocated: the full stream event still reaches
// the TUI and the run log, neither of which is versioned.
func streamSummary(se types.StreamEvent) string {
	switch se.Type {
	case types.EventToolUse:
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
