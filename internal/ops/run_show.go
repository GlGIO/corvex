package ops

import (
	"sort"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/stepout"
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

	// Output is what this step's command printed when it failed — the tail of
	// its own stdout+stderr, read from `.corvex/runs/output/`.
	//
	// It is not in the ledger and cannot be: activity.jsonl is committed by
	// corvex's own auto_commit, and command output is the same class of content
	// as a command input, which is why activity.Entry carries a tool's NAME and
	// never its arguments. So this screen joins two stores — the committed
	// timeline for what happened, and machine-local scratch for what was said.
	//
	// Empty for every step that passed, for every step whose command printed
	// nothing, and for every run that predates the store. This is the canonical
	// detail surface, which is why it is the one that must not need the terminal
	// scrollback of the person who started the run.
	Output string `json:"output,omitempty"`
}

// RunReport is the single screen F3's D3 replaces three commands with.
type RunReport struct {
	Scope       RunScope     `json:"scope"`
	RunID       string       `json:"run_id,omitempty"`
	Repo        string       `json:"repo"`
	Project     string       `json:"project"`
	Recipe      string       `json:"recipe,omitempty"`
	Status      run.Status   `json:"status,omitempty"`
	Liveness    run.Liveness `json:"liveness,omitempty"`
	Environment string       `json:"environment,omitempty"`
	StartedAt   time.Time    `json:"started_at,omitempty"`
	UpdatedAt   time.Time    `json:"updated_at,omitempty"`
	Intent      string       `json:"intent,omitempty"`
	Total       int          `json:"total"`
	Completed   int          `json:"completed"`
	// Skipped are steps a failed dependency took out. Kept apart from Completed
	// because a run that skipped four of five steps did not almost finish — the
	// first dogfood run reported "4/5 steps" for a run that completed nothing.
	Skipped int          `json:"skipped,omitempty"`
	CostUSD float64      `json:"cost_usd"`
	Tasks   []RunTaskRow `json:"tasks"`

	// PerPhase is where the money went, by the nature of the work that spent it
	// (F5). It is the 2f bar, and it only became answerable when `phase` stopped
	// being a column nobody wrote.
	PerPhase []PhaseCost `json:"per_phase,omitempty"`
	// HumanWaitMs is time a person, not a machine, was the bottleneck — gates
	// waiting to be decided. Reported apart from the run's own clock because a
	// run that took four hours of which three were somebody asleep is not a slow
	// run, and until F5 both numbers were the same number.
	HumanWaitMs int64 `json:"human_wait_ms,omitempty"`

	Step *RunStepDetail `json:"step,omitempty"`
}

