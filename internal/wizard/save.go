package wizard

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/giovannialves/corvex/internal/config"
	"gopkg.in/yaml.v3"
)

// SaveConfig writes cfg to <workDir>/.corvex/config.yaml. The directory is not
// created: a missing .corvex is reported as an error, which is how the wizard
// refuses to configure a workspace that was never initialised.
func SaveConfig(workDir string, cfg *config.Config) error {
	configPath := filepath.Join(workDir, ".corvex", "config.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	return os.WriteFile(configPath, data, 0o644)
}
