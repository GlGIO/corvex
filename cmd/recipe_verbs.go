package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

func runRecipeList(_ *cobra.Command, _ []string) error {
	workDir, err := workspaceDir()
	if err != nil {
		return err
	}
	recipes := ops.ListRecipes(workDir)
	if *recipeListJSON {
		return printJSON(os.Stdout, recipes)
	}
	if len(recipes) == 0 {
		fmt.Printf("No recipes found. Write one at %s/<name>.yaml\n", ops.RecipesDir(workDir))
		return nil
	}
	for _, r := range recipes {
		fmt.Printf("  %-24s", r.Name)
		if r.Problem != "" {
			fmt.Printf(" broken — %s\n", firstLine(r.Problem))
			continue
		}
		fmt.Printf(" %d stage(s), %d gate(s)", r.Stages, r.Gates)
		if r.Compiled {
			fmt.Print("  ·  compiled")
		}
		if r.Description != "" {
			fmt.Printf("  ·  %s", r.Description)
		}
		fmt.Println()
	}
	return nil
}

func runRecipeShow(_ *cobra.Command, args []string) error {
	workDir, err := workspaceDir()
	if err != nil {
		return err
	}
	detail, err := ops.ShowRecipe(workDir, args[0])
	if err != nil {
		return recipeError(err)
	}
	if *recipeShowJSON {
		return printJSON(os.Stdout, detail)
	}
	renderRecipeDetail(detail)
	return nil
}

func runRecipeValidate(_ *cobra.Command, args []string) error {
	workDir, err := workspaceDir()
	if err != nil {
		return err
	}
	tasks, err := ops.ValidateRecipe(workDir, args[0])
	if err != nil {
		return recipeError(err)
	}
	fmt.Printf("Recipe %q is valid — compiles to %d task(s). Nothing was written.\n", args[0], tasks)
	return nil
}

func runRecipeCompile(_ *cobra.Command, args []string) error {
	return compileRecipeInto(args[0])
}

// recipeError answers a missing recipe with the hint the legacy command already
// gave, so the noun does not lose the one message worth keeping.
func recipeError(err error) error {
	var notFound *ops.RecipeNotFoundError
	if errors.As(err, &notFound) {
		return fmt.Errorf("recipe not found: %s (create a recipe YAML there, then re-run)", notFound.Path)
	}
	return err
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
