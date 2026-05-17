package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar renders the single-line bottom bar: metrics on the left,
// keybinding hints on the right. When filter mode is active, the bar
// displays the filter input in place of the metrics.
type StatusBar struct {
	tokensIn  int
	tokensOut int
	totalCost float64
	turns     int
	startedAt time.Time
	elapsed   time.Duration
	paused    bool
	filtering bool
	filter    string
	width     int
	keys      KeyMap
}

// NewStatusBar creates a status bar with the given key bindings.
func NewStatusBar(keys KeyMap) StatusBar {
	return StatusBar{
		startedAt: time.Now(),
		keys:      keys,
	}
}

// View renders the status bar.
func (s StatusBar) View() string {
	if s.width <= 0 {
		s.width = 80
	}

	left := s.renderLeft()
	right := s.renderHints()

	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	gap := s.width - leftWidth - rightWidth
	if gap < 1 {
		gap = 1
	}

	line := left + strings.Repeat(" ", gap) + right
	divider := Divider.Render(strings.Repeat("─", s.width))
	return divider + "\n" + StatusBarStyle.Render(line)
}

func (s StatusBar) renderLeft() string {
	if s.filtering {
		return " " + KeyChip.Render("/") + " " + TextMain.Render(s.filter) + TextMuted.Render("_")
	}

	parts := []string{
		fmt.Sprintf("tokens %s↑ %s↓",
			CounterStyle.Render(FormatTokens(s.tokensIn)),
			CounterStyle.Render(FormatTokens(s.tokensOut)),
		),
		fmt.Sprintf("turn %d", s.turns),
		FormatDuration(s.elapsed),
		CostStyle.Render(FormatCost(s.totalCost)),
	}
	if s.paused {
		parts = append(parts, StatusFailed.Render("⏸ paused"))
	}
	return " " + strings.Join(parts, "  ·  ")
}

func (s StatusBar) renderHints() string {
	hints := []string{
		hint("?", "help"),
		hint("/", "filter"),
		hint("↵", "detail"),
		hint("p", "pause"),
		hint("q", "quit"),
	}
	return strings.Join(hints, "  ") + " "
}

func hint(key, desc string) string {
	return KeyChip.Render(key) + " " + KeyHint.Render(desc)
}

// SetSize adjusts the bar width.
func (s StatusBar) SetSize(w int) StatusBar {
	s.width = w
	return s
}

// AddTokens accumulates token counts and cost.
func (s StatusBar) AddTokens(in, out int, cost float64) StatusBar {
	s.tokensIn += in
	s.tokensOut += out
	s.totalCost += cost
	return s
}

// IncrTurns increments the turn counter.
func (s StatusBar) IncrTurns() StatusBar {
	s.turns++
	return s
}

// SetPaused toggles the pause indicator.
func (s StatusBar) SetPaused(p bool) StatusBar {
	s.paused = p
	return s
}

// Tick updates the elapsed duration.
func (s StatusBar) Tick(now time.Time) StatusBar {
	s.elapsed = now.Sub(s.startedAt)
	return s
}

// EnterFilter switches the bar into filter input mode.
func (s StatusBar) EnterFilter() StatusBar {
	s.filtering = true
	s.filter = ""
	return s
}

// ExitFilter clears filter mode.
func (s StatusBar) ExitFilter() StatusBar {
	s.filtering = false
	return s
}

// AppendFilter appends a rune to the filter input.
func (s StatusBar) AppendFilter(r rune) StatusBar {
	s.filter += string(r)
	return s
}

// BackspaceFilter removes the trailing rune from the filter input.
func (s StatusBar) BackspaceFilter() StatusBar {
	if s.filter == "" {
		return s
	}
	runes := []rune(s.filter)
	s.filter = string(runes[:len(runes)-1])
	return s
}

// Filtering reports whether the bar is in filter mode.
func (s StatusBar) Filtering() bool {
	return s.filtering
}

// Filter returns the current filter text.
func (s StatusBar) Filter() string {
	return s.filter
}
