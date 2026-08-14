package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/ops"
	"github.com/giovannialves/corvex/internal/provider"
)

// runStartMode asks how defined the feature is and runs the matching path. The
// provider is built BEFORE the menu is shown: a misconfigured provider should
// fail before the user answers questions, not after.
func runStartMode(ctx context.Context, cfg *config.Config, workDir, project string, reader *bufio.Reader) error {
	pDir := ops.ProjectDir(workDir, project)
	specPath := filepath.Join(pDir, "spec.md")
	decisionsPath := filepath.Join(pDir, "decisions.md")
	qaPath := filepath.Join(pDir, "brainstorm-qa.md")

	_, specErr := os.Stat(specPath)
	specExists := specErr == nil

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}
	model := cfg.Provider.Models.Planner

	switch mode := promptMode(reader, specExists); mode {
	case "brainstorm":
		return brainstormPath(ctx, p, model, workDir, project, specPath, decisionsPath, qaPath, reader)
	case "grill":
		return grillPath(ctx, p, model, workDir, project, specPath, decisionsPath, reader)
	case "plan":
		return planPath(ctx, p, model, workDir, project, specPath, decisionsPath, reader)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

// promptMode renders the entry-point menu and maps the keystroke to a mode.
// Option 3 is only offered — and only accepted — when spec.md already exists.
func promptMode(reader *bufio.Reader, specExists bool) string {
	fmt.Println()
	fmt.Println("corvex: How defined is this feature?")
	fmt.Println()
	fmt.Println("  1) brainstorm — vague idea; AI will ask questions and write spec.md")
	fmt.Println("  2) grill      — clear idea; describe it quickly, then AI interrogates spec")
	if specExists {
		fmt.Println("  3) plan       — spec.md already exists; skip straight to grill → plan")
	}
	fmt.Println()
	fmt.Print("Choice [1]: ")

	raw, err := reader.ReadString('\n')
	if err != nil {
		return "brainstorm"
	}
	switch strings.TrimSpace(raw) {
	case "2":
		return "grill"
	case "3":
		if specExists {
			return "plan"
		}
		return "brainstorm"
	default:
		return "brainstorm"
	}
}
