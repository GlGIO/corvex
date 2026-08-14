package cmd

import (
	"fmt"
	"os"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a Corvex project",
	Long:  "Scaffold a .corvex/ directory with default configuration, agents, context, and hooks.",
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(_ *cobra.Command, _ []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	corvexDir, err := ops.InitWorkspace(wd)
	if err != nil {
		return err
	}

	log.Info("initialized corvex project", "path", corvexDir)
	fmt.Println("\nCreated .corvex/ with:")
	fmt.Println("  config.yaml         — project configuration")
	fmt.Println("  agents/default.md   — default agent prompt")
	fmt.Println("  context/README.md   — context directory guide")
	fmt.Println("  hooks/              — lifecycle hooks (pre-task, post-task, ...)")
	fmt.Println("  tasks/              — task manifests")
	fmt.Println("\nNext: create a project spec in .corvex/tasks/<project>/spec.md")

	return nil
}
