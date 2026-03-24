package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/types"
)

func loadConfig() (*config.Config, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("getting working directory: %w", err)
	}

	configPath := filepath.Join(wd, ".corvex", "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return config.Default(), wd, nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("loading config: %w", err)
	}

	return cfg, wd, nil
}

func projectDir(workDir, project string) string {
	return filepath.Join(workDir, ".corvex", "tasks", project)
}

func requireCorvexDir(workDir string) error {
	corvexDir := filepath.Join(workDir, ".corvex")
	if _, err := os.Stat(corvexDir); os.IsNotExist(err) {
		return fmt.Errorf(".corvex directory not found — run 'corvex init' first")
	}
	return nil
}

func statusEmoji(s types.TaskStatus) string {
	switch s {
	case types.StatusPending:
		return "⬜"
	case types.StatusRunning:
		return "🔄"
	case types.StatusPassed:
		return "✅"
	case types.StatusFailed:
		return "❌"
	case types.StatusSkipped:
		return "⏭️"
	default:
		return "?"
	}
}
