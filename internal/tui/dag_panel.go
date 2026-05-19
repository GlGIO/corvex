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

	// Reserve one row for the "↑ N above" / "↓ N below" footer when we
	// actually need to render one — otherwise the footer line overflows past
	// the panel's declared height and visually corrupts the divider/worker
	// area beneath it.
	hasAbove := d.scrollOff > 0
	wantTaskRows := rows
	if hasAbove {
		wantTaskRows--
	}
	end := d.scrollOff + wantTaskRows
	if end > len(visible) {
		end = len(visible)
	}
	hasBelow := end < len(visible)
	if hasBelow {
		// Pull back one more row so the "↓ N more" line fits.
		end--
		if end < d.scrollOff {
			end = d.scrollOff
		}
		hasBelow = end < len(visible)
	}

	contentWidth := d.width - 2
	if contentWidth < 30 {
		contentWidth = 30
	}

	var b strings.Builder
	if hasAbove {
		fmt.Fprintf(&b, "%s\n", TextFaint.Render(fmt.Sprintf("  ↑ %d above", d.scrollOff)))
	}
	for i := d.scrollOff; i < end; i++ {
		t := d.tasks[visible[i]]
		b.WriteString(renderDAGRow(t, i == d.cursor, contentWidth))
		if i < end-1 || hasBelow {
			b.WriteString("\n")
		}
	}

	if hasBelow {
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

// ScrollToTask ensures the row containing the given task is visible in the
// panel viewport. The cursor follows the task only when it was previously
// parked on another running task (or hadn't moved from row 0) — if the user
// manually navigated to inspect something, we keep their cursor put.
func (d DAGPanel) ScrollToTask(id string) DAGPanel {
	visible := d.visibleIndexes()
	if len(visible) == 0 {
		return d
	}

	// Locate the task's position within the visible slice.
	targetVisible := -1
	for vi, ti := range visible {
		if d.tasks[ti].ID == id {
			targetVisible = vi
			break
		}
	}
	if targetVisible == -1 {
		return d // task hidden by filter; nothing to do
	}

	rows := d.visibleRows()
	if rows <= 0 {
		rows = len(visible)
	}

	// View() may consume up to two rows on "↑ N above" / "↓ N more" footers,
	// shrinking the actual task-row budget. Account for this when computing
	// the new scroll offset so the target doesn't get pushed back off-screen
	// by the footer that appears once we scroll.
	const footerBudget = 2
	effective := rows - footerBudget
	if effective < 1 {
		effective = 1
	}

	if targetVisible < d.scrollOff {
		// Position target near the top of the viewport.
		d.scrollOff = targetVisible
	} else if targetVisible >= d.scrollOff+effective {
		// Position target near the bottom (but not on the absolute last row,
		// so the "↓ more" footer still has room).
		d.scrollOff = targetVisible - effective + 1
		if d.scrollOff < 0 {
			d.scrollOff = 0
		}
	}

	if d.cursor == 0 || d.cursor < d.scrollOff || d.cursor >= d.scrollOff+effective {
		d.cursor = targetVisible
	}

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
