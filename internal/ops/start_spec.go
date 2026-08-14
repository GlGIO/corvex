package ops

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteMinimalSpec creates a bare spec.md for project from a user-supplied
// description, creating the parent directory when it is missing.
//
// This is the shortcut `corvex start` takes in grill mode: just enough
// structure for the Griller to interrogate, with none of the brainstorm
// interview in front of it.
func WriteMinimalSpec(specPath, project, description string) error {
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		return fmt.Errorf("creating spec directory: %w", err)
	}
	content := fmt.Sprintf("# %s\n\n## Objective\n\n%s\n", project, description)
	if err := os.WriteFile(specPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing spec.md: %w", err)
	}
	return nil
}
