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
	MaxTurns    int
	Description string
	Criteria    []string
	Files       TaskFiles
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
	MaxTurns        int
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
