package types

// Version is set via ldflags at build time (e.g. -X ...types.Version=v1.0.0).
var Version = "v0.1.0-dev"

type TaskStatus string

const (
	StatusPending TaskStatus = "PENDING"
	StatusRunning TaskStatus = "RUNNING"
	StatusPassed  TaskStatus = "PASSED"
	StatusFailed  TaskStatus = "FAILED"
	StatusSkipped TaskStatus = "SKIPPED"
)

func (s TaskStatus) IsValid() bool {
	switch s {
	case StatusPending, StatusRunning, StatusPassed, StatusFailed, StatusSkipped:
		return true
	}
	return false
}

func (s TaskStatus) IsTerminal() bool {
	switch s {
	case StatusPassed, StatusFailed, StatusSkipped:
		return true
	}
	return false
}

func (s TaskStatus) String() string {
	return string(s)
}

type TaskType string

const (
	TypeDatabase TaskType = "database"
	TypeBackend  TaskType = "backend"
	TypeFrontend TaskType = "frontend"
	TypeReview   TaskType = "review"
	TypeGeneral  TaskType = "general"
)

func (t TaskType) IsValid() bool {
	switch t {
	case TypeDatabase, TypeBackend, TypeFrontend, TypeReview, TypeGeneral:
		return true
	}
	return false
}

type StreamEventType string

const (
	EventText       StreamEventType = "text"
	EventToolUse    StreamEventType = "tool_use"
	EventToolResult StreamEventType = "tool_result"
	EventDone       StreamEventType = "done"
	EventError      StreamEventType = "error"
)

func (e StreamEventType) IsValid() bool {
	switch e {
	case EventText, EventToolUse, EventToolResult, EventDone, EventError:
		return true
	}
	return false
}

type Task struct {
	ID          string
	Title       string
	Status      TaskStatus
	Type        TaskType
	DependsOn   []string
	Description string
	Criteria    []string
	Files       TaskFiles
	// Kind selects how the task executes. Empty or "task" → an AI worker task
	// (the default). "command" → run Command as a shell step (exit 0 = pass,
	// no LLM). Reserved: "human-gate".
	Kind string
	// Command is the shell command run when Kind == "command".
	Command string
	// LoopUntil, when set on a command stage, is a shell condition re-evaluated
	// each iteration; the stage loops until it exits 0 (or LoopMax is reached).
	// When empty, the command's own exit code is the loop condition.
	LoopUntil string
	// Timeout overrides the run-wide wall clock for THIS step
	// (`execution.task_timeout_minutes`). Empty means the run-wide value.
	//
	// It exists because the run-wide value cannot be right for every step: a
	// `code` step where an agent edits a package and a `test` step that runs a
	// suite have completely different time profiles, and the first dogfood run
	// of this repository was killed at 20 minutes while the worker was still
	// working. Per step, because that is the granularity at which the answer
	// differs.
	Timeout string

	// LoopMax caps loop iterations for a command stage. 0 or 1 → run once (no
	// loop).
	LoopMax int

	// Gates are the decisions attached to this step (F2). Empty means the
	// step's own outcome is the only decision.
	Gates []Gate
	// Evidence is what this step hands whoever stands at its gates. Declared
	// items are listed here; producers add more at run time.
	Evidence []Evidence
	// Fanout, when set, expands this node into N instances of a template
	// discovered at run time.
	Fanout *Fanout
	// Produces names what this step's output feeds ("items" for a fanout
	// source). Empty for a step nothing reads from.
	Produces string
	// FixedBy is the step that is expected to make a `repro` command stop
	// reproducing. Only meaningful for KindRepro.
	FixedBy string
	// ExpectFail inverts the exit-code verdict: the command must fail for the
	// step to pass. Set by the compiler on the "before" half of a repro, which
	// is the only node in the system whose success is a non-zero exit.
	ExpectFail bool
	// Item is the value this node was expanded from, when it came out of a
	// fanout. Empty for a declared node.
	Item string
	// Items is what a `produces: items` step discovered. Persisted with the
	// task so a resumed run expands the same set instead of rediscovering it —
	// rediscovery would silently pick up whatever changed in between.
	Items []string
	// Expanded marks a fanout node whose template has already been instantiated.
	// It is what makes expansion idempotent across a resume.
	Expanded bool
	// FanoutOf is the id of the fanout node this task was expanded from.
	FanoutOf string
	// WritesRunTree marks a command step that edits the RUN's checkout, which
	// the runner cannot tell from the command itself: `npm test` and `git merge`
	// are both `kind: tool` and only one of them writes.
	//
	// It exists because of a measured collision: two generated merge nodes of an
	// isolated fan-out ran in the same wave, in the same checkout, and the
	// second git call died on `.git/index.lock` — exit 128, reported as a failed
	// step with a message about a lock file. Marking them makes the tree lock
	// (internal/orchestrator/schedule.go) cover them, which is what the lock was
	// always for: one writer per tree.
	WritesRunTree bool
	// WorkDir is the directory this task executes in when it is not the run's
	// own checkout: the git worktree an isolated fan-out item was given.
	//
	// Persisted with the task, not derived from the id, because a resumed run
	// has to land in the SAME worktree the first attempt used — and a derivation
	// rule is a second source of truth that drifts the day the naming changes.
	// Empty means the run's checkout, which is every task that is not an
	// isolated fan-out item.
	WorkDir string
}

type TaskFiles struct {
	Create []string
	Modify []string
}

type DAGSpec struct {
	GeneratedBy  string              `yaml:"generated_by"`
	GeneratedAt  string              `yaml:"generated_at"`
	Dependencies map[string][]string `yaml:"dag"`
}

type CompletedTask struct {
	ID            string   `yaml:"id"`
	Title         string   `yaml:"title"`
	Summary       string   `yaml:"summary"`
	FilesCreated  []string `yaml:"files_created"`
	FilesModified []string `yaml:"files_modified"`
	Decisions     []string `yaml:"decisions"`
}

type CurrentState struct {
	TotalTasks     int `yaml:"total_tasks"`
	CompletedTasks int `yaml:"completed_tasks"`
}

type AnchorState struct {
	Project         string          `yaml:"project"`
	UpdatedAt       string          `yaml:"updated_at"`
	SpecHash        string          `yaml:"spec_hash"`
	Intent          string          `yaml:"intent"`
	Completed       []CompletedTask `yaml:"completed"`
	CurrentState    CurrentState    `yaml:"current_state"`
	NextTask        string          `yaml:"next_task"`
	NextTaskContext string          `yaml:"next_task_context"`
}

type ExecuteRequest struct {
	Prompt          string
	Model           string
	WorkDir         string
	Env             map[string]string
	AllowedTools    []string
	DisallowedTools []string
	// DenyEnv are variable NAMES the provider process must not inherit from
	// corvex (F9 credential custody). Applied where the process is created,
	// because that is the only place it is true — everywhere else it is a
	// statement about a set nobody enforces.
	DenyEnv []string
}

type ExecuteResult struct {
	Output     string
	ExitCode   int
	TokensIn   int
	TokensOut  int
	CostUSD    float64
	DurationMs int64
}

type StreamEvent struct {
	Type    StreamEventType
	Content string
	Tool    string
	File    string
	// ID pairs a tool call with its result: the provider's own id on a
	// tool_use, and the id it refers to on a tool_result. Without it the only
	// available pairing is arrival order, which is wrong the moment one
	// assistant turn issues several calls.
	ID string
}
