package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/giovannialves/corvex/internal/types"
)

var (
	subtle = lipgloss.AdaptiveColor{Light: "#888888", Dark: "#666666"}
	accent = lipgloss.AdaptiveColor{Light: "#7D56F4", Dark: "#AD8CFF"}

	green = lipgloss.AdaptiveColor{Light: "#04B575", Dark: "#04B575"}
	cyan  = lipgloss.AdaptiveColor{Light: "#00ADD8", Dark: "#00E5FF"}
	red   = lipgloss.AdaptiveColor{Light: "#FF4672", Dark: "#FF6B8A"}
	gray  = lipgloss.AdaptiveColor{Light: "#AAAAAA", Dark: "#555555"}

	// HeaderStyle renders the top bar.
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accent).
			PaddingLeft(1).
			PaddingRight(1)

	// DAGPanelStyle wraps the left task list panel.
	DAGPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtle).
			PaddingLeft(1).
			PaddingRight(1)

	// WorkerPanelStyle wraps the right streaming panel.
	WorkerPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(subtle).
				PaddingLeft(1).
				PaddingRight(1)

	// StatusBarStyle renders the bottom metrics bar.
	StatusBarStyle = lipgloss.NewStyle().
			Foreground(subtle).
			PaddingLeft(1)

	// StatusPassed styles passed task text.
	StatusPassed = lipgloss.NewStyle().Foreground(green)
	// StatusRunning styles running task text.
	StatusRunning = lipgloss.NewStyle().Foreground(cyan)
	// StatusPending styles pending task text.
	StatusPending = lipgloss.NewStyle().Foreground(gray)
	// StatusFailed styles failed task text.
	StatusFailed = lipgloss.NewStyle().Foreground(red)
	// StatusSkippedStyle styles skipped task text.
	StatusSkippedStyle = lipgloss.NewStyle().Foreground(gray)

	// StreamText styles streamed text content.
	StreamText = lipgloss.NewStyle()
	// StreamTool styles tool_use events.
	StreamTool = lipgloss.NewStyle().Foreground(cyan)
	// StreamResult styles tool_result events.
	StreamResult = lipgloss.NewStyle().Foreground(green)
	// StreamFile styles file events.
	StreamFile = lipgloss.NewStyle().Foreground(accent)

	// KeyHint styles keyboard shortcut hints.
	KeyHint = lipgloss.NewStyle().Foreground(subtle)
	// CostStyle styles cost display.
	CostStyle = lipgloss.NewStyle().Foreground(green)
	// CounterStyle styles numeric counters.
	CounterStyle = lipgloss.NewStyle().Bold(true)
	// DividerStyle styles vertical/horizontal dividers.
	DividerStyle = lipgloss.NewStyle().Foreground(subtle)

	// CursorStyle highlights the selected task row.
	CursorStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
)

// StatusEmoji returns an emoji for the given task status.
func StatusEmoji(status types.TaskStatus) string {
	switch status {
	case types.StatusPassed:
		return "✅"
	case types.StatusRunning:
		return "🔄"
	case types.StatusPending:
		return "⬜"
	case types.StatusFailed:
		return "❌"
	case types.StatusSkipped:
		return "⏭️"
	default:
		return "?"
	}
}

// ToolIcon returns an icon for the given tool name.
func ToolIcon(tool string) string {
	switch tool {
	case "Read":
		return "📖"
	case "Write", "Edit":
		return "✏️"
	case "Bash":
		return "⚡"
	default:
		return "🔧"
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
