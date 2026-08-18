package cmd

import (
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

// The `recipe` noun (F3, §2). Unlike the legacy commands, `recipe` is not
// hidden: it becomes the noun. What is deprecated is its bare FORM — `corvex
// recipe <name>` compiling — which cannot be hidden without hiding the noun, so
// it warns from inside its own RunE (see runRecipe).
var (
	recipeListCmd = &cobra.Command{
		Use:   "list",
		Short: "List the recipes of this repository",
		Args:  cobra.NoArgs,
		RunE:  runRecipeList,
	}

	recipeShowCmd = &cobra.Command{
		Use:               "show <name>",
		Short:             "Show a recipe's stages, gates and evidence",
		Long:              "Read a recipe and describe it. It never writes: tasks.md is state, and inspecting a plan must not reset one.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeRecipeArg,
		RunE:              runRecipeShow,
	}

	recipeValidateCmd = &cobra.Command{
		Use:               "validate <name>",
		Short:             "Parse, validate and compile a recipe in memory",
		Long:              "Check a recipe without writing anything. Compiling is part of the check: a recipe can parse and still fail to become a DAG.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeRecipeArg,
		RunE:              runRecipeValidate,
	}

	recipeCompileCmd = &cobra.Command{
		Use:               "compile <name>",
		Short:             "Compile a recipe into a runnable task DAG",
		Long:              "Read .corvex/recipes/<name>.yaml and write .corvex/tasks/<name>/tasks.md, skipping AI planning.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeRecipeArg,
		RunE:              runRecipeCompile,
	}
)

var recipeListJSON, recipeShowJSON *bool

// completeRecipeArg completes a recipe name from the catalogue on disk.
func completeRecipeArg(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	_, workDir, err := ops.LoadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return ops.RecipeNames(workDir), cobra.ShellCompDirectiveNoFileComp
}

func init() {
	recipeListJSON = addJSONFlag(recipeListCmd)
	recipeShowJSON = addJSONFlag(recipeShowCmd)
	recipeCmd.AddCommand(recipeListCmd, recipeShowCmd, recipeValidateCmd, recipeCompileCmd)
}
