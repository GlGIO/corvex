package ops

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/giovannialves/corvex/internal/config"
)

// LoadConfig resolves the workspace from the current working directory and
// loads its configuration. It returns the config and the workspace directory.
func LoadConfig() (*config.Config, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("getting working directory: %w", err)
	}

	cfg, err := LoadConfigAt(wd)
	if err != nil {
		return nil, "", err
	}

	return cfg, wd, nil
}

// LoadConfigAt loads .corvex/config.yaml under workDir, falling back to
// config.Default() when the file does not exist. Callers that already know the
// workspace (a server handling a request for a repo) use this instead of
// LoadConfig, which depends on the process working directory.
func LoadConfigAt(workDir string) (*config.Config, error) {
	configPath := filepath.Join(workDir, ".corvex", "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return config.Default(), nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	return cfg, nil
}
