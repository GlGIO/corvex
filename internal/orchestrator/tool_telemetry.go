package orchestrator

import (
	"fmt"
	"sync"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/types"
)

// Tool telemetry: the only part of a worker's stream that reaches the ledger.
//
// emit() used to drop every EventTaskStream with one argument — per-token
// chunks explode disk and reading time. That argument is still right for text
// and still enforced here; it was wrong for tools. "Which tool is running right
// now" and "where did this task's wall clock go" are questions the UI (2d) and
// the cost-by-nature bar (2f) ask of the ledger, and nothing in the ledger could
// answer them: the events existed only in memory, for the TUI.
//
// So exactly two stream events become lines — the start of a tool call and its
// end — and nothing else. A `text` chunk is still dropped, which is the rule and
// not an omission: a single worker attempt emits thousands of them.
//
// The line's `type` is the stream event's own name (`tool_use` / `tool_result`)
// rather than `task_stream`, because start and end have to be distinguishable by
// grep alone. That is the property this file is sold on, and a shared
// `task_stream` type would force a reader to look at a second field to recover
// what the first one threw away. Neither value collides with an event.Type.
const (
	toolUseLineType    = string(types.EventToolUse)
	toolResultLineType = string(types.EventToolResult)
)

// maxToolLinesPerTask caps how much tool telemetry one task may put in the
// committed file, and is the answer to "the package comment of internal/activity
// promises ~200-500 entries per 30-task run".
//
// Arithmetic, measured (see TestToolLine_StaysWithinDiskBudget, which fails if a
// line grows past 160 bytes): a tool line marshals to ~110-130 bytes — ts,
// type, run_id, task_id, tool, and duration_ms on the closing line. At this cap a
// 30-task run tops out at 30*200 = 6000 tool lines ~ 780 KB, and a realistic
// task (10-30 tool calls => 20-60 lines) lands at ~230 KB. The previous ceiling
// was ~500 lines ~ 60 KB, so this is knowingly an order of magnitude more in a
// file that is committed — bounded, and bounded per run rather than per repo.
//
// A cap was chosen over sampling because sampling silently biases exactly the
// two consumers this data exists for: a sampled ledger cannot say what ran in a
// given second, and per-tool totals computed from it are wrong in a way no
// reader can detect. Truncation is wrong only after the cap, and says so on the
// line (see toolLine): the tail of a pathological task is lost, loudly.
//
// Debt: the package comment of internal/activity still advertises "~200-500
// entries per 30-task run (a few hundred KB)". With tool telemetry on, the first
// half of that sentence is wrong and the second half is right — it needs to read
// ~2000-6000 entries at a few hundred KB to a megabyte. That file is outside
// this change, so the correction is registered here rather than made.
//
// The cap counts LINES, not calls, so a task cannot dodge it by never closing
// its calls. It is per task and per run, and deliberately not reset by a retry:
// three attempts of the same task share one budget, or a retry loop would
// multiply the ceiling by MaxRetries.
const maxToolLinesPerTask = 200

// maxPendingToolsPerTask bounds the unmatched starts held in memory for one
// task. A start goes unmatched whenever a tool never reports an end — a
// cancelled attempt, a killed provider, or a command stage whose output was
// empty (command_stage.go only emits the result event for non-empty output). A
// long run would otherwise grow this slice forever.
//
// 16 is above any real depth (the provider reports one turn's calls before the
// next turn starts) and cheap to be wrong about: worst case is
// 512 tasks * 16 * ~40 bytes ~ 330 KB, and dropping the OLDEST start on overflow
// loses a duration, never a line.
const maxPendingToolsPerTask = 16

// pendingTool is one tool call waiting for its end: when it started and what it
// was called. The name is kept because the provider does not repeat it on the
// result event (protocol.go builds tool_result with Content only), and a closing
// line without a name cannot be grouped by tool.
type pendingTool struct {
	id   string
	name string
	at   time.Time
}

