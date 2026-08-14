package wizard

import (
	"fmt"

	"github.com/giovannialves/corvex/internal/config"
)

// Manual is the fallback questionnaire used when AI inference is unavailable.
// ready_timeout is never asked and is hardcoded to 30, and the POSTGRES_* env
// map is injected for postgres only.
func (w *Wizard) Manual(workDir string, cfg *config.Config) error {
	cfg.Validate.Stack.Runtime = w.Prompt("Backend runtime? (node/python/go/java)", "node")
	cfg.Validate.Stack.Framework = w.Prompt("Framework? (nestjs/express/fastapi/...)", "")
	cfg.Validate.Stack.StartCommand = w.Prompt("Start command?", "npm run start:test")
	cfg.Validate.Stack.Port = parseIntDefault(w.Prompt("Port?", "3000"), 3000)
	cfg.Validate.Stack.ReadyTimeout = 30
	cfg.Validate.Stack.HealthPath = w.Prompt("Health check path? (empty to skip)", "/health")

	dbType := w.Prompt("Database? (postgres/mysql/sqlite/none)", "postgres")
	cfg.Validate.Database.Type = dbType
	if dbType != "none" && dbType != "sqlite" && dbType != "" {
		defaultImage := dbType + ":latest"
		if dbType == "postgres" {
			defaultImage = "postgres:16"
		} else if dbType == "mysql" {
			defaultImage = "mysql:8"
		}
		cfg.Validate.Database.Image = w.Prompt("DB docker image?", defaultImage)
		cfg.Validate.Database.MigrateCommand = w.Prompt("Migrate command?", "npm run migrate")
		if dbType == "postgres" {
			cfg.Validate.Database.Env = map[string]string{
				"POSTGRES_DB":       "testdb",
				"POSTGRES_USER":     "test",
				"POSTGRES_PASSWORD": "test",
			}
		}
	}

	uiAnswer := w.Prompt("Test UI with Chrome CDP? (y/n)", "n")
	cfg.Validate.UI.Enabled = uiAnswer == "y" || uiAnswer == "yes"

	if err := SaveConfig(workDir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(w.out, "\n✓ Written to .corvex/config.yaml\n\n")
	return nil
}
