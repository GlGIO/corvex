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

// DAGPanel renders the task list with status glyphs and supports
// keyboard navigation, scrolling, and an optional case-insensitive filter
// applied to ID and Title.
type DAGPanel struct {
	tasks      []TaskEntry
	filter     string
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

// Update handles keyboard navigation within the panel. Filter mode is
// driven from the parent model (which captures keystrokes into the
// status bar); the panel only renders the filtered view.
func (d DAGPanel) Update(msg tea.Msg) DAGPanel {
	if km, ok := msg.(tea.KeyMsg); ok {
		visible := d.visibleIndexes()
		switch km.String() {
		case "up", "k":
			if d.cursor > 0 {
				d.cursor--
				if d.cursor < d.scrollOff {
					d.scrollOff = d.cursor
				}
			}
		case "down", "j":
			if d.cursor < len(visible)-1 {
				d.cursor++
				rows := d.visibleRows()
				if d.cursor >= d.scrollOff+rows {
					d.scrollOff = d.cursor - rows + 1
				}
			}
		}
	}
	return d
}

// View renders the panel content (without a border — the parent draws
// dividers around it).
func (d DAGPanel) View() string {
	visible := d.visibleIndexes()
	if len(visible) == 0 {
		if d.filter != "" {
			return TextFaint.Render(fmt.Sprintf("  no tasks match %q", d.filter))
		}
		return TextFaint.Render("  no tasks loaded")
	}

	rows := d.visibleRows()
	if rows <= 0 {
		rows = len(visible)
	}

	end := d.scrollOff + rows
	if end > len(visible) {
		end = len(visible)
	}

	contentWidth := d.width - 2
	if contentWidth < 30 {
		contentWidth = 30
	}

	var b strings.Builder
	for i := d.scrollOff; i < end; i++ {
		t := d.tasks[visible[i]]
		b.WriteString(renderDAGRow(t, i == d.cursor, contentWidth))
		if i < end-1 {
			b.WriteString("\n")
		}
	}

	if end < len(visible) {
		b.WriteString("\n")
		b.WriteString(TextFaint.Render(fmt.Sprintf("  ↓ %d more", len(visible)-end)))
	}

	return b.String()
}

func renderDAGRow(t TaskEntry, selected bool, width int) string {
	glyph := StatusGlyph(t.Status)

	// Compose: " G  ID  Title …………………………… status/duration "
	idCol := fmt.Sprintf("%-4s", t.ID)
	tail := rowTail(t)

	// Reserve room for: 1 (gutter) + 1 (glyph) + 1 (space) + len(idCol) +
	// 2 (space) + len(tail) + 1 (gutter).
	tailLen := lipgloss.Width(tail)
	titleMax := width - (1 + 1 + 1 + len(idCol) + 2 + tailLen + 1)
	if titleMax < 8 {
		titleMax = 8
	}
	title := t.Title
	if lipgloss.Width(title) > titleMax {
		title = title[:titleMax-1] + "…"
	}

	pad := titleMax - lipgloss.Width(title)
	if pad < 0 {
		pad = 0
	}

	statusStyle := StatusStyle(t.Status)
	row := fmt.Sprintf(" %s  %s  %s%s  %s",
		statusStyle.Render(glyph),
		TextMuted.Render(idCol),
		statusStyle.Render(title),
		strings.Repeat(" ", pad),
		tail,
	)

	if selected {
		return CursorStyle.Render(row)
	}
	return row
}

func rowTail(t TaskEntry) string {
	switch t.Status {
	case types.StatusRunning:
		if !t.StartedAt.IsZero() {
			return StatusRunning.Render(fmt.Sprintf("running · %s", FormatDuration(time.Since(t.StartedAt))))
		}
		return StatusRunning.Render("running")
	case types.StatusPassed:
		return StatusPassed.Render(FormatDuration(t.Duration))
	case types.StatusFailed:
		return StatusFailed.Render(fmt.Sprintf("failed · %s", FormatDuration(t.Duration)))
	case types.StatusSkipped:
		return StatusSkippedStyle.Render("skipped")
	default:
		return TextFaint.Render("pending")
	}
}

// SetSize adjusts the panel dimensions.
func (d DAGPanel) SetSize(w, h int) DAGPanel {
	d.width = w
	d.height = h
	return d
}

// SetFilter applies a case-insensitive substring filter on task ID and
// title. Passing an empty string clears the filter.
func (d DAGPanel) SetFilter(s string) DAGPanel {
	d.filter = s
	if d.cursor >= len(d.visibleIndexes()) {
		d.cursor = 0
		d.scrollOff = 0
	}
	return d
}

// Filter returns the current filter (empty when not filtered).
func (d DAGPanel) Filter() string {
	return d.filter
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

// SelectedTaskID returns the task ID under the cursor (within the
// currently visible set).
func (d DAGPanel) SelectedTaskID() string {
	visible := d.visibleIndexes()
	if d.cursor >= 0 && d.cursor < len(visible) {
		return d.tasks[visible[d.cursor]].ID
	}
	return ""
}

// SelectedTask returns the full TaskEntry under the cursor, or nil if the
// panel is empty / filter cleared the view.
func (d DAGPanel) SelectedTask() *TaskEntry {
	visible := d.visibleIndexes()
	if d.cursor >= 0 && d.cursor < len(visible) {
		t := d.tasks[visible[d.cursor]]
		return &t
	}
	return nil
}

// Tasks returns the unfiltered task list, used by callers needing the full
// set (e.g. modal detail or progress counters).
func (d DAGPanel) Tasks() []TaskEntry {
	return d.tasks
}

func (d DAGPanel) visibleRows() int {
	rows := d.height
	if rows < 1 {
		rows = len(d.tasks)
	}
	return rows
}

func (d DAGPanel) visibleIndexes() []int {
	if d.filter == "" {
		idx := make([]int, len(d.tasks))
		for i := range d.tasks {
			idx[i] = i
		}
		return idx
	}
	needle := strings.ToLower(d.filter)
	var out []int
	for i, t := range d.tasks {
		if strings.Contains(strings.ToLower(t.ID), needle) ||
			strings.Contains(strings.ToLower(t.Title), needle) {
			out = append(out, i)
		}
	}
	return out
}
