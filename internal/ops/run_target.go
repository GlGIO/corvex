package ops

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// StartPlan reports what `run start <name>` had to do before it could run.
type StartPlan struct {
	// Compiled is true when this call wrote tasks.md from a recipe.
	Compiled bool
	// Tasks is how many the compilation produced (zero when nothing compiled).
	Tasks int
	// Finished is how many steps were PASSED before a --recompile discarded
	// them, so the caller can say what it cost.
	Finished int
}

// RecipeDriftError reports a compiled DAG that is older than the recipe that
// produced it.
//
// It is an error rather than an automatic recompilation because tasks.md is
// STATE, not shape: the step statuses live in that file. F2 found this the
// expensive way — a fan-out that rewrote tasks.md from memory sent every step
// back to PENDING and the next run died on DAG integrity. Recompiling silently
// would do exactly that to a user's finished work.
type RecipeDriftError struct {
	Name     string
	Recipe   string
	Tasks    string
	Finished int
}

func (e *RecipeDriftError) Error() string {
	return fmt.Sprintf("recipe %q changed after %s was compiled (%d step(s) already finished).\n"+
		"  recompile and lose that progress:  corvex run start %s --recompile\n"+
		"  or keep the compiled DAG as it is: corvex run start %s --no-recompile",
		e.Name, e.Tasks, e.Finished, e.Name, e.Name)
}

// PrepareRunTarget resolves the name `run start` was given (F3, D4).
//
// Resolution order, and why: an existing compiled project wins, because that is
// where the progress lives; a recipe with no project compiles, because refusing
// would make `run start <recipe>` useless for the exact case recipes exist for;
// and a name that is neither is left alone, so the caller can produce the
// "project not found" message with its spelling suggestion.
func PrepareRunTarget(workDir, name string, recompile, noRecompile bool) (StartPlan, error) {
	tasksPath := filepath.Join(ProjectDir(workDir, name), "tasks.md")
	recipePath := RecipePath(workDir, name)

	tasksInfo, tasksErr := os.Stat(tasksPath)
	recipeInfo, recipeErr := os.Stat(recipePath)
	hasTasks, hasRecipe := tasksErr == nil, recipeErr == nil

	switch {
	case !hasRecipe:
		return StartPlan{}, nil // legacy project, or nothing — not our business
	case !hasTasks:
		_, count, err := CompileRecipe(workDir, name)
		if err != nil {
			return StartPlan{}, err
		}
		return StartPlan{Compiled: true, Tasks: count}, nil
	case recompile:
		finished := finishedSteps(tasksPath)
		_, count, err := CompileRecipe(workDir, name)
		if err != nil {
			return StartPlan{}, err
		}
		return StartPlan{Compiled: true, Tasks: count, Finished: finished}, nil
	case noRecompile || !recipeInfo.ModTime().After(tasksInfo.ModTime()):
		return StartPlan{}, nil
	default:
		return StartPlan{}, &RecipeDriftError{
			Name: name, Recipe: recipePath, Tasks: tasksPath, Finished: finishedSteps(tasksPath),
		}
	}
}

// finishedSteps counts what a recompilation would throw away. An unreadable
// tasks.md counts zero: the number exists to make a warning concrete, and a
// broken file is not a reason to refuse the recompilation that would fix it.
func finishedSteps(tasksPath string) int {
	tasks, _, err := task.ParseTasksFile(tasksPath)
	if err != nil {
		return 0
	}
	n := 0
	for _, t := range tasks {
		if t.Status == types.StatusPassed {
			n++
		}
	}
	return n
}