// PhaseCost is one bar of the cost-by-nature breakdown, sorted so the screen is
// stable between reads.
type PhaseCost struct {
	Phase      string  `json:"phase"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMs int64   `json:"duration_ms"`
	Entries    int     `json:"entries"`
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

	rep.PerPhase, rep.HumanWaitMs = summarise(entries)
	rep.Tasks = buildRunTaskRows(view, entries)
	for _, t := range rep.Tasks {
		// The header's total is the sum of the per-step rows, which are keyed by
		// task with last-write-wins — the same rule `inspect` has always used,
		// and the reason a retried task counts once.
		rep.CostUSD += t.CostUSD
		// SKIPPED is not done. A dogfood run whose first step failed and whose
		// four dependents were skipped reported "4/5 steps" — a screen saying a
		// run nearly finished when it did nothing. Skipped is counted and named
		// separately.
		if t.Status == types.StatusPassed {
			rep.Completed++
		}
		if t.Status == types.StatusSkipped {
			rep.Skipped++
		}
	}
	rep.Total = len(rep.Tasks)

	if stepID != "" {
		detail, derr := buildStepDetail(view, entries, rep.Tasks, stepID, rep.Repo, rep.RunID)
		if derr != nil {
			return RunReport{}, derr
		}
		rep.Step = detail
	}
	return rep, nil
}

// summarise reduces the same slice of entries the screen already holds, rather
// than re-reading the ledger: `run show <id>` filters to one execution, and a
// second read would summarise the project instead of the run.
//
// The breakdown sums EVERY line, while the header sums the per-step rows with
// last-write-wins. The two answer different questions — "what did this project
// ever spend on this step" versus "what did the surviving attempt cost" — and on
// a project that was retried the breakdown is legitimately larger. That is worth
// a line on screen rather than a silent discrepancy, so renderPhaseBar scales
// its bars to the breakdown's own total and says so when the two disagree.
func summarise(entries []activity.Entry) ([]PhaseCost, int64) {
	sum := activity.AggregateEntries(entries)
	phases := make([]PhaseCost, 0, len(sum.PerPhase))
	for _, m := range sum.PerPhase {
		phases = append(phases, PhaseCost{Phase: m.Phase, CostUSD: m.CostUSD, DurationMs: m.DurationMs, Entries: m.Entries})
	}
	sort.Slice(phases, func(i, j int) bool {
		if phases[i].CostUSD == phases[j].CostUSD {
			return phases[i].Phase < phases[j].Phase
		}
		return phases[i].CostUSD > phases[j].CostUSD
	})
	return phases, sum.HumanWaitMs
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
	// lastReviewCost mirrors internal/activity's aggregate(): task_complete only
	// carries the worker's own spend, so the row's total is read back off the
	// review_result line that shares its attempt. Consumed on use so an
	// after-gate's later review_result (same task_id, no row of its own) is
	// never folded in.
	lastReviewCost := make(map[string]float64)
	// Same two-accumulator rule as BuildInspectReport: task_complete assigns,
	// the split-out lines accumulate, and mixing them into one field lets the
	// assignment erase the others.
	extra := make(map[string]RunTaskRow, len(view.Tasks))
	for _, e := range entries {
		row, ok := metrics[e.TaskID]
		if !ok {
			continue
		}
		switch e.Type {
		case "review_result":
			lastReviewCost[e.TaskID] = e.CostUSD
		case "task_complete":
			reviewCost := lastReviewCost[e.TaskID]
			delete(lastReviewCost, e.TaskID)
			row.DurationMs = e.DurationMs
			row.CostUSD = e.CostUSD + reviewCost
			row.TokensIn = e.TokensIn
			row.TokensOut = e.TokensOut
		case "attempt_cost":
			// The spend of an attempt that did NOT end in a task_complete: a
			// worker call that failed, a review that failed, and the call that
			// tripped a ceiling. It accumulates — there is one line per attempt
			// — and it is kept apart from the assignment above for the reason
			// the comment on `extra` gives: mixing them lets the assignment
			// erase what the failures cost.
			//
			// This case is why `extra` exists, and until now it was declared,
			// read at the bottom of this function, and written by NOBODY. The
			// run screen therefore reported only the surviving attempt while
			// `corvex inspect` reported every one of them — two screens over one
			// ledger, disagreeing about money, with nothing saying so. Measured
			// against a ceiling abort: the run screen said $22.50 for a run the
			// runner had aborted at $27.00.
			acc := extra[e.TaskID]
			acc.CostUSD += e.CostUSD
			acc.TokensIn += e.TokensIn
			acc.TokensOut += e.TokensOut
			extra[e.TaskID] = acc
		case "retry":
			row.Retries++
		}
	}
	rows := make([]RunTaskRow, 0, len(view.Order))
	for _, id := range view.Order {
		row, ok := metrics[id]
		if !ok {
			continue
		}
		if acc, has := extra[id]; has {
			row.CostUSD += acc.CostUSD
			row.TokensIn += acc.TokensIn
			row.TokensOut += acc.TokensOut
		}
		rows = append(rows, *row)
	}
	return rows
}

// buildStepDetail assembles one step's screen.
//
// repo and runID are taken rather than derived because the step's output lives
// outside the ledger, keyed by (repo, run id, step id) — the same tuple the gate
// store uses, and for the same reason: it is per-machine scratch that must not
// reach a commit. On a project-scoped screen runID is the project's last run,
// which is also the run whose status tasks.md is showing.
func buildStepDetail(view *ProjectView, entries []activity.Entry, rows []RunTaskRow, stepID, repo, runID string) (*RunStepDetail, error) {
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
		Output:      stepout.Read(repo, runID, t.ID),
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
