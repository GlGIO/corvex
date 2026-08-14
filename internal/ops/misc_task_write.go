package ops

import (
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// ResetTask marks one task PENDING in a project's tasks.md so it can be
// executed again. Task IDs are case-insensitive: "s01" and "S01" are the same
// task.
func ResetTask(workDir, project, taskID string) error {
	tasksPath := filepath.Join(ProjectDir(workDir, project), "tasks.md")
	return task.UpdateTaskStatus(tasksPath, strings.ToUpper(taskID), types.StatusPending)
}
