package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/giovannialves/corvex/internal/types"
)

// maxStreamLines bounds the worker panel's retained stream lines so a long
// task can't grow memory (and viewport content) without limit. Older lines
// slide out of the window.
const maxStreamLines = 2000

// WorkerPanel renders stream output from Worker or Reviewer in a scrollable viewport.
type WorkerPanel struct {
	viewport   viewport.Model
	lines      []string
	activeTask string
	phase      string // "worker" or "review"
	width      int
	height     int
	autoScroll bool
}

// NewWorkerPanel creates an empty worker panel with auto-scroll enabled.
func NewWorkerPanel() WorkerPanel {
	vp := viewport.New(0, 0)
	return WorkerPanel{
		viewport:   vp,
		autoScroll: true,
		phase:      "worker",
	}
}

// Update delegates viewport key/mouse handling.
func (w WorkerPanel) Update(msg tea.Msg) (WorkerPanel, tea.Cmd) {
	var cmd tea.Cmd
	w.viewport, cmd = w.viewport.Update(msg)

	if w.viewport.AtBottom() {
		w.autoScroll = true
	} else if _, ok := msg.(tea.KeyMsg); ok {
		w.autoScroll = false
	}

	return w, cmd
}

// View renders the panel header (phase + active task as a chip) followed
// by the scrollable stream content.
func (w WorkerPanel) View() string {
	header := w.renderHeader()
	return header + "\n" + w.viewport.View()
}

func (w WorkerPanel) renderHeader() string {
	phase := w.phase
	if phase == "" {
		phase = "worker"
	}
	chip := Chip.Render(phase)
	if w.activeTask == "" {
		return TextMuted.Render(" ") + chip
	}
	return " " + chip + " " + TextMuted.Render(w.activeTask)
}

// SetSize adjusts the panel and its viewport dimensions.
func (w WorkerPanel) SetSize(width, height int) WorkerPanel {
	w.width = width
	w.height = height

	// viewport height minus header (1 line)
	vpHeight := height - 1
	if vpHeight < 1 {
		vpHeight = 1
	}
	vpWidth := width - 2
	if vpWidth < 10 {
		vpWidth = 10
	}
	w.viewport.Width = vpWidth
	w.viewport.Height = vpHeight
	w.syncContent()
	return w
}

// AppendStream adds a formatted stream event to the output. Tool calls
// and their results use ›/↳ glyphs instead of emoji icons; text events
// are rendered plain to keep the stream low-noise.
func (w WorkerPanel) AppendStream(ev *types.StreamEvent) WorkerPanel {
	if ev == nil {
		return w
	}

	var line string
	switch ev.Type {
	case types.EventToolUse:
		tool := strings.TrimSpace(ev.Tool)
		body := strings.TrimSpace(ev.Content)
		if ev.File != "" {
			body = ev.File
		}
		switch {
		case tool != "" && body != "":
			line = fmt.Sprintf("  %s %s  %s",
				StreamTool.Render(GlyphToolUse),
				StreamTool.Bold(true).Render(tool),
				TextMuted.Render(truncate(body, 200)),
			)
		case tool != "":
			line = fmt.Sprintf("  %s %s",
				StreamTool.Render(GlyphToolUse),
				StreamTool.Bold(true).Render(tool),
			)
		default:
			line = fmt.Sprintf("  %s %s",
				StreamTool.Render(GlyphToolUse),
				StreamTool.Render(truncate(body, 200)),
			)
		}
	case types.EventToolResult:
		line = fmt.Sprintf("  %s %s",
			StreamResult.Render(GlyphToolDone),
			StreamResult.Render(truncate(ev.Content, 200)),
		)
	case types.EventText:
		line = "  " + StreamText.Render(ev.Content)
	case types.EventError:
		line = fmt.Sprintf("  %s %s",
			StreamError.Render(GlyphFailed),
			StreamError.Render(ev.Content),
		)
	default:
		if ev.File != "" {
			line = fmt.Sprintf("  %s %s",
				StreamFile.Render(GlyphBullet),
				StreamFile.Render(ev.File),
			)
		} else {
			line = "  " + StreamText.Render(ev.Content)
		}
	}

	lines := make([]string, len(w.lines)+1)
	copy(lines, w.lines)
	lines[len(w.lines)] = line
	// Ring buffer: keep only the most recent maxStreamLines entries.
	if len(lines) > maxStreamLines {
		lines = lines[len(lines)-maxStreamLines:]
	}
	w.lines = lines

	w.syncContent()
	return w
}

// SetActiveTask updates the current task label and phase chip.
func (w WorkerPanel) SetActiveTask(taskID, phase string) WorkerPanel {
	w.activeTask = taskID
	w.phase = strings.ToLower(phase)
	return w
}

// Clear resets lines and viewport content.
func (w WorkerPanel) Clear() WorkerPanel {
	w.lines = nil
	w.viewport.SetContent("")
	w.viewport.GotoTop()
	w.autoScroll = true
	return w
}

func (w *WorkerPanel) syncContent() {
	content := strings.Join(w.lines, "\n")
	w.viewport.SetContent(content)
	if w.autoScroll {
		w.viewport.GotoBottom()
	}
}

// truncate shortens s to at most max display columns, appending "…" when it
// cuts. It is rune- and ANSI-aware: it never splits a multibyte rune or an
// ANSI escape sequence, and it counts visible width (not bytes), so styled
// and CJK/emoji text truncate correctly.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= max {
		return s
	}
	return ansi.Truncate(s, max, "…")
}
