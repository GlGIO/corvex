package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/tui"
	"github.com/giovannialves/corvex/internal/types"
)

// PlainRenderer writes clean, scannable orchestrator event lines to w.
//
// When noColor is false (color-capable TTY, no NO_COLOR flag), Unicode glyphs
// are rendered with lipgloss colour styles and the middle-dot separator is used.
// When noColor is true (non-TTY, --no-color, or NO_COLOR env), ASCII glyphs are
// used and lipgloss styles are omitted — output is safe to pipe or redirect.
type PlainRenderer struct {
	w       io.Writer
	noColor bool
	quiet   bool
}

// NewPlainRenderer returns a renderer that writes to w.
// Set noColor=true when the output is not a colour-capable TTY.
// Set quiet=true to suppress per-task progress lines; only failures/errors and
// the final summary are printed.
func NewPlainRenderer(w io.Writer, noColor, quiet bool) *PlainRenderer {
	return &PlainRenderer{w: w, noColor: noColor, quiet: quiet}
}

// Drain consumes events until the channel is closed, writing a line per event.
func (r *PlainRenderer) Drain(events <-chan orchestrator.Event) {
	for ev := range events {
		r.render(ev)
	}
}

func (r *PlainRenderer) render(ev orchestrator.Event) {
	switch ev.Type {
	case orchestrator.EventTaskStart:
		if r.quiet {
			return
		}
		g := r.coloured("▶", ">", tui.StatusRunning)
		if ev.Message != "" {
			fmt.Fprintf(r.w, "%s %s  %s\n", g, ev.TaskID, ev.Message)
		} else {
			fmt.Fprintf(r.w, "%s %s\n", g, ev.TaskID)
		}

	case orchestrator.EventTaskComplete:
		switch ev.Status {
		case types.StatusPassed:
			if r.quiet {
				return
			}
			g := r.coloured("✓", "+", tui.StatusPassed)
			dur := tui.FormatDuration(time.Duration(ev.DurationMs) * time.Millisecond)
			cost := tui.FormatCost(ev.CostUSD)
			fmt.Fprintf(r.w, "%s %s  passed %s %s %s %s\n",
				g, ev.TaskID, r.dot(), dur, r.dot(), cost)
		case types.StatusSkipped:
			if r.quiet {
				return
			}
			g := r.coloured("⏭", "->", tui.StatusSkippedStyle)
			msg := strings.TrimPrefix(ev.Message, "skipped: ")
			msg = strings.TrimPrefix(msg, "skipped ")
			fmt.Fprintf(r.w, "%s %s  skipped (%s)\n", g, ev.TaskID, msg)
		default:
			g := r.coloured("✗", "!", tui.StatusFailed)
			fmt.Fprintf(r.w, "%s %s  failed %s %s\n", g, ev.TaskID, r.dot(), ev.Message)
		}

	case orchestrator.EventRetry:
		if r.quiet {
			return
		}
		g := r.coloured("↻", "~", tui.TextMuted)
		fmt.Fprintf(r.w, "%s %s  retry %d\n", g, ev.TaskID, ev.Attempt)

	case orchestrator.EventPlanStart:
		if r.quiet {
			return
		}
		g := r.coloured("■", "*", tui.TextMuted)
		fmt.Fprintf(r.w, "%s planning%s\n", g, r.ellipsis())

	case orchestrator.EventDAGResolved:
		if r.quiet {
			return
		}
		g := r.coloured("✓", "+", tui.StatusPassed)
		if ev.Total > 0 {
			fmt.Fprintf(r.w, "%s plan ready (%d tasks)\n", g, ev.Total)
		} else {
			fmt.Fprintf(r.w, "%s plan ready\n", g)
		}

	case orchestrator.EventDone:
		g := r.coloured("✓", "+", tui.StatusPassed)
		fmt.Fprintf(r.w, "%s done\n", g)

	case orchestrator.EventHumanGate:
		g := r.coloured("⏸", "||", tui.StatusRunning)
		fmt.Fprintf(r.w, "%s %s  human-gate: %s\n", g, ev.TaskID, ev.Message)

	case orchestrator.EventError:
		g := r.coloured("✗", "!", tui.StatusFailed)
		fmt.Fprintf(r.w, "%s %s\n", g, ev.Message)

	case orchestrator.EventInsight:
		if ev.Insight != nil {
			fmt.Fprintf(r.w, "\n%s Insight: %d tasks of type %q completed without a dedicated agent.\n",
				r.coloured("✨", "!", tui.TextMuted), ev.Insight.Count, ev.Insight.TaskType)
			fmt.Fprintf(r.w, "   A suggested agent prompt was saved to: .corvex/insights/%s-agent-suggestion.md\n", ev.Insight.TaskType)
			fmt.Fprintf(r.w, "   To activate it: mv .corvex/insights/%s-agent-suggestion.md %s\n\n",
				ev.Insight.TaskType, ev.Insight.SuggestedPath)
		}
	}
}

// coloured returns the unicode glyph rendered with style when colour is enabled,
// or the ascii fallback when noColor is true.
func (r *PlainRenderer) coloured(unicode, ascii string, style lipgloss.Style) string {
	if r.noColor {
		return ascii
	}
	return style.Render(unicode)
}

// dot returns the middle-dot separator or an ASCII period.
func (r *PlainRenderer) dot() string {
	if r.noColor {
		return "."
	}
	return "·"
}

// ellipsis returns a Unicode or ASCII ellipsis.
func (r *PlainRenderer) ellipsis() string {
	if r.noColor {
		return "..."
	}
	return "…"
}
