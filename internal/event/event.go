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

	// AttemptCost reports what ONE attempt spent, on the paths where the
	// attempt does not end in task_complete: rejected by review, or failed.
	//
	// It exists because a run that fails is the run whose cost somebody most
	// wants to know, and it was the one run that reported nothing. Separate
	// from task_complete rather than folded into it: task_complete is a step's
	// outcome and there is exactly one per step, while attempts are many.
	AttemptCost Type = "attempt_cost"

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

	// Output is what a FAILED computational stage printed — the tail of its own
	// stdout+stderr, already truncated by internal/stepout.
	//
	// It exists because a stage that failed used to say only "command exit did
	// not pass after 1 iteration(s)": the output was captured and then dropped,
	// and the operator had to re-run the command by hand to read the sentence
	// the tool had already written. This field is how the failure line and its
	// cause reach the same screen.
	//
	// It is deliberately NOT part of the ledger. activity.Entry has no field
	// for it and ledgerEntryFromEvent copies field by field, so this one stops
	// at the renderers by construction rather than by anyone remembering —
	// which is what it needs, because activity.jsonl is committed and command
	// output is the same class of content as a command input. The persisted
	// copy goes to `.corvex/runs/output/`, gitignored and 0600; see
	// internal/stepout.
	//
	// Empty on every other event, including a stage that passed: a passing
	// step's output is already gate evidence, and putting it here would change
	// what a green run prints.
	Output string
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
