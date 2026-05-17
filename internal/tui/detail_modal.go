package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/giovannialves/corvex/internal/types"
)

// detailModalView renders the per-task overlay shown when the user
// presses Enter on a DAG row. Shows the basics today (ID, title, status,
// attempt); review summary and escalation note loading is left for a
// follow-up iteration once those streams are available in the model.
func detailModalView(t TaskEntry, screenW, screenH int) string {
	statusLine := fmt.Sprintf("%s  %s",
		StatusStyle(t.Status).Render(StatusGlyph(t.Status)),
		StatusStyle(t.Status).Render(StatusLabel(t.Status)),
	)
	if t.Status == types.StatusRunning && !t.StartedAt.IsZero() {
		statusLine += TextMuted.Render(fmt.Sprintf("  ·  %s", FormatDuration(time.Since(t.StartedAt))))
	} else if t.Duration > 0 {
		statusLine += TextMuted.Render(fmt.Sprintf("  ·  %s", FormatDuration(t.Duration)))
	}

	rows := []string{
		ModalLabel.Render("ID") + "      " + TextMain.Render(t.ID),
		ModalLabel.Render("TITLE") + "   " + TextMain.Render(t.Title),
		ModalLabel.Render("STATUS") + "  " + statusLine,
	}
	if t.Attempt > 0 {
		rows = append(rows, ModalLabel.Render("ATTEMPT")+" "+TextMain.Render(fmt.Sprintf("%d", t.Attempt)))
	}

	footer := TextFaint.Render("esc to close")
	content := ModalTitle.Render("Task detail") + "\n" + strings.Join(rows, "\n") + "\n\n" + footer

	w := minInt(screenW-8, 72)
	if w < 30 {
		w = 30
	}
	h := minInt(screenH-6, lipgloss.Height(content)+2)

	return ModalStyle.Width(w).Height(h).Render(content)
}