// toolTelemetry pairs the start of a tool call with its end so the closing line
// can carry a duration, and rations how much of that reaches the committed file.
// The zero value is ready to use.
//
// It is its own mutex rather than a piggyback on emitMu: emitMu serialises bytes
// going into the file, this serialises a decision taken before the bytes exist,
// and merging them would make the ledger write hold state it does not own.
type toolTelemetry struct {
	mu sync.Mutex
	// pending is task id -> unmatched starts, oldest first. Cleared when the
	// task completes, which is the lifecycle bound: whatever is unmatched by
	// then is never going to be matched.
	pending map[string][]pendingTool
	// written is task id -> tool lines already persisted for that task. It
	// survives task completion on purpose (see maxToolLinesPerTask) and costs
	// one int per task for the life of the run.
	written map[string]int
}

// start records the beginning of a tool call for a task.
//
// Pairing is per task and by arrival order (FIFO). That is exact for the only
// shape that exists today — one provider process per task, reporting a call and
// then its result — and it is the reason the state is keyed by task at all:
// tasks run in parallel, so a single global queue would pair task A's start with
// task B's end.
//
// The limitation is real and not hypothetical: one assistant turn can carry
// several tool_use blocks (protocol.go's parseAssistant loops over them), and if
// those finish out of order FIFO attributes the wrong duration to each call. The
// per-task total survives — sum(end_i) - sum(start_i) does not depend on the
// pairing — so the 2f bar stays honest while a single line's duration_ms may not
// be. The correct fix is to carry the provider's `tool_use_id`, which
// toolResultLine already parses and then discards because types.StreamEvent has
// nowhere to put it; both files are outside this change. Registered as debt.
func (tt *toolTelemetry) start(taskID, id, name string, at time.Time) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if tt.pending == nil {
		tt.pending = make(map[string][]pendingTool)
	}
	q := append(tt.pending[taskID], pendingTool{id: id, name: name, at: at})
	if len(q) > maxPendingToolsPerTask {
		// Drop the oldest: the newest start is the one whose end is still
		// plausibly coming, and an overflow means the old ones never closed.
		q = q[len(q)-maxPendingToolsPerTask:]
	}
	tt.pending[taskID] = q
}

// finish consumes the oldest unmatched start of a task and returns how long it
// took and what it was called. ok is false when no start is waiting — an end
// without a beginning (a resumed run, or a start dropped by the cap), which is
// recorded as a line with no duration rather than a fabricated zero.
func (tt *toolTelemetry) finish(taskID, id string, at time.Time) (durationMs int64, name string, ok bool) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	q := tt.pending[taskID]
	if len(q) == 0 {
		return 0, "", false
	}

	// Pair by the provider's own id when it is there, and only fall back to
	// arrival order when it is not. The fallback used to be the whole strategy,
	// with the debt written down as "the correct fix is to carry the
	// tool_use_id, which the parser reads and discards". It did not read it —
	// it ignored the line the id arrives on. Now that the parser sees it, one
	// assistant turn issuing several calls pairs exactly instead of plausibly.
	idx := 0
	if id != "" {
		idx = -1
		for i := range q {
			if q[i].id == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return 0, "", false
		}
	}
	first := q[idx]
	if len(q) == 1 {
		delete(tt.pending, taskID)
	} else {
		tt.pending[taskID] = append(append([]pendingTool(nil), q[:idx]...), q[idx+1:]...)
	}
	d := at.Sub(first.at).Milliseconds()
	if d < 0 {
		// Non-monotonic clocks (a wall-clock step during a run) must not put a
		// negative duration in a file people aggregate.
		d = 0
	}
	return d, first.name, true
}

// forget releases the unmatched starts of a finished task. The written budget is
// intentionally not released — see maxToolLinesPerTask.
func (tt *toolTelemetry) forget(taskID string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	delete(tt.pending, taskID)
}

