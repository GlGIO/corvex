package ops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

func writeProjectTasks(t *testing.T, workDir, project, generatedBy string) {
	t.Helper()
	dir := ProjectDir(workDir, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	tasks := []types.Task{{ID: "S01", Title: "Bootstrap", Status: types.StatusPending}}
	spec := types.DAGSpec{GeneratedBy: generatedBy, Dependencies: map[string][]string{"S01": {}}}
	if err := task.WriteTasksFile(filepath.Join(dir, "tasks.md"), tasks, spec); err != nil {
		t.Fatalf("WriteTasksFile: %v", err)
	}
}

// TestRecipeFromTasks closes the F1 debt without new storage: the recipe name is
// already stamped on the tasks.md frontmatter by the compiler, so identity reads
// it back instead of carrying a second copy that could drift.
func TestRecipeFromTasks(t *testing.T) {
	cases := []struct {
		name        string
		generatedBy string
		want        string
	}{
		{"recipe run", "corvex-recipe:feature-pipeline", "feature-pipeline"},
		{"planner run", "corvex-planner", ""},
		{"legacy, no frontmatter", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			workDir := t.TempDir()
			writeProjectTasks(t, workDir, "demo", c.generatedBy)
			if got := recipeFromTasks(workDir, "demo"); got != c.want {
				t.Errorf("recipeFromTasks = %q, want %q", got, c.want)
			}
		})
	}
}

// TestRecipeFromTasks_MissingProjectIsEmpty is the best-effort promise: identity
// is minted before the planner has written anything, and a run must never be
// refused because a recipe name could not be read.
func TestRecipeFromTasks_MissingProjectIsEmpty(t *testing.T) {
	if got := recipeFromTasks(t.TempDir(), "nope"); got != "" {
		t.Errorf("recipeFromTasks on a missing project = %q, want empty", got)
	}
}
