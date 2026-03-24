package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/templates"
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

	corvexDir := filepath.Join(wd, ".corvex")
	if _, err := os.Stat(corvexDir); err == nil {
		return fmt.Errorf(".corvex/ already exists in %s", wd)
	}

	dirs := []string{
		".corvex",
		".corvex/agents",
		".corvex/context",
		".corvex/hooks",
		".corvex/templates",
		".corvex/tasks",
	}

	for _, dir := range dirs {
		path := filepath.Join(wd, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	fileMappings := map[string]string{
		"config.yaml":       ".corvex/config.yaml",
		"agents/default.md": ".corvex/agents/default.md",
		"context/README.md": ".corvex/context/README.md",
	}

	for src, dst := range fileMappings {
		data, err := fs.ReadFile(templates.FS, src)
		if err != nil {
			return fmt.Errorf("reading template %s: %w", src, err)
		}
		dstPath := filepath.Join(wd, dst)
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", dst, err)
		}
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
