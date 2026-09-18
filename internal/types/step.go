package types

import "strings"

// StepKind is the nature of the *work* a step performs. It answers "what does
// this node do", and is deliberately a different axis from Gate, which answers
// "who decides whether the run advances".
//
// Collapsing the two into one enum is what the pre-F2 `Kind` field did (`task`
// and `command` are execution mechanisms, `human-gate` is a decision), and it
// does not survive contact with the real gate catalogue: every gate observed in
// the reference flow guards an action rather than standing alone. "Push the tag
// — a human confirms" is a tool step with a human gate in front of it, not a
// gate node next to a tool node.
type StepKind string

const (
	// KindCode is inferential work: an AI worker writes or fixes code.
	// Expensive, slow, non-deterministic.
	KindCode StepKind = "code"
	// KindTool is a fixed-contract operation that changes the world (open a
	// PR, push a tag, run a migration). Deterministic and cheap.
	KindTool StepKind = "tool"
	// KindTest is a fixed-contract operation that only *observes* the world.
	//
	// It executes exactly like KindTool today and the split is semantic on
	// purpose: it is what the UI uses to paint "this changed something" apart
	// from "this only looked", what F5's accounting uses to separate the cost
	// of acting from the cost of verifying, and what F9 uses to decide where a
	// credential may reach. An enum that merges them now costs a recipe
	// migration later.
	KindTest StepKind = "test"
	// KindRepro is deterministic with a *temporal* verdict: the command must
	// fail before the fix and pass after it. Compiled into two nodes — see
	// Task.FixedBy.
	KindRepro StepKind = "repro"
)

// Legacy kind values, accepted forever so no recipe written before F2 has to be
// rewritten. They are normalised at dispatch, not at compile: tasks.md keeps the
// word the user actually wrote.
const (
	LegacyKindTask      = "task"
	LegacyKindCommand   = "command"
	LegacyKindHumanGate = "human-gate"
)

// NormalizeKind maps whatever a recipe or tasks.md declared onto the four
// step kinds. An unrecognised value maps to KindCode, matching the pre-F2
// default — recipe.Validate is what rejects unknown kinds, and it runs first.
func NormalizeKind(kind string) StepKind {
	switch strings.TrimSpace(kind) {
	case string(KindTool), LegacyKindCommand, LegacyKindHumanGate:
		return KindTool
	case string(KindTest):
		return KindTest
	case string(KindRepro):
		return KindRepro
	default:
		return KindCode
	}
}

// IsComputational reports whether the kind runs without an LLM.
func (k StepKind) IsComputational() bool {
	return k == KindTool || k == KindTest || k == KindRepro
}

// GateNature is who decides whether the run advances past a step. Four of them,
// taken from the twelve gates catalogued in the reference flow — not three, and
// not two: policy (a runner rule: a counter, a ceiling, a branch level) is as
// real as the other three and is today scattered across `if`s, which is why no
// accounting can answer "which gate rejects most".
//
// A fifth, `question`, is not from that catalogue: it is the same axis read
// backwards. The four above ask a decider for a VERDICT about work the run
// already proposed; a question asks a person for a VALUE the run does not have.
// It is a nature rather than a flag on the human gate because the nature is the
// discriminator that already travels on the gate file, on the audit row and on
// the audit's Nature filter — without it, "how long somebody took to type an
// answer" would land in the same distribution as "how long somebody took to
// approve a migration", and the sensor over the sensors would be measuring two
// different acts as one.
type GateNature string

const (
	// GateComputational is decided by the exit code of a shell command.
	GateComputational GateNature = "computational"
	// GateInferential is decided by an independent agent — never the author.
	//
	// Independence here is structural, not a model comparison: the gate runs a
	// fresh provider call with its own prompt and read-only tools, so it never
	// inherits the worker's context. The same model with a clean context and an
	// adversarial prompt is independent; a different model continuing the
	// worker's conversation is not.
	GateInferential GateNature = "inferential"
	// GateHuman blocks the run until a person decides, from another process.
	GateHuman GateNature = "human"
	// GatePolicy is decided by a runner rule: attempt counter, cost ceiling,
	// protected branch. Deterministic, zero tokens.
	GatePolicy GateNature = "policy"
	// GateQuestion parks the run until a person answers it in words. The
	// verdict stays a verdict — approved means "continue, and here is the
	// answer" — and the words themselves ride on gate.Decision.Answer.
	GateQuestion GateNature = "question"
)

// IsValid reports whether the nature is one of the four.
func (n GateNature) IsValid() bool {
	switch n {
	case GateComputational, GateInferential, GateHuman, GatePolicy, GateQuestion:
		return true
	}
	return false
}

// GateWhen is a gate's position relative to the work it guards.
//
// Without it, "approve before merging into main" and "approve the diff after it
// is written" are the same declaration — and the difference between them is
// whether the side effect already happened when you rejected.
type GateWhen string

