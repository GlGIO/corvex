package wizard

import (
	"fmt"
	"strconv"

	"github.com/giovannialves/corvex/internal/config"
)

// ManualOverride walks the nine editable fields in a fixed order, offering the
// current value as the default of each prompt. stack.env_file is deliberately
// not among them.
func (w *Wizard) ManualOverride(v *config.ValidateConfig) {
	fmt.Fprintln(w.out, "\nManual edit — Enter to keep current value:")
	v.Stack.Runtime = w.Prompt("  stack.runtime", v.Stack.Runtime)
	v.Stack.Framework = w.Prompt("  stack.framework", v.Stack.Framework)
	v.Stack.StartCommand = w.Prompt("  stack.start_command", v.Stack.StartCommand)
	v.Stack.Port = parseIntDefault(w.Prompt("  stack.port", strconv.Itoa(v.Stack.Port)), v.Stack.Port)
	v.Stack.HealthPath = w.Prompt("  stack.health_path", v.Stack.HealthPath)
	v.Database.Type = w.Prompt("  database.type", v.Database.Type)
	v.Database.Image = w.Prompt("  database.image", v.Database.Image)
	v.Database.MigrateCommand = w.Prompt("  database.migrate_command", v.Database.MigrateCommand)
	uiAnswer := w.Prompt("  ui.enabled (y/n)", boolStr(v.UI.Enabled))
	v.UI.Enabled = uiAnswer == "y" || uiAnswer == "yes" || uiAnswer == "true"
}

// ApplyFieldOverride writes value to v at the given dotted path (e.g.
// "stack.port"). An unknown path is ignored.
func ApplyFieldOverride(v *config.ValidateConfig, field, value string) {
	switch field {
	case "stack.runtime":
		v.Stack.Runtime = value
	case "stack.framework":
		v.Stack.Framework = value
	case "stack.start_command":
		v.Stack.StartCommand = value
	case "stack.port":
		v.Stack.Port = parseIntDefault(value, v.Stack.Port)
	case "stack.health_path":
		v.Stack.HealthPath = value
	case "stack.ready_timeout":
		v.Stack.ReadyTimeout = parseIntDefault(value, v.Stack.ReadyTimeout)
	case "database.type":
		v.Database.Type = value
	case "database.image":
		v.Database.Image = value
	case "database.migrate_command":
		v.Database.MigrateCommand = value
	case "ui.enabled":
		v.UI.Enabled = value == "y" || value == "yes" || value == "true"
	}
}
