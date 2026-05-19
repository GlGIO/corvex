package tui

import (
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func makeTasks(n int) []TaskEntry {
	entries := make([]TaskEntry, n)
	for i := 0; i < n; i++ {
		entries[i] = TaskEntry{
			ID:     "S" + padInt(i+1),
			Title:  "Task " + padInt(i+1),
			Status: types.StatusPending,
		}
	}
	return entries
}

func padInt(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	tens := n / 10
	ones := n % 10
	return string(rune('0'+tens)) + string(rune('0'+ones))
}

// TestDAGPanel_NeverExceedsHeight is the regression guard for the bug where
// the "↓ N more" footer was appended on top of the configured row budget,
// producing a panel one line taller than its declared height and visually
// corrupting the divider/worker area beneath it.
func TestDAGPanel_NeverExceedsHeight(t *testing.T) {
	cases := []struct {
		name      string
		tasks     int
		height    int
		scrollOff int
	}{
		{"more below, at top", 30, 12, 0},
		{"more above and below", 30, 12, 4},
		{"only more above", 30, 12, 20},
		{"all fit (no footer)", 8, 12, 0},
		{"all fit at exact height", 12, 12, 0},
		{"narrow viewport", 30, 6, 10},
		{"single row viewport", 30, 1, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDAGPanel()
			d = d.AddTasks(makeTasks(tc.tasks))
			d = d.SetSize(80, tc.height)
			d.scrollOff = tc.scrollOff

			got := d.View()
			lines := strings.Count(got, "\n") + 1
			// An empty View() returns "" which Count interprets as 1 — handle that.
			if strings.TrimSpace(got) == "" {
				lines = 0
			}
			if lines > tc.height {
				t.Errorf("View() produced %d lines, exceeds height %d.\nOutput:\n%s",
					lines, tc.height, got)
			}
		})
	}
}

// TestDAGPanel_ScrollToTask_RevealsHiddenTask verifies that the running-task
// auto-scroll behaviour brings an off-screen task into view.
func TestDAGPanel_ScrollToTask_RevealsHiddenTask(t *testing.T) {
	d := NewDAGPanel()
	d = d.AddTasks(makeTasks(30))
	d = d.SetSize(80, 12)
	// Initially the panel shows S01 onwards; S20 is hidden.
	if strings.Contains(d.View(), " S20 ") {
		t.Fatal("precondition: S20 should be hidden before ScrollToTask")
	}

	d = d.ScrollToTask("S20")
	out := d.View()
	if !strings.Contains(out, " S20 ") {
		t.Errorf("ScrollToTask(S20) did not reveal the task. Output:\n%s", out)
	}
}