const (
	// GateBefore decides whether the work happens at all.
	GateBefore GateWhen = "before"
	// GateAfter decides whether the result passes.
	GateAfter GateWhen = "after"
)

// Gate is one decision attached to a step.
type Gate struct {
	Nature GateNature `yaml:"nature" json:"nature"`
	When   GateWhen   `yaml:"when,omitempty" json:"when,omitempty"`
	Label  string     `yaml:"label,omitempty" json:"label,omitempty"`

	// Command is the shell check of a computational gate; exit 0 passes.
	Command string `yaml:"command,omitempty" json:"command,omitempty"`

	// Reviewer is an optional repo skill guiding an inferential gate, and
	// Model overrides the reviewer model for it.
	Reviewer string `yaml:"reviewer,omitempty" json:"reviewer,omitempty"`
	Model    string `yaml:"model,omitempty" json:"model,omitempty"`

	// Prompt is the question put to the person at a human gate.
	Prompt string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	// ExpiresAfter optionally bounds the wait ("72h"). There is deliberately
	// no default: a gate that decides on its own after N hours is a rubber
	// stamp with a delay.
	ExpiresAfter string `yaml:"expires_after,omitempty" json:"expires_after,omitempty"`

	// Policy knobs. MaxAttempts caps retries of the step it guards (the
	// reference flow's "fix loop, cap 2"); MaxCostUSD caps that step's spend;
	// BranchNot refuses to run when the current branch is one of these (the
	// merge-by-level rule, the single most important safety rule of the flow).
	MaxAttempts int      `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty"`
	MaxCostUSD  float64  `yaml:"max_cost_usd,omitempty" json:"max_cost_usd,omitempty"`
	BranchNot   []string `yaml:"branch_not,omitempty" json:"branch_not,omitempty"`
}

// EffectiveWhen resolves the gate's position, applying the per-nature default:
// the two natures that park on a person guard the action, everything else judges
// the result. A question defaults to `before` because the run asks for something
// it needs in order to do the work — asking after it is done is a survey.
func (g Gate) EffectiveWhen() GateWhen {
	if g.When == GateBefore || g.When == GateAfter {
		return g.When
	}
	if g.Nature == GateHuman || g.Nature == GateQuestion {
		return GateBefore
	}
	return GateAfter
}

// Describe is the one-line, machine-independent label a gate contributes to the
// ledger: nature plus the user's own words. It never carries evidence content —
// activity.jsonl is committed (see the activity package comment).
func (g Gate) Describe() string {
	label := strings.TrimSpace(g.Label)
	if label == "" {
		label = strings.TrimSpace(g.Prompt)
	}
	if label == "" {
		return string(g.Nature)
	}
	return string(g.Nature) + ": " + label
}

// EvidenceKind classifies what a piece of evidence *is*, so a renderer can show
// a diff as a diff and a verdict as a verdict without knowing the domain.
type EvidenceKind string

const (
	EvidenceTestOutput EvidenceKind = "test_output"
	EvidenceVerdict    EvidenceKind = "verdict"
	EvidenceDiff       EvidenceKind = "diff"
	EvidenceSQL        EvidenceKind = "sql"
	EvidenceLink       EvidenceKind = "link"
)

// IsValid reports whether the kind is one the renderer understands.
func (k EvidenceKind) IsValid() bool {
	switch k {
	case EvidenceTestOutput, EvidenceVerdict, EvidenceDiff, EvidenceSQL, EvidenceLink:
		return true
	}
	return false
}

// EvidenceStatus is the traffic light on one piece of evidence.
type EvidenceStatus string

const (
	EvidencePass EvidenceStatus = "pass"
	EvidenceWarn EvidenceStatus = "warn"
	EvidenceFail EvidenceStatus = "fail"
)

// IsValid reports whether the status is one of the three.
func (s EvidenceStatus) IsValid() bool {
	switch s {
	case EvidencePass, EvidenceWarn, EvidenceFail:
		return true
	}
	return false
}

// Evidence is what a step hands the person standing at a gate.
//
// The runner only transports and renders it: what "the query plan changed from
// seq scan to index scan" means is known by the `from` command the user wrote in
// their own recipe, never by this binary. That is the whole reason the contract
// exists — without it the gate screen is either generic and useless, or the
// binary learns somebody's domain.
type Evidence struct {
	Kind   EvidenceKind   `yaml:"kind" json:"kind"`
	Label  string         `yaml:"label" json:"label"`
	Status EvidenceStatus `yaml:"status,omitempty" json:"status,omitempty"`
	// RequiredReading arms the approval lock: the CLI refuses to approve until
	// every required item has been acknowledged by label.
	RequiredReading bool `yaml:"required_reading,omitempty" json:"required_reading,omitempty"`
	// Content is the evidence itself, or From is a shell command that produces
	// it at gate time. Exactly one of the two.
	Content string `yaml:"content,omitempty" json:"content,omitempty"`
	From    string `yaml:"from,omitempty" json:"from,omitempty"`
	// Truncated records that Content hit the size ceiling. Set by the producer,
	// never declared in a recipe.
	Truncated bool `yaml:"-" json:"truncated,omitempty"`
}

