package ops

import (
	"fmt"

	"github.com/giovannialves/corvex/internal/dag"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// PlannedTask is one task in the resolved execution order.
type PlannedTask struct {
	ID     string
	Title  string
	Status types.TaskStatus
}

// RunPlan is what a run would do, without doing it: the topological order of
// the DAG and how many of those tasks are still PENDING.
type RunPlan struct {
	Order   []PlannedTask
	Pending int
}

// PlanRun parses tasks.md, validates the DAG and resolves the execution order.
// It answers "what would run", leaving how to display it to the caller.
func PlanRun(tasksPath string) (RunPlan, error) {
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return RunPlan{}, fmt.Errorf("parsing tasks: %w", err)
	}

	d := dag.NewDAG(tasks)
	if err := d.Validate(); err != nil {
		return RunPlan{}, fmt.Errorf("validating DAG: %w", err)
	}

	order, err := d.Resolve()
	if err != nil {
		return RunPlan{}, fmt.Errorf("resolving DAG: %w", err)
	}

	taskMap := make(map[string]*types.Task, len(tasks))
	for i := range tasks {
		taskMap[tasks[i].ID] = &tasks[i]
	}

	plan := RunPlan{Order: make([]PlannedTask, 0, len(order))}
	for _, id := range order {
		t := taskMap[id]
		plan.Order = append(plan.Order, PlannedTask{ID: t.ID, Title: t.Title, Status: t.Status})
		if t.Status == types.StatusPending {
			plan.Pending++
		}
	}
	return plan, nil
}
