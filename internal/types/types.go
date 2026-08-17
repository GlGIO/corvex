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
}
