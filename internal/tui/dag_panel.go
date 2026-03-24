package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/giovannialves/corvex/internal/types"
)

// TaskEntry represents a single task row in the DAG panel.
type TaskEntry struct {
	ID        string
	Title     string
	Status    types.TaskStatus
	Duration  time.Duration
	Attempt   int
	StartedAt time.Time
}

// DAGPanel renders the left task list with status icons and scroll.
type DAGPanel struct {
	tasks      []TaskEntry
	cursor     int
	scrollOff  int
	width      int
	height     int
	totalTasks int
	completed  int
}

// NewDAGPanel creates an empty DAG panel.
func NewDAGPanel() DAGPanel {
	return DAGPanel{}
}

// Update handles keyboard navigation within the panel.
func (d DAGPanel) Update(msg tea.Msg) DAGPanel {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "up", "k":
			if d.cursor > 0 {
				d.cursor--
				if d.cursor < d.scrollOff {
					d.scrollOff = d.cursor
				}
			}
		case "down", "j":
			if d.cursor < len(d.tasks)-1 {
				d.cursor++
				visible := d.visibleRows()
				if d.cursor >= d.scrollOff+visible {
					d.scrollOff = d.cursor - visible + 1
				}
			}
		}
	}
	return d
}

// View renders the panel content (without border — border is applied by the parent).
func (d DAGPanel) View() string {
	if len(d.tasks) == 0 {
		return StatusPending.Render("No tasks loaded")
	}

	visible := d.visibleRows()
	if visible <= 0 {
		visible = len(d.tasks)
	}

	end := d.scrollOff + visible
	if end > len(d.tasks) {
		end = len(d.tasks)
	}

	contentWidth := d.width - 4 // border + padding
	if contentWidth < 20 {
		contentWidth = 20
	}

	var b strings.Builder
	header := CounterStyle.Render(fmt.Sprintf(" Tasks %d/%d", d.completed, d.totalTasks))
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(DividerStyle.Render(strings.Repeat("─", contentWidth)))
	b.WriteString("\n")

	for i := d.scrollOff; i < end; i++ {
		t := d.tasks[i]
		emoji := StatusEmoji(t.Status)

		title := t.Title
		maxTitle := contentWidth - 20
		if maxTitle < 8 {
			maxTitle = 8
		}
		if len(title) > maxTitle {
			title = title[:maxTitle-1] + "…"
		}

		dur := formatTaskDuration(t)

		line := fmt.Sprintf("%s %s %s %s", emoji, t.ID, title, dur)

		if i == d.cursor {
			line = CursorStyle.Render(line)
		} else {
			line = styleForStatus(t.Status).Render(line)
		}

		b.WriteString(line)
		if i < end-1 {
			b.WriteString("\n")
		}
	}

	if end < len(d.tasks) {
		b.WriteString("\n")
		b.WriteString(KeyHint.Render(fmt.Sprintf("  ↓ %d more", len(d.tasks)-end)))
	}

	return b.String()
}

// SetSize adjusts the panel dimensions.
func (d DAGPanel) SetSize(w, h int) DAGPanel {
	d.width = w
	d.height = h
	return d
}

// UpdateTask modifies a single task's status, duration, and attempt count.
func (d DAGPanel) UpdateTask(id string, status types.TaskStatus, duration time.Duration, attempt int) DAGPanel {
	tasks := make([]TaskEntry, len(d.tasks))
	copy(tasks, d.tasks)
	for i := range tasks {
		if tasks[i].ID == id {
			tasks[i].Status = status
			tasks[i].Duration = duration
			tasks[i].Attempt = attempt
			if status == types.StatusRunning && tasks[i].StartedAt.IsZero() {
				tasks[i].StartedAt = time.Now()
			}
			break
		}
	}
	d.tasks = tasks
	return d
}

// AddTasks appends new task entries to the panel.
func (d DAGPanel) AddTasks(entries []TaskEntry) DAGPanel {
	tasks := make([]TaskEntry, len(d.tasks)+len(entries))
	copy(tasks, d.tasks)
	copy(tasks[len(d.tasks):], entries)
	d.tasks = tasks
	return d
}

// SetProgress updates the completed/total counters.
func (d DAGPanel) SetProgress(completed, total int) DAGPanel {
	d.completed = completed
	d.totalTasks = total
	return d
}

// SelectedTaskID returns the task ID under the cursor.
func (d DAGPanel) SelectedTaskID() string {
	if d.cursor >= 0 && d.cursor < len(d.tasks) {
		return d.tasks[d.cursor].ID
	}
	return ""
}

func (d DAGPanel) visibleRows() int {
	// height minus header (1) minus divider (1)
	rows := d.height - 2
	if rows < 1 {
		rows = len(d.tasks)
	}
	return rows
}

func formatTaskDuration(t TaskEntry) string {
	switch t.Status {
	case types.StatusRunning:
		if !t.StartedAt.IsZero() {
			return lipgloss.NewStyle().Foreground(cyan).Render(FormatDuration(time.Since(t.StartedAt)))
		}
		return lipgloss.NewStyle().Foreground(cyan).Render("...")
	case types.StatusPassed, types.StatusFailed:
		return FormatDuration(t.Duration)
	default:
		return ""
	}
}

func styleForStatus(s types.TaskStatus) lipgloss.Style {
	switch s {
	case types.StatusPassed:
		return StatusPassed
	case types.StatusRunning:
		return StatusRunning
	case types.StatusFailed:
		return StatusFailed
	case types.StatusSkipped:
		return StatusSkippedStyle
	default:
		return StatusPending
	}
}
