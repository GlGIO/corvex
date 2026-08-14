package ops

import (
	"path/filepath"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// TaskProgress is one task as a dashboard needs it: its identity, its current
// status, and how long it took when it already passed in an earlier run.
type TaskProgress struct {
	ID       string
	Title    string
	Status   types.TaskStatus
	Duration time.Duration
}

// ProjectProgress is the state a run starts from: the task list with historical
// durations, how many are already finished, and the spend accumulated so far.
// It is what fills a dashboard before the first event arrives.
type ProjectProgress struct {
	Tasks     []TaskProgress
	Completed int
	Total     int
	TokensIn  int
	TokensOut int
	CostUSD   float64
}

// LoadProjectProgress reads tasks.md and joins it with the activity ledger.
// A missing or unparsable tasks.md is an error (there is nothing to show); a
// missing ledger is not, because a fresh project simply has no history.
func LoadProjectProgress(workDir, project string) (ProjectProgress, error) {
	tasksPath := filepath.Join(ProjectDir(workDir, project), "tasks.md")
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return ProjectProgress{}, err
	}

	summary, _ := activity.Summarize(workDir, project)

	p := ProjectProgress{
		Tasks:     make([]TaskProgress, 0, len(tasks)),
		Total:     len(tasks),
		TokensIn:  summary.TotalTokensIn,
		TokensOut: summary.TotalTokensOut,
		CostUSD:   summary.TotalCostUSD,
	}
	for _, t := range tasks {
		entry := TaskProgress{ID: t.ID, Title: t.Title, Status: t.Status}
		if metric, ok := summary.PerTask[t.ID]; ok && t.Status == types.StatusPassed {
			entry.Duration = time.Duration(metric.DurationMs) * time.Millisecond
		}
		p.Tasks = append(p.Tasks, entry)
		if t.Status == types.StatusPassed || t.Status == types.StatusSkipped {
			p.Completed++
		}
	}
	return p, nil
}

// RunPreview is what a run is about to cost: how many tasks are pending, the
// configured ceilings, and what the project already spent. Zero ceilings mean
// none is configured; formatting the amounts is the caller's business.
type RunPreview struct {
	Project           string
	Pending           int
	MaxCostUSD        float64
	MaxCostPerTaskUSD float64
	SpentUSD          float64
}

// LoadRunPreview gathers the preview facts. Nothing here is fatal: an
// unreadable tasks.md counts zero pending tasks and an unreadable ledger counts
// zero spend, because a preview must never be the reason a run fails.
func LoadRunPreview(workDir, project string, cfg *config.Config) RunPreview {
	p := RunPreview{
		Project:           project,
		MaxCostUSD:        cfg.Execution.MaxCostUSD,
		MaxCostPerTaskUSD: cfg.Execution.MaxCostPerTaskUSD,
	}

	tasksPath := filepath.Join(ProjectDir(workDir, project), "tasks.md")
	if tasks, _, err := task.ParseTasksFile(tasksPath); err == nil {
		for _, t := range tasks {
			if t.Status == types.StatusPending {
				p.Pending++
			}
		}
	}

	if summary, err := activity.Summarize(workDir, project); err == nil {
		p.SpentUSD = summary.TotalCostUSD
	}
	return p
}
