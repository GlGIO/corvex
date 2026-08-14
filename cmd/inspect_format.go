package cmd

import (
	"fmt"
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

// glyphFor maps a task status to the emoji shown in the STATUS column.
func glyphFor(s types.TaskStatus) string {
	switch s {
	case types.StatusPassed:
		return "✅"
	case types.StatusRunning:
		return "🔄"
	case types.StatusFailed:
		return "❌"
	case types.StatusSkipped:
		return "⏭"
	default:
		return "⬜"
	}
}

// humanDuration renders a duration in the compact form the tables use: "750ms",
// "45s", "1m35s".
func humanDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) - mins*60
	return fmt.Sprintf("%dm%02ds", mins, secs)
}

// msDuration converts the milliseconds carried by ledger entries and task stats
// into a time.Duration.
func msDuration(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

// truncateTitle cuts long titles so the TITLE column stays on one line. The cut
// counts BYTES, not runes, so a title with accents can be sliced mid-rune and
// print mojibake — a known anomaly kept as-is (only the human table is affected;
// --json carries the full title).
func truncateTitle(title string) string {
	if len(title) > 50 {
		return title[:49] + "…"
	}
	return title
}
