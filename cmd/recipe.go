package cmd

import (
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
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeRecipeArg,
	RunE:              runRecipe,
}

func init() {
	rootCmd.AddCommand(recipeCmd)
}

// runRecipe is the bare form `corvex recipe <name>`, which compiles. F3
// deprecated the FORM, not the command: the verb is `recipe compile`. The bytes
// it prints are unchanged, and the notice only reaches a terminal — same rule as
// every other deprecation in this phase, and the same reason (the golden network
// records stdout and stderr together).
func runRecipe(_ *cobra.Command, args []string) error {
	noticeDeprecatedForm("recipe <name>", "corvex recipe compile <name>")
	return compileRecipeInto(args[0])
}

// compileRecipeInto is the one implementation both forms call, so the deprecated
// spelling cannot drift from the verb.
func compileRecipeInto(name string) error {
	workDir, err := workspaceDir()
	if err != nil {
		return err
	}

	tasksPath, count, err := ops.CompileRecipe(workDir, name)
	if err != nil {
		// A missing recipe is the one failure worth answering with a hint about
		// what to write and where.
		return recipeError(err)
	}

	log.Info("compiled recipe", "name", name, "stages", count, "tasks", tasksPath)
	fmt.Printf("Compiled recipe %q → %d task(s) at %s\n", name, count, tasksPath)
	fmt.Printf("Next: corvex run %s\n", name)
	return nil
}
