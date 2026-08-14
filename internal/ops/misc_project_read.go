package ops

import (
	"fmt"
	"path/filepath"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// ProjectView is a read-only snapshot of one project's plan: the tasks as
// tasks.md lists them, the order the DAG says to walk them in, what the anchor
// records as finished, and the tallies derived from both. It is the single read
// behind `status`, `logs` and anything else that needs to answer "where is this
// project?" — each caller decides on its own how to render it.
type ProjectView struct {
	// Project is the project name as asked for.
	Project string
	// Intent is the anchor's one-line statement of what the project is for,
	// empty when no anchor exists yet.
	Intent string
	// Tasks are the tasks in tasks.md order.
	Tasks []types.Task
	// Order is the dependency-resolved task order. When the DAG has a cycle it
	// falls back to tasks.md order so a broken plan can still be listed.
	Order []string
	// ByID indexes into Tasks.
	ByID map[string]*types.Task
	// Completed indexes the anchor's completion records by task ID.
	Completed map[string]types.CompletedTask
	// Passed, Failed and Pending tally Tasks by status; Pending is everything
	// that is neither passed nor failed.
	Passed  int
	Failed  int
	Pending int
}

// ReadProject loads a project's tasks.md and anchor.yaml and derives the view.
// A missing or unparseable tasks.md is an error; a missing or unreadable
// anchor.yaml is not — a project that was never run simply has no completions.
func ReadProject(workDir, project string) (*ProjectView, error) {
	pDir := ProjectDir(workDir, project)

	tasks, _, err := task.ParseTasksFile(filepath.Join(pDir, "tasks.md"))
	if err != nil {
		return nil, fmt.Errorf("parsing tasks: %w", err)
	}

	anchorState, _ := anchor.Load(filepath.Join(pDir, "anchor.yaml"))

	v := &ProjectView{
		Project:   project,
		Intent:    anchorState.Intent,
		Tasks:     tasks,
		ByID:      make(map[string]*types.Task, len(tasks)),
		Completed: make(map[string]types.CompletedTask, len(anchorState.Completed)),
	}

	order, err := dag.NewDAG(tasks).Resolve()
	if err != nil {
		order = make([]string, len(tasks))
		for i, t := range tasks {
			order[i] = t.ID
		}
	}
	v.Order = order

	for i := range v.Tasks {
		v.ByID[v.Tasks[i].ID] = &v.Tasks[i]
	}
	for _, c := range anchorState.Completed {
		v.Completed[c.ID] = c
	}

	for _, t := range v.Tasks {
		switch t.Status {
		case types.StatusPassed:
			v.Passed++
		case types.StatusFailed:
			v.Failed++
		}
	}
	v.Pending = len(v.Tasks) - v.Passed - v.Failed

	return v, nil
}

// FindTask resolves a task ID against a task list, reporting a missing ID as an
// error so callers do not each invent their own not-found phrasing.
func FindTask(tasks []types.Task, taskID string) (*types.Task, error) {
	for i := range tasks {
		if tasks[i].ID == taskID {
			return &tasks[i], nil
		}
	}
	return nil, fmt.Errorf("task %s not found", taskID)
}
