// Package anchor manages persistent state across task executions,
// tracking completed work and providing context for subsequent tasks.
package anchor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/types"
	"gopkg.in/yaml.v3"
)

// Load reads anchor.yaml from path. Missing file returns empty AnchorState, nil error.
// Empty file returns empty AnchorState, nil error. Invalid YAML returns error.
func Load(path string) (types.AnchorState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return types.AnchorState{}, nil
		}
		return types.AnchorState{}, fmt.Errorf("reading anchor %s: %w", path, err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return types.AnchorState{}, nil
	}

	var state types.AnchorState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return types.AnchorState{}, fmt.Errorf("parsing anchor %s: %w", path, err)
	}

	return state, nil
}

// Save marshals state to YAML and writes to path atomically (temp file + rename).
// Creates parent directories if needed.
func Save(path string, state types.AnchorState) error {
	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling anchor: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating anchor dir %s: %w", dir, err)
	}

	f, err := os.CreateTemp(dir, ".anchor-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temp anchor file: %w", err)
	}
	tmpName := f.Name()

	cleanup := func() { os.Remove(tmpName) }

	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return fmt.Errorf("writing anchor temp file: %w", err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		cleanup()
		return fmt.Errorf("syncing anchor temp file: %w", err)
	}

	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing anchor temp file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("renaming anchor temp to %s: %w", path, err)
	}

	return nil
}

// TaskResult carries the delta after a successful task completion.
type TaskResult struct {
	Completed       types.CompletedTask
	NextTask        string
	NextTaskContext string
	TotalTasks      int
}

// Update returns a new AnchorState with the task result applied.
// Pure function, no I/O. Sets UpdatedAt to current UTC time.
func Update(state types.AnchorState, tr TaskResult) types.AnchorState {
	out := state
	out.Completed = make([]types.CompletedTask, len(state.Completed), len(state.Completed)+1)
	copy(out.Completed, state.Completed)
	out.Completed = append(out.Completed, tr.Completed)

	out.CurrentState.CompletedTasks = len(out.Completed)
	out.NextTask = tr.NextTask
	out.NextTaskContext = tr.NextTaskContext

	if tr.TotalTasks > 0 {
		out.CurrentState.TotalTasks = tr.TotalTasks
	}

	out.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	return out
}

// GenerateContext builds a human-readable string for Worker prompt injection.
// taskID is the task about to run; used to decide whether to include NextTaskContext.
func GenerateContext(state types.AnchorState, taskID string) string {
	var b strings.Builder

	if state.Intent != "" {
		b.WriteString("## Project Intent\n\n")
		b.WriteString(state.Intent)
		b.WriteString("\n\n")
	}

	if len(state.Completed) > 0 {
		b.WriteString("## Completed Work\n\n")
		for _, c := range state.Completed {
			fmt.Fprintf(&b, "### %s — %s\n\n", c.ID, c.Title)
			if c.Summary != "" {
				b.WriteString(c.Summary)
				b.WriteString("\n\n")
			}
			if len(c.FilesCreated) > 0 || len(c.FilesModified) > 0 {
				b.WriteString("**Files:**\n")
				for _, f := range c.FilesCreated {
					fmt.Fprintf(&b, "- Created: `%s`\n", f)
				}
				for _, f := range c.FilesModified {
					fmt.Fprintf(&b, "- Modified: `%s`\n", f)
				}
				b.WriteString("\n")
			}
			if len(c.Decisions) > 0 {
				b.WriteString("**Decisions:**\n")
				for _, d := range c.Decisions {
					fmt.Fprintf(&b, "- %s\n", d)
				}
				b.WriteString("\n")
			}
		}
	}

	if taskID == state.NextTask && state.NextTaskContext != "" {
		b.WriteString("## Handoff Context\n\n")
		b.WriteString(state.NextTaskContext)
		b.WriteString("\n")
	}

	return b.String()
}

// SpecHash returns hex-encoded SHA-256 of spec file at specPath.
func SpecHash(specPath string) (string, error) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return "", fmt.Errorf("reading spec %s: %w", specPath, err)
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
