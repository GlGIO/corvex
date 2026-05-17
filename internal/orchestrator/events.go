// Package orchestrator coordinates task planning, execution, review, and
// recovery into a single run loop driven by a DAG of tasks.
package orchestrator

import (
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

// EventType classifies orchestration events for TUI consumption.
type EventType string

const (
	EventRecoveryCheck  EventType = "recovery_check"
	EventRecoveryResult EventType = "recovery_result"
	EventPlanStart      EventType = "plan_start"
	EventPlanComplete   EventType = "plan_complete"
	EventDAGResolved    EventType = "dag_resolved"
	EventTaskStart      EventType = "task_start"
	EventTaskStream     EventType = "task_stream"
	EventTaskComplete   EventType = "task_complete"
	EventReviewStart    EventType = "review_start"
	EventReviewResult   EventType = "review_result"
	EventCheckpoint     EventType = "checkpoint"
	EventRetry          EventType = "retry"
	EventError          EventType = "error"
	EventDone           EventType = "done"
	EventSandboxPrepare EventType = "sandbox_prepare"
	EventSandboxCleanup EventType = "sandbox_cleanup"
	EventInsight        EventType = "insight"
)

// InsightData carries an agent-creation suggestion from the Advisor.
type InsightData struct {
	TaskType         string
	Count            int
	SuggestedPath    string
	SuggestedContent string
}

// Event carries orchestration progress data to the TUI layer.
type Event struct {
	Type       EventType
	TaskID     string
	Message    string
	Status     types.TaskStatus
	Stream     *types.StreamEvent
	Attempt    int
	Total      int
	Completed  int
	CostUSD    float64
	TokensIn   int
	TokensOut  int
	DurationMs int64
	Timestamp  time.Time
	Insight    *InsightData
}
