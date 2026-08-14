package step

import (
	"sync"

	"github.com/giovannialves/corvex/internal/anchor"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// Bookkeeper guards the state a run shares between the scheduler
// (internal/orchestrator) and the step executor: the tasks.md/anchor.yaml
// files, the git checkpoint, the completed/terminal sets, and the cumulative
// cost counters.
//
// One instance is created per run and handed to both sides, so a single mutex
// covers that state no matter which goroutine touches it. The expensive LLM
// calls (worker, reviewer) run OUTSIDE the lock — only the fast bookkeeping is
// serialised.
type Bookkeeper struct {
	mu sync.Mutex
}

// Lock/Unlock expose the shared mutex for callers that must serialise a whole
// read-modify-write block (e.g. the scheduler mutating `completed`) rather
// than a single operation.
func (b *Bookkeeper) Lock()   { b.mu.Lock() }
func (b *Bookkeeper) Unlock() { b.mu.Unlock() }

// SetStatus serialises tasks.md rewrites (a whole-file read-modify-write) so
// concurrent tasks can't clobber each other's status updates.
func (b *Bookkeeper) SetStatus(tasksPath, id string, status types.TaskStatus) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return task.UpdateTaskStatus(tasksPath, id, status)
}

// AddCost accumulates an attempt's cost into the per-task and run totals under
// the lock and returns both updated totals for ceiling checks.
func (b *Bookkeeper) AddCost(taskTotal, runTotal *float64, c float64) (taskT, runT float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*taskTotal += c
	*runTotal += c
	return *taskTotal, *runTotal
}

// AnchorContext builds the prompt context from the shared anchor under the
// lock (parallel tasks may be updating it concurrently).
func (b *Bookkeeper) AnchorContext(state *types.AnchorState, taskID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return anchor.GenerateContext(*state, taskID)
}