// budget answers whether one more tool line may be written for a task.
// atCeiling is true exactly once per task: on the call that exhausts it, so the
// caller can leave a single mark in the file instead of going quiet.
func (tt *toolTelemetry) budget(taskID string) (allow, atCeiling bool) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if tt.written == nil {
		tt.written = make(map[string]int)
	}
	n := tt.written[taskID]
	switch {
	case n < maxToolLinesPerTask:
		tt.written[taskID] = n + 1
		return true, false
	case n == maxToolLinesPerTask:
		tt.written[taskID] = n + 1 // the truncation notice spends the last slot
		return false, true
	default:
		return false, false
	}
}

// toolLine turns a stream event into the ledger line for it, or reports that the
// event does not belong on disk.
//
// What must NOT be here is as load-bearing as what is: types.StreamEvent also
// carries Content (a summary of the tool's input) and File (the path it touched),
// and neither may reach activity.jsonl. That file is committed to the user's
// repository, a Read input is an absolute path under /Users/<username>, a Bash
// input is a command line, and either can hold a secret the user exported. This
// already cost a post-close correction in F1, when `repo` published the home
// directory of whoever ran it. The name alone is what a screen can act on; the
// arguments stay in the terminal, where they already are.
// TestToolLine_CarriesNameNeverInputOrPath fails if that ever changes.
func (o *Orchestrator) toolLine(ev Event) (activity.Entry, bool) {
	if ev.Stream == nil {
		return activity.Entry{}, false
	}
	switch ev.Stream.Type {
	case types.EventToolUse, types.EventToolResult:
	default:
		// Text and per-token chunks: dropped, deliberately. See the file comment.
		return activity.Entry{}, false
	}

	allow, atCeiling := o.tools.budget(ev.TaskID)
	if !allow {
		if !atCeiling {
			return activity.Entry{}, false
		}
		// One line saying the rest is missing. It reuses task_warn instead of
		// minting an on-disk type for a truncation, and carries a number so a
		// reader can tell a truncated task from a quiet one.
		return activity.Entry{
			Timestamp: ev.Timestamp,
			Type:      string(EventTaskWarn),
			TaskID:    ev.TaskID,
			Phase:     ev.Phase,
			Message:   fmt.Sprintf("tool telemetry truncated after %d lines for this task", maxToolLinesPerTask),
		}, true
	}

	e := activity.Entry{
		Timestamp: ev.Timestamp,
		TaskID:    ev.TaskID,
		// Phase is passed through, never inferred: a tool call inside a command
		// stage is not the worker phase, and guessing "worker" here would put a
		// wrong bucket in the 2f accounting for every command stage.
		Phase: ev.Phase,
		// The name may arrive on either field. The step emitters put it on the
		// stream event; Event.Tool exists for producers that fill the event
		// directly, and wins when set.
		Tool: ev.Tool,
	}
	if e.Tool == "" {
		e.Tool = ev.Stream.Tool
	}

	if ev.Stream.Type == types.EventToolUse {
		e.Type = toolUseLineType
		o.tools.start(ev.TaskID, ev.Stream.ID, e.Tool, ev.Timestamp)
		return e, true
	}

	e.Type = toolResultLineType
	if d, name, ok := o.tools.finish(ev.TaskID, ev.Stream.ID, ev.Timestamp); ok {
		e.DurationMs = d
		if e.Tool == "" {
			e.Tool = name
		}
	}
	return e, true
}

// ledgerEntry decides what, if anything, ev writes to the committed ledger.
//
// Everything that is not a stream event keeps the mapping it always had. A
// stream event goes through toolLine, which persists tool boundaries and drops
// the rest.
func (o *Orchestrator) ledgerEntry(ev Event) (activity.Entry, bool) {
	if ev.Type == EventTaskStream {
		return o.toolLine(ev)
	}
	if ev.Type == EventTaskComplete {
		// The task is done: anything still waiting for an end never gets one.
		o.tools.forget(ev.TaskID)
	}
	return ledgerEntryFromEvent(ev), true
}
