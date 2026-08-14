package tui

// Startup back-fill: helpers the caller uses before the first orchestrator
// event arrives, so the dashboard opens with the DAG and the cumulative
// metrics of previous runs already in place.

import (
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

// AddDAGTasks is a convenience to populate the DAG panel from orchestrator data.
func (m Model) AddDAGTasks(tasks []TaskEntry) Model {
	m.dag = m.dag.AddTasks(tasks)
	return m
}

// SetDAGProgress updates the completed/total counters shown in the header.
func (m Model) SetDAGProgress(completed, total int) Model {
	m.dag = m.dag.SetProgress(completed, total)
	return m
}

// SeedStatusTotals adds cumulative tokens/cost from previous runs to the
// status bar so the header doesn't display "$0.00" while the project has
// actually consumed budget in earlier sessions. Call this once at startup
// before the first orchestrator event arrives; subsequent EventTaskComplete
// events add on top, producing the correct running total.
func (m Model) SeedStatusTotals(tokensIn, tokensOut int, cost float64) Model {
	m.status = m.status.AddTokens(tokensIn, tokensOut, cost)
	return m
}

// SeedTaskDuration backfills the duration of a task that completed in a
// previous run. The orchestrator emits no events for already-PASSED tasks,
// so without this the DAG panel would render them with "0s".
func (m Model) SeedTaskDuration(id string, status types.TaskStatus, duration time.Duration) Model {
	m.dag = m.dag.UpdateTask(id, status, duration, 0)
	return m
}
