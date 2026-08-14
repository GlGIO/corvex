package ops

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// InspectReport is the aggregated view of a project: the anchor intent plus one
// row per task in tasks.md, enriched with the metrics found in the activity
// ledger. The JSON tags are part of the CLI contract (`corvex inspect --json`).
type InspectReport struct {
	Project      string            `json:"project"`
	Intent       string            `json:"intent,omitempty"`
	Total        int               `json:"total"`
	Completed    int               `json:"completed"`
	TotalCostUSD float64           `json:"totalCostUSD"`
	Tasks        []InspectTaskStat `json:"tasks"`
}

// InspectTaskStat carries per-task metrics aggregated from the activity ledger.
type InspectTaskStat struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Status     string  `json:"status"`
	DurationMs int64   `json:"durationMs"`
	Retries    int     `json:"retries"`
	CostUSD    float64 `json:"costUSD"`
	TokensIn   int     `json:"tokensIn"`
	TokensOut  int     `json:"tokensOut"`
}

// ReadActivityLedger loads every entry recorded for a project. A missing ledger
// is not an error: it yields an empty slice, which callers read as "nothing ran
// yet".
func ReadActivityLedger(workDir, project string) ([]activity.Entry, error) {
	entries, err := activity.Read(workDir, project)
	if err != nil {
		return nil, fmt.Errorf("reading activity ledger: %w", err)
	}
	return entries, nil
}

// FilterActivityByTask keeps only the entries belonging to one task, preserving
// ledger order. The result is never nil, so a JSON encoder emits [] and not
// null when the task has no events.
func FilterActivityByTask(entries []activity.Entry, taskID string) []activity.Entry {
	filtered := make([]activity.Entry, 0)
	for _, e := range entries {
		if e.TaskID == taskID {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// BuildInspectReport joins tasks.md, anchor.yaml and the activity ledger into a
// single report, with tasks sorted by ID.
//
// Note: a ledger entry whose task ID is absent from tasks.md is dropped without
// warning, so its cost never reaches TotalCostUSD. That is a known anomaly kept
// deliberately (see .corvex/tasks/rebrand/f-1-anomalias.md); do not "fix" it
// here without changing the golden network first.
func BuildInspectReport(workDir, project string, entries []activity.Entry) (InspectReport, error) {
	tasksPath := filepath.Join(ProjectDir(workDir, project), "tasks.md")
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return InspectReport{}, fmt.Errorf("reading tasks: %w", err)
	}

	anchorState, _ := anchor.Load(filepath.Join(ProjectDir(workDir, project), "anchor.yaml"))

	statsByID := make(map[string]*InspectTaskStat, len(tasks))
	for _, t := range tasks {
		statsByID[t.ID] = &InspectTaskStat{ID: t.ID, Title: t.Title, Status: string(t.Status)}
	}

	for _, e := range entries {
		s, ok := statsByID[e.TaskID]
		if !ok {
			continue
		}
		switch e.Type {
		case "task_complete":
			s.DurationMs = e.DurationMs
			s.CostUSD = e.CostUSD
			s.TokensIn = e.TokensIn
			s.TokensOut = e.TokensOut
		case "retry":
			s.Retries++
		}
	}

	ids := make([]string, 0, len(statsByID))
	for id := range statsByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var totalCost float64
	completed := 0
	statList := make([]InspectTaskStat, 0, len(ids))
	for _, id := range ids {
		s := statsByID[id]
		totalCost += s.CostUSD
		if types.TaskStatus(s.Status) == types.StatusPassed {
			completed++
		}
		statList = append(statList, *s)
	}

	return InspectReport{
		Project:      project,
		Intent:       anchorState.Intent,
		Total:        len(tasks),
		Completed:    completed,
		TotalCostUSD: totalCost,
		Tasks:        statList,
	}, nil
}
