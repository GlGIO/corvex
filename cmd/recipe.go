package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/recipe"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/spf13/cobra"
)

var recipeCmd = &cobra.Command{
	Use:   "recipe <name>",
	Short: "Compile a declarative workflow recipe into a runnable task DAG",
	Long: "Read .corvex/recipes/<name>.yaml, compile its stages into " +
		".corvex/tasks/<name>/tasks.md (a fixed DAG), and skip AI planning. " +
		"Then run it with `corvex run <name>`.",
	Args: cobra.ExactArgs(1),
	RunE: runRecipe,
}

func init() {
	rootCmd.AddCommand(recipeCmd)
}

func runRecipe(_ *cobra.Command, args []string) error {
	name := args[0]

	_, workDir, err := loadConfig()
	if err != nil {
		return err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	recipePath := filepath.Join(workDir, ".corvex", "recipes", name+".yaml")
	data, err := os.ReadFile(recipePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("recipe not found: %s (create a recipe YAML there, then re-run)", recipePath)
		}
		return fmt.Errorf("reading recipe %s: %w", recipePath, err)
	}

	r, err := recipe.Parse(data)
	if err != nil {
		return err
	}
	tasks, dag, err := r.Compile()
	if err != nil {
		return err
	}

	projDir := projectDir(workDir, name)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		return fmt.Errorf("creating project dir %s: %w", projDir, err)
	}
	tasksPath := filepath.Join(projDir, "tasks.md")
	if err := task.WriteTasksFile(tasksPath, tasks, dag); err != nil {
		return fmt.Errorf("writing tasks.md: %w", err)
	}

	log.Info("compiled recipe", "name", name, "stages", len(tasks), "tasks", tasksPath)
	fmt.Printf("Compiled recipe %q → %d task(s) at %s\n", name, len(tasks), tasksPath)
	fmt.Printf("Next: corvex run %s\n", name)
	return nil
}
