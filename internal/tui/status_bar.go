package tui

import (
	"fmt"
	"strings"
	"time"
)

// StatusBar renders bottom metrics and keybinding hints.
type StatusBar struct {
	tokensIn  int
	tokensOut int
	totalCost float64
	turns     int
	maxTurns  int
	startedAt time.Time
	elapsed   time.Duration
	paused    bool
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

// Update is a no-op for now (future: handle tick messages internally).
func (s StatusBar) Update(_ any) StatusBar {
	return s
}

// View renders the two-line status bar.
func (s StatusBar) View() string {
	pauseIndicator := ""
	if s.paused {
		pauseIndicator = StatusFailed.Render(" ⏸ PAUSED")
	}

	metrics := fmt.Sprintf(
		"tokens: %s in / %s out    turns: %s    elapsed: %s    %s%s",
		CounterStyle.Render(FormatTokens(s.tokensIn)),
		CounterStyle.Render(FormatTokens(s.tokensOut)),
		CounterStyle.Render(fmt.Sprintf("%d/%d", s.turns, s.maxTurns)),
		CounterStyle.Render(FormatDuration(s.elapsed)),
		CostStyle.Render(FormatCost(s.totalCost)),
		pauseIndicator,
	)

	bindings := s.keys.ShortHelp()
	hints := make([]string, 0, len(bindings))
	for _, b := range bindings {
		help := b.Help()
		hints = append(hints, KeyHint.Render(fmt.Sprintf("[%s] %s", help.Key, help.Desc)))
	}
	keysLine := strings.Join(hints, "   ")

	divider := DividerStyle.Render(strings.Repeat("─", s.width))
	return divider + "\n" + StatusBarStyle.Render(metrics) + "\n" + StatusBarStyle.Render(keysLine)
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

// SetMaxTurns sets the maximum turns counter.
func (s StatusBar) SetMaxTurns(max int) StatusBar {
	s.maxTurns = max
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
