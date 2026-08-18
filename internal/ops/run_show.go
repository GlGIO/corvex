package ops

import (
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/types"
)

// RunScope says which key answered the question, because the two keys give
// different answers and a screen that hides which one it used is lying by
// omission: a run id is one execution, a project name is the whole history of
// that project (which is what `status`/`inspect` always showed).
type RunScope string

const (
	ScopeRun     RunScope = "run"
	ScopeProject RunScope = "project"
)

// RunTaskRow is one step on the run screen. It carries what `status` showed
// (id, title, status, dependencies) and what `inspect` showed (duration,
// retries, cost, tokens) in one row, because they were always facts about the
// same thing seen through two commands.
//
// It is a new shape on purpose. Widening InspectTaskStat would have changed the
// frozen `inspect --json` contract, and F3 promised the legacy commands stay
// byte-identical.
type RunTaskRow struct {
	ID         string           `json:"id"`
	Title      string           `json:"title"`
	Status     types.TaskStatus `json:"status"`
	DependsOn  []string         `json:"depends_on,omitempty"`
	DurationMs int64            `json:"duration_ms"`
	Retries    int              `json:"retries"`
	CostUSD    float64          `json:"cost_usd"`
	TokensIn   int              `json:"tokens_in"`
	TokensOut  int              `json:"tokens_out"`
}

// RunStepDetail is `--step`: the plan for one step plus everything the ledger
// recorded about it.
type RunStepDetail struct {
	RunTaskRow
	Description string           `json:"description,omitempty"`
	Criteria    []string         `json:"criteria,omitempty"`
	Create      []string         `json:"create,omitempty"`
	Modify      []string         `json:"modify,omitempty"`
	Summary     string           `json:"summary,omitempty"`
	Decisions   []string         `json:"decisions,omitempty"`
	Events      []activity.Entry `json:"events"`
}

// RunReport is the single screen F3's D3 replaces three commands with.
type RunReport struct {
	Scope       RunScope       `json:"scope"`
	RunID       string         `json:"run_id,omitempty"`
	Repo        string         `json:"repo"`
	Project     string         `json:"project"`
	Recipe      string         `json:"recipe,omitempty"`
	Status      run.Status     `json:"status,omitempty"`
	Liveness    run.Liveness   `json:"liveness,omitempty"`
	Environment string         `json:"environment,omitempty"`
	StartedAt   time.Time      `json:"started_at,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at,omitempty"`
	Intent      string         `json:"intent,omitempty"`
	Total       int            `json:"total"`
	Completed   int            `json:"completed"`
	CostUSD     float64        `json:"cost_usd"`
	Tasks       []RunTaskRow   `json:"tasks"`
	Step        *RunStepDetail `json:"step,omitempty"`
}

// Settled reports whether there is nothing left to watch: the run ended, or it
// can no longer be observed from here. `stale` and `canceling` are deliberately
// NOT settled — a stale heartbeat may come back, and a cancelling run is still
// unwinding, which is exactly the moment a watcher wants to see.
func (r RunReport) Settled() bool {
	if r.RunID == "" {
		return true
	}
	switch r.Liveness {
	case run.LivenessAlive, run.LivenessCanceling, run.LivenessStale:
		return false
	default:
		return true
	}
}

