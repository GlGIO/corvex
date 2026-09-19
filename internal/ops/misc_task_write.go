package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// ResetTask marks one task PENDING in a project's tasks.md — and with it
// everything downstream — so it can be executed again. Task IDs are
// case-insensitive: "s01" and "S01" are the same task.
//
// # Why the cascade
//
// "Re-execute one stage without redoing the rest" is the promise, and the rest
// means the steps that do NOT depend on this one. A dependent that stayed PASSED
// was computed from an output this reset just threw away, and the runner says so
// itself: the DAG integrity check refuses a run whose PASSED task has a
// dependency that is not PASSED. Measured on a fan-out item: retrying
// `S02/000/trabalha` left `S02/000/merge` PASSED and the next run aborted with
// "DAG integrity violation", pointing the operator at two manual fixes.
//
// So the fix is the same one `always:` needed (internal/orchestrator/run.go):
// reset the step, then everything that reaches it.
func ResetTask(workDir, project, taskID string) error {
	tasksPath := filepath.Join(ProjectDir(workDir, project), "tasks.md")
	// The id is passed through as typed: the lookups match it
	// case-insensitively, which is the only rule that serves both `s03` and a
	// fan-out's `S02/000/trabalha` — upper-casing destroyed the second.
	wanted := strings.TrimSpace(taskID)

	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return err
	}
	stale := map[string]bool{}
	for _, t := range tasks {
		if strings.EqualFold(t.ID, wanted) {
			stale[t.ID] = true
		}
	}
	if len(stale) == 0 {
		return fmt.Errorf("task %s not found in %s", wanted, tasksPath)
	}
	for again := true; again; {
		again = false
		for _, t := range tasks {
			if stale[t.ID] {
				continue
			}
			for _, dep := range t.DependsOn {
				if stale[dep] {
					stale[t.ID] = true
					again = true
					break
				}
			}
		}
	}

	// An isolated fan-out item runs in a worktree the merge node removed when it
	// landed. Re-running the item there would execute in a directory that is not
	// on disk, so the refusal names the situation and the way around it instead
	// of failing later with a path nobody recognises.
	for _, t := range tasks {
		if !stale[t.ID] || t.WorkDir == "" {
			continue
		}
		if _, statErr := os.Stat(t.WorkDir); statErr != nil {
			return fmt.Errorf("step %s ran in its own worktree (%s), which was removed when its work was merged.\n"+
				"Re-running just this item would execute in a directory that is gone.\n"+
				"  → re-run the whole stage instead:  corvex run start %s --recompile",
				t.ID, t.WorkDir, project)
		}
	}

	for _, t := range tasks {
		if !stale[t.ID] {
			continue
		}
		if err := task.UpdateTaskStatus(tasksPath, t.ID, types.StatusPending); err != nil {
			return err
		}
	}
	return nil
}
