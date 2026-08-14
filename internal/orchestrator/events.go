// Package orchestrator schedules a run: it plans when the spec has drifted,
// resolves the task DAG, walks it wave by wave, honours pause/skip commands
// from the UI, and publishes progress events. Executing any single task —
// worker, review, command stage, human gate, escalation — belongs to
// internal/step.
package orchestrator

import (
	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/step"
)

// Event and EventType live in internal/event: both the scheduler here and the
// step executor emit them, and the renderers (cmd, internal/tui) consume them,
// so a leaf package is the only home that avoids an import cycle.
//
// These are aliases, not copies — `orchestrator.Event` and `event.Event` are
// the same type, so a consumer can reach it through either name.
type (
	Event     = event.Event
	EventType = event.Type
)

const (
	EventRecoveryCheck  = event.RecoveryCheck
	EventRecoveryResult = event.RecoveryResult
	EventPlanStart      = event.PlanStart
	EventPlanComplete   = event.PlanComplete
	EventDAGResolved    = event.DAGResolved
	EventTaskStart      = event.TaskStart
	EventTaskStream     = event.TaskStream
	EventTaskWarn       = event.TaskWarn
	EventTaskTimeout    = event.TaskTimeout
	EventHumanGate      = event.HumanGate
	EventTaskComplete   = event.TaskComplete
	EventReviewStart    = event.ReviewStart
	EventReviewResult   = event.ReviewResult
	EventCheckpoint     = event.Checkpoint
	EventRetry          = event.Retry
	EventError          = event.Error
	EventDone           = event.Done
	EventSandboxPrepare = event.SandboxPrepare
	EventSandboxCleanup = event.SandboxCleanup
	EventInsight        = event.Insight
)

// ReviewVerdict and its constants are re-exported from internal/step, which
// owns the reviewer that produces them. The Validator reports one, and
// `corvex validate` compares against it.
type ReviewVerdict = step.ReviewVerdict

const (
	VerdictPass          = step.VerdictPass
	VerdictFail          = step.VerdictFail
	VerdictIndeterminate = step.VerdictIndeterminate
)
