package ops

import (
	"os"
	"path/filepath"
)

// ProjectSummary is one project in a workspace's inventory: which of its two
// input files exist, and the resulting state of the project.
type ProjectSummary struct {
	Name     string
	HasSpec  bool
	HasTasks bool
	// Status is the derived state: "ready" (spec and plan present),
	// "needs planning" (spec only) or "no spec".
	Status string
}

// ListProjects inventories the projects under a workspace's .corvex/tasks/.
// A workspace with no tasks directory yields an empty list, not an error — the
// caller decides whether "nothing here" is worth saying.
func ListProjects(workDir string) []ProjectSummary {
	names := ProjectNames(workDir)
	tasksDir := filepath.Join(workDir, ".corvex", "tasks")

	projects := make([]ProjectSummary, 0, len(names))
	for _, name := range names {
		hasSpec := false
		hasTasks := false

		if _, err := os.Stat(filepath.Join(tasksDir, name, "spec.md")); err == nil {
			hasSpec = true
		}
		if _, err := os.Stat(filepath.Join(tasksDir, name, "tasks.md")); err == nil {
			hasTasks = true
		}

		status := "no spec"
		if hasSpec && hasTasks {
			status = "ready"
		} else if hasSpec {
			status = "needs planning"
		}

		projects = append(projects, ProjectSummary{
			Name: name, HasSpec: hasSpec, HasTasks: hasTasks, Status: status,
		})
	}
	return projects
}
