package tui

// Rendering and layout for the run dashboard: the vertical budget shared by
// View and resize, the header line, and the modal overlay.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/giovannialves/corvex/internal/types"
)

// layoutDims holds the computed vertical budget for the main panels.
type layoutDims struct {
	mainHeight   int
	dagHeight    int
	workerHeight int
}

// computeLayout is the single source of truth for the vertical layout budget,
// used by both View (to render) and resize (to size sub-panels) so the two can
// never drift. DAG gets ~40% of the space, capped to [4,12] rows; the worker
// panel takes the rest minus a separator line.
func computeLayout(width, height int) layoutDims {
	mainHeight := height - 3 // header + status divider + status body
	if mainHeight < 6 {
		mainHeight = 6
	}
	dagHeight := mainHeight * 40 / 100
	if dagHeight < 4 {
		dagHeight = 4
	}
	if dagHeight > 12 {
		dagHeight = 12
	}
	if dagHeight > mainHeight-3 {
		dagHeight = mainHeight - 3
	}
	workerHeight := mainHeight - dagHeight - 1 // 1 line for separator
	if workerHeight < 1 {
		workerHeight = 1
	}
	return layoutDims{mainHeight: mainHeight, dagHeight: dagHeight, workerHeight: workerHeight}
}

// View renders the full TUI layout.
func (m Model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}
	if m.quitting {
		return "\n  Cancelled.\n"
	}

	header := m.renderHeader()
	statusView := m.status.View()

	dims := computeLayout(m.width, m.height)
	dagHeight := dims.dagHeight
	workerHeight := dims.workerHeight

	// MaxHeight is the twin of Height that *truncates* overflow instead of
	// padding. Without it, if either panel's View() ever returns more lines
	// than its declared height (due to wrap, hidden styling padding, or any
	// future refactor), the extra lines bleed into the divider and the
	// adjacent panel — corrupting the whole layout.
	dagView := lipgloss.NewStyle().
		Width(m.width).
		Height(dagHeight).
		MaxHeight(dagHeight).
		Render(m.dag.View())

	separator := Divider.Render(strings.Repeat("─", m.width))

	workerView := lipgloss.NewStyle().
		Width(m.width).
		Height(workerHeight).
		MaxHeight(workerHeight).
		Render(m.worker.View())

	main := lipgloss.JoinVertical(lipgloss.Left, header, dagView, separator, workerView, statusView)

	if m.modal == modalHelp {
		return overlay(main, helpModalView(m.keys, m.width, m.height), m.width, m.height)
	}
	if m.modal == modalDetail {
		if t := m.dag.SelectedTask(); t != nil {
			return overlay(main, detailModalView(*t, m.width, m.height), m.width, m.height)
		}
	}

	return main
}

func (m Model) renderHeader() string {
	tasks := m.dag.Tasks()
	completed := 0
	for _, t := range tasks {
		if t.Status == types.StatusPassed {
			completed++
		}
	}
	total := len(tasks)

	left := HeaderTitle.Render("corvex") + TextMuted.Render(" · ") +
		Chip.Render(m.project)
	if m.done {
		left += TextMuted.Render(" · ") + StatusPassed.Render("✓ complete — press q to exit")
	}

	right := fmt.Sprintf("%s%s%s",
		TextMuted.Render(fmt.Sprintf("%d/%d", completed, total)),
		TextMuted.Render(" · "),
		CostStyle.Render(FormatCost(m.status.totalCost)),
	)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := m.width - leftW - rightW - 2
	if gap < 1 {
		gap = 1
	}

	return " " + left + strings.Repeat(" ", gap) + right + " "
}

func (m Model) resize() Model {
	dims := computeLayout(m.width, m.height)
	m.dag = m.dag.SetSize(m.width, dims.dagHeight)
	m.worker = m.worker.SetSize(m.width, dims.workerHeight)
	m.status = m.status.SetSize(m.width)
	return m
}

// HeaderLine returns a simple header string for non-TUI contexts.
func HeaderLine(project string, completed, total int, cost float64) string {
	return fmt.Sprintf(
		"corvex · %s · %d/%d done · %s",
		project, completed, total, FormatCost(cost),
	)
}

// overlay centres `inner` over `background` so the modal floats above the
// main layout without breaking the rest of the screen geometry.
func overlay(background, inner string, w, h int) string {
	box := lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, inner,
		lipgloss.WithWhitespaceChars(" "),
	)
	_ = background
	return box
}
