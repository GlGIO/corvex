// Package event carries run progress from the code that produces it to the
// code that renders it.
//
// It is a leaf package on purpose. The scheduler (internal/orchestrator) and
// the single-step executor (internal/step) both emit these events, and both
// renderers (cmd, internal/tui) consume them; parking the type in either
// producer would force an import cycle between the two.
package event

import (
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

// Type classifies a run event for UI consumption. The string values are also
// the `type` field of the activity ledger's JSONL entries, so they are part of
// the on-disk format.
type Type string

const (
	RecoveryCheck  Type = "recovery_check"
	RecoveryResult Type = "recovery_result"
	PlanStart      Type = "plan_start"
	PlanComplete   Type = "plan_complete"
	DAGResolved    Type = "dag_resolved"
	TaskStart      Type = "task_start"
	TaskStream     Type = "task_stream"
	TaskWarn       Type = "task_warn"
	TaskTimeout    Type = "task_timeout"
	HumanGate      Type = "human_gate"
	TaskComplete   Type = "task_complete"

	// Gate lifecycle (F2). These reach activity.jsonl, which is committed, so
	// their Message carries only the gate's nature and the label the user wrote
	// in their own recipe — never evidence content, which lives in the
	// gitignored gate file under `.corvex/runs/gates/`.
	GatePending Type = "gate_pending"
	GateDecided Type = "gate_decided"
	GateFailed  Type = "gate_failed"

	ReviewStart    Type = "review_start"
	ReviewResult   Type = "review_result"
	Checkpoint     Type = "checkpoint"
	Retry          Type = "retry"
	Error          Type = "error"
	Done           Type = "done"
	SandboxPrepare Type = "sandbox_prepare"
	SandboxCleanup Type = "sandbox_cleanup"
	Insight        Type = "insight"
)

// Event carries orchestration progress data to the TUI layer.
type Event struct {
	Type       Type
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
	Insight    *types.InsightData

	// Phase says which part of the machine produced this event: worker,
	// review, plan, recovery, gate, validate. It is what makes "where did the
	// money go" answerable — the ledger has carried a `phase` column since
	// before F1 and nothing ever filled it, so every cost in every ledger on
	// disk is unattributed. F5 fills it.
	Phase string
	// Tool is the name of the tool a worker invoked, for the tool_use and
	// tool_result lines. Never its input: see activity.Entry.Tool.
	Tool string
}

// Phase names. Values are on-disk (the ledger's `phase` column), so they are a
// format, not labels: renaming one silently re-buckets every past run.
const (
	PhaseWorker   = "worker"
	PhaseReview   = "review"
	PhasePlan     = "plan"
	PhaseRecovery = "recovery"
	PhaseGate     = "gate"
	PhaseValidate = "validate"
)

// Emitter is the narrow sink a producer needs: hand it an event and it reaches
// the ledger and the UI. The orchestrator supplies the real implementation.
type Emitter func(Event)