// StepSpec is one step of a fanout template: the declarable shape of a node,
// without the run-time fields a Task carries.
//
// It lives here rather than in internal/recipe because a template has to survive
// a round trip through tasks.md, and the packages that read tasks.md must not
// depend on the recipe compiler.
type StepSpec struct {
	ID          string     `yaml:"id" json:"id"`
	Title       string     `yaml:"title,omitempty" json:"title,omitempty"`
	Kind        string     `yaml:"kind,omitempty" json:"kind,omitempty"`
	Type        string     `yaml:"type,omitempty" json:"type,omitempty"`
	DependsOn   []string   `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Description string     `yaml:"description,omitempty" json:"description,omitempty"`
	Criteria    []string   `yaml:"criteria,omitempty" json:"criteria,omitempty"`
	Command     string     `yaml:"command,omitempty" json:"command,omitempty"`
	Gates       []Gate     `yaml:"gates,omitempty" json:"gates,omitempty"`
	Evidence    []Evidence `yaml:"evidence,omitempty" json:"evidence,omitempty"`
}

// Fan-out item-failure policies.
const (
	// OnItemFailureAbort is the default and matches pre-F2 behaviour: a failed
	// item fails the fanout, and the normal SKIPPED cascade reaches whatever
	// depended on it.
	OnItemFailureAbort = "abort"
	// OnItemFailureContinue lets the surviving items finish; the fanout passes
	// and records which items fell over as evidence.
	OnItemFailureContinue = "continue"
)

// DefaultFanoutMaxItems bounds a fan-out that discovers its work at run time.
//
// The ceiling is mandatory rather than advisory because each item can be a code
// step: an `ls` that returns 4.000 files is 4.000 LLM calls. The global cost
// ceiling is the net underneath, but a net that catches you after $25 is not the
// same thing as a door that does not open. This is the same class of mistake the
// F1 audit found in the id space — a phase treating a runtime-sized set as
// unbounded.
const DefaultFanoutMaxItems = 50

// Fanout expands one declared node into N instances of a template, discovered at
// run time.
//
// It expands into the *static* DAG rather than becoming a scheduler primitive:
// waves between items are ordinary depends_on edges, so nothing in the wave loop,
// the parallelism bound or the failure cascade has to learn a new concept. The
// only new machinery is that the graph grows once, at expansion time.
type Fanout struct {
	// Over is the id of the step whose output supplies the items.
	Over string `yaml:"over" json:"over"`
	// WaveBy optionally groups items into waves; items in wave k+1 depend on
	// the last template step of every item in wave k.
	WaveBy string `yaml:"wave_by,omitempty" json:"wave_by,omitempty"`
	// MaxItems caps how many items may be expanded. 0 means
	// DefaultFanoutMaxItems.
	MaxItems int `yaml:"max_items,omitempty" json:"max_items,omitempty"`
	// MaxParallel bounds concurrency inside the fanout. 0 defers to the run's
	// execution.max_parallel.
	MaxParallel int `yaml:"max_parallel,omitempty" json:"max_parallel,omitempty"`
	// OnItemFailure is OnItemFailureAbort (default) or OnItemFailureContinue.
	OnItemFailure string `yaml:"on_item_failure,omitempty" json:"on_item_failure,omitempty"`
	// Isolate gives each item its own git worktree and branch, merged back when
	// the item finishes. Empty (the default) keeps every item in the run's one
	// checkout.
	//
	// It exists because "N items in parallel" and "one working tree" cannot both
	// be true for items that WRITE. Without isolation the runner serialises the
	// writers (internal/orchestrator/schedule.go) — correct, and slow. With it,
	// each item edits its own checkout on its own branch, and the merge back is
	// a node of the graph like any other, so a conflict is a step that failed
	// with the conflict in its output rather than a surprise in a shared tree.
	Isolate  string     `yaml:"isolate,omitempty" json:"isolate,omitempty"`
	Template []StepSpec `yaml:"template" json:"template"`
}

// IsolateWorktree is the one isolation mode: a git worktree per item.
const IsolateWorktree = "worktree"

// IsolatesItems reports whether each item gets its own checkout.
func (f Fanout) IsolatesItems() bool { return f.Isolate == IsolateWorktree }

// EffectiveMaxItems resolves the ceiling, applying the default.
func (f Fanout) EffectiveMaxItems() int {
	if f.MaxItems > 0 {
		return f.MaxItems
	}
	return DefaultFanoutMaxItems
}

// ContinuesOnItemFailure reports whether surviving items should finish.
func (f Fanout) ContinuesOnItemFailure() bool {
	return f.OnItemFailure == OnItemFailureContinue
}
