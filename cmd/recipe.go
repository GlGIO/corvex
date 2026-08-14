package cmd

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/ops"
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

	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	tasksPath, count, err := ops.CompileRecipe(workDir, name)
	if err != nil {
		// A missing recipe is the one failure worth answering with a hint about
		// what to write and where.
		var notFound *ops.RecipeNotFoundError
		if errors.As(err, &notFound) {
			return fmt.Errorf("recipe not found: %s (create a recipe YAML there, then re-run)", notFound.Path)
		}
		return err
	}

	log.Info("compiled recipe", "name", name, "stages", count, "tasks", tasksPath)
	fmt.Printf("Compiled recipe %q → %d task(s) at %s\n", name, count, tasksPath)
	fmt.Printf("Next: corvex run %s\n", name)
	return nil
}
