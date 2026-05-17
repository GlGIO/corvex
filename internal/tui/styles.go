package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/giovannialves/corvex/internal/types"
)

// Palette — charm.sh-inspired. Soft accents, restrained reds/greens,
// adaptive for light and dark terminals.
var (
	accent      = lipgloss.AdaptiveColor{Light: "#7D56F4", Dark: "#AD8CFF"}
	accentSoft  = lipgloss.AdaptiveColor{Light: "#B59FFF", Dark: "#8B6FE0"}
	textMain    = lipgloss.AdaptiveColor{Light: "#1A1A2E", Dark: "#E6E6F0"}
	textMuted   = lipgloss.AdaptiveColor{Light: "#5F5F6E", Dark: "#9696A8"}
	textFaint   = lipgloss.AdaptiveColor{Light: "#9E9EAD", Dark: "#5F5F70"}
	dividerCol  = lipgloss.AdaptiveColor{Light: "#D4D4DC", Dark: "#3A3A4A"}
	chipBg      = lipgloss.AdaptiveColor{Light: "#EFEAFC", Dark: "#2A2240"}

	success = lipgloss.AdaptiveColor{Light: "#1E8A5E", Dark: "#4FD49C"}
	running = lipgloss.AdaptiveColor{Light: "#0F8AAA", Dark: "#5EC4DA"}
	failure = lipgloss.AdaptiveColor{Light: "#C9325A", Dark: "#E36A87"}
)

// Status glyphs. Single Unicode characters keep widths predictable and
// blend better with the muted palette than emoji.
const (
	GlyphPending  = "○"
	GlyphRunning  = "●"
	GlyphPassed   = "✓"
	GlyphFailed   = "✗"
	GlyphSkipped  = "→"
	GlyphUnknown  = "·"
	GlyphToolUse  = "›"
	GlyphToolDone = "↳"
	GlyphBullet   = "•"
)

// Core text styles.
var (
	TextMain  = lipgloss.NewStyle().Foreground(textMain)
	TextMuted = lipgloss.NewStyle().Foreground(textMuted)
	TextFaint = lipgloss.NewStyle().Foreground(textFaint)

	HeaderTitle = lipgloss.NewStyle().Bold(true).Foreground(accent)

	// Chip renders a soft-background label used in the header.
	Chip = lipgloss.NewStyle().
		Foreground(textMain).
		Background(chipBg).
		Padding(0, 1)

	// Divider draws a thin horizontal rule used to separate panels.
	Divider = lipgloss.NewStyle().Foreground(dividerCol)

	// StatusBarStyle wraps the bottom metrics + hints line.
	StatusBarStyle = lipgloss.NewStyle().Foreground(textMuted).Padding(0, 1)

	// CounterStyle and CostStyle are kept for backwards compatibility with
	// callers that format inline metrics.
	CounterStyle = lipgloss.NewStyle().Bold(true).Foreground(textMain)
	CostStyle    = lipgloss.NewStyle().Foreground(success).Bold(true)

	// Status-coloured text used in DAG rows.
	StatusPassed       = lipgloss.NewStyle().Foreground(success)
	StatusRunning      = lipgloss.NewStyle().Foreground(running)
	StatusPending      = lipgloss.NewStyle().Foreground(textFaint)
	StatusFailed       = lipgloss.NewStyle().Foreground(failure)
	StatusSkippedStyle = lipgloss.NewStyle().Foreground(textFaint).Italic(true)

	// Stream styles for the worker viewport.
	StreamText   = lipgloss.NewStyle().Foreground(textMain)
	StreamTool   = lipgloss.NewStyle().Foreground(running)
	StreamResult = lipgloss.NewStyle().Foreground(textMuted)
	StreamFile   = lipgloss.NewStyle().Foreground(accentSoft)
	StreamError  = lipgloss.NewStyle().Foreground(failure)

	// CursorStyle highlights the selected DAG row.
	CursorStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)

	// KeyHint renders a single [k] desc fragment in the status bar.
	KeyHint = lipgloss.NewStyle().Foreground(textMuted)
	KeyChip = lipgloss.NewStyle().Foreground(accent).Bold(true)

	// ModalStyle wraps full-screen overlays (help, detail).
	ModalStyle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(accent).
		Padding(1, 2)
	ModalTitle = lipgloss.NewStyle().Bold(true).Foreground(accent).MarginBottom(1)
	ModalLabel = lipgloss.NewStyle().Foreground(textMuted).Bold(true)
)

// StatusGlyph returns the glyph for a given task status. Falls back to a
// neutral dot for unknown values.
func StatusGlyph(status types.TaskStatus) string {
	switch status {
	case types.StatusPassed:
		return GlyphPassed
	case types.StatusRunning:
		return GlyphRunning
	case types.StatusPending:
		return GlyphPending
	case types.StatusFailed:
		return GlyphFailed
	case types.StatusSkipped:
		return GlyphSkipped
	default:
		return GlyphUnknown
	}
}

// StatusStyle returns the style for a status, used to colour the whole row.
func StatusStyle(s types.TaskStatus) lipgloss.Style {
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

// FormatTokens formats a token count to a human-readable string like "45.2k".
func FormatTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// FormatDuration formats a duration to a compact string like "1m32s".
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	d = d.Truncate(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// FormatCost formats a USD cost like "$2.34".
func FormatCost(c float64) string {
	return fmt.Sprintf("$%.2f", c)
}

// Status text labels — used instead of the raw status string when the
// glyph carries the same information and only the textual label is needed.
func StatusLabel(s types.TaskStatus) string {
	switch s {
	case types.StatusPassed:
		return "passed"
	case types.StatusRunning:
		return "running"
	case types.StatusPending:
		return "pending"
	case types.StatusFailed:
		return "failed"
	case types.StatusSkipped:
		return "skipped"
	default:
		return "?"
	}
}
