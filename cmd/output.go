package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/giovannialves/corvex/internal/types"
)

// statusEmoji is the terminal glyph for a task status.
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

// printJSON marshals v as indented JSON and writes it to w followed by a
// newline. Human/log output must be suppressed before calling this.
func printJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling JSON: %w", err)
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}