// LoadRunReport builds the screen for `run show <id|project>`.
//
// Resolution is the rule F3 fixed (D2): a well-formed run id is a run id, and
// anything else is a project in the current workspace. Both land on the same
// report; only the ledger slice differs — a run id filters the ledger to that
// execution, a project name keeps the project's whole history, which is exactly
// what the commands this replaces always showed.
func (l RunLister) LoadRunReport(workDir, arg, stepID string) (RunReport, error) {
	rep := RunReport{Scope: ScopeProject, Repo: workDir, Project: arg}
	runID := ""

	if run.ValidID(arg) {
		row, err := l.FindRun(arg)
		if err != nil {
			return RunReport{}, err
		}
		rep.Scope, runID = ScopeRun, row.RunID
		rep.RunID, rep.Repo, rep.Recipe = row.RunID, row.Repo, row.Recipe
		rep.Status, rep.Liveness = row.Status, row.Liveness
		rep.Environment = row.Environment
		rep.StartedAt, rep.UpdatedAt = row.StartedAt, row.UpdatedAt
		rep.Project = row.Project
		if rep.Project == "" {
			// A recipe-driven run compiles into `.corvex/tasks/<recipe>/`, so the
			// recipe name is also the directory to read.
			rep.Project = row.Recipe
		}
	} else if row, ok, err := l.LastRunOf(workDir, arg); err == nil && ok {
		// A project screen still deserves an identity header when the project has
		// run at least once: it is how the user learns the id to address later.
		rep.RunID, rep.Recipe = row.RunID, row.Recipe
		rep.Status, rep.Liveness, rep.Environment = row.Status, row.Liveness, row.Environment
		rep.StartedAt, rep.UpdatedAt = row.StartedAt, row.UpdatedAt
	}

	view, err := ReadProject(rep.Repo, rep.Project)
	if err != nil {
		return RunReport{}, err
	}
	rep.Intent = view.Intent

	entries, _ := activity.Read(rep.Repo, rep.Project)
	if runID != "" {
		entries = activity.FilterByRun(entries, runID)
	}

	rep.Tasks = buildRunTaskRows(view, entries)
	for _, t := range rep.Tasks {
		rep.CostUSD += t.CostUSD
		if t.Status == types.StatusPassed || t.Status == types.StatusSkipped {
			rep.Completed++
		}
	}
	rep.Total = len(rep.Tasks)

	if stepID != "" {
		detail, derr := buildStepDetail(view, entries, rep.Tasks, stepID)
		if derr != nil {
			return RunReport{}, derr
		}
		rep.Step = detail
	}
	return rep, nil
}

// buildRunTaskRows walks the DAG order rather than tasks.md order, so the screen
// reads as the runner would execute it — the ordering `status` established.
func buildRunTaskRows(view *ProjectView, entries []activity.Entry) []RunTaskRow {
	metrics := make(map[string]*RunTaskRow, len(view.Tasks))
	for _, id := range view.Order {
		t, ok := view.ByID[id]
		if !ok {
			continue
		}
		metrics[id] = &RunTaskRow{ID: t.ID, Title: t.Title, Status: t.Status, DependsOn: t.DependsOn}
	}
	for _, e := range entries {
		row, ok := metrics[e.TaskID]
		if !ok {
			continue
		}
		switch e.Type {
		case "task_complete":
			row.DurationMs = e.DurationMs
			row.CostUSD = e.CostUSD
			row.TokensIn = e.TokensIn
			row.TokensOut = e.TokensOut
		case "retry":
			row.Retries++
		}
	}
	rows := make([]RunTaskRow, 0, len(view.Order))
	for _, id := range view.Order {
		if row, ok := metrics[id]; ok {
			rows = append(rows, *row)
		}
	}
	return rows
}

func buildStepDetail(view *ProjectView, entries []activity.Entry, rows []RunTaskRow, stepID string) (*RunStepDetail, error) {
	t, err := FindTask(view.Tasks, stepID)
	if err != nil {
		return nil, err
	}
	detail := &RunStepDetail{
		Description: t.Description,
		Criteria:    t.Criteria,
		Create:      t.Files.Create,
		Modify:      t.Files.Modify,
		Events:      FilterActivityByTask(entries, t.ID),
	}
	detail.RunTaskRow = RunTaskRow{ID: t.ID, Title: t.Title, Status: t.Status, DependsOn: t.DependsOn}
	for _, row := range rows {
		if row.ID == t.ID {
			detail.RunTaskRow = row
			break
		}
	}
	if c, ok := view.Completed[t.ID]; ok {
		detail.Summary, detail.Decisions = c.Summary, c.Decisions
	}
	return detail, nil
}
