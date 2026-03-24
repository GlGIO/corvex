package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/giovannialves/corvex/internal/types"
)

// WorkerPanel renders stream output from Worker or Reviewer in a scrollable viewport.
type WorkerPanel struct {
	viewport   viewport.Model
	lines      []string
	activeTask string
	phase      string // "Worker" or "Reviewer"
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
		phase:      "Worker",
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

// View renders the panel header and viewport content.
func (w WorkerPanel) View() string {
	header := CounterStyle.Render(fmt.Sprintf(" %s: %s", w.phase, w.activeTask))

	contentWidth := w.width - 4
	if contentWidth < 10 {
		contentWidth = 10
	}
	divider := DividerStyle.Render(strings.Repeat("─", contentWidth))

	return header + "\n" + divider + "\n" + w.viewport.View()
}

// SetSize adjusts the panel and its viewport dimensions.
func (w WorkerPanel) SetSize(width, height int) WorkerPanel {
	w.width = width
	w.height = height

	// viewport height minus header (1) and divider (1)
	vpHeight := height - 2
	if vpHeight < 1 {
		vpHeight = 1
	}
	vpWidth := width - 4 // border + padding
	if vpWidth < 10 {
		vpWidth = 10
	}
	w.viewport.Width = vpWidth
	w.viewport.Height = vpHeight
	w.syncContent()
	return w
}

// AppendStream adds a formatted stream event to the output.
func (w WorkerPanel) AppendStream(ev *types.StreamEvent) WorkerPanel {
	if ev == nil {
		return w
	}

	var line string
	switch ev.Type {
	case types.EventToolUse:
		icon := ToolIcon(ev.Tool)
		line = StreamTool.Render(fmt.Sprintf("%s %s", icon, ev.Content))
	case types.EventToolResult:
		line = StreamResult.Render(fmt.Sprintf("  → %s", truncate(ev.Content, 120)))
	case types.EventText:
		line = StreamText.Render(fmt.Sprintf("💬 %s", ev.Content))
	default:
		if ev.File != "" {
			line = StreamFile.Render(fmt.Sprintf("📄 %s", ev.File))
		} else {
			line = StreamText.Render(ev.Content)
		}
	}

	lines := make([]string, len(w.lines)+1)
	copy(lines, w.lines)
	lines[len(w.lines)] = line
	w.lines = lines

	w.syncContent()
	return w
}

// SetActiveTask updates the current task label and phase.
func (w WorkerPanel) SetActiveTask(taskID, phase string) WorkerPanel {
	w.activeTask = taskID
	w.phase = phase
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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
