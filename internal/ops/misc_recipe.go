package ops

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/giovannialves/corvex/internal/recipe"
	"github.com/giovannialves/corvex/internal/task"
)

// RecipeNotFoundError reports that a workspace has no recipe YAML by that name.
// It is a distinct type because the CLI answers it with an authoring hint no
// other read failure deserves.
type RecipeNotFoundError struct {
	Path string
}

func (e *RecipeNotFoundError) Error() string {
	return fmt.Sprintf("recipe not found: %s", e.Path)
}

// RecipePath returns where a recipe YAML lives inside a workspace.
func RecipePath(workDir, name string) string {
	return filepath.Join(workDir, ".corvex", "recipes", name+".yaml")
}

// CompileRecipe reads .corvex/recipes/<name>.yaml, compiles its stages into a
// fixed task DAG and writes it to the project's tasks.md, bypassing AI
// planning. It returns the tasks.md path and the number of compiled tasks.
// A missing recipe yields *RecipeNotFoundError.
func CompileRecipe(workDir, name string) (string, int, error) {
	recipePath := RecipePath(workDir, name)

	data, err := os.ReadFile(recipePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, &RecipeNotFoundError{Path: recipePath}
		}
		return "", 0, fmt.Errorf("reading recipe %s: %w", recipePath, err)
	}

	r, err := recipe.Parse(data)
	if err != nil {
		return "", 0, err
	}
	tasks, spec, err := r.Compile()
	if err != nil {
		return "", 0, err
	}

	projDir := ProjectDir(workDir, name)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("creating project dir %s: %w", projDir, err)
	}
	tasksPath := filepath.Join(projDir, "tasks.md")
	if err := task.WriteTasksFile(tasksPath, tasks, spec); err != nil {
		return "", 0, fmt.Errorf("writing tasks.md: %w", err)
	}

	return tasksPath, len(tasks), nil
}
