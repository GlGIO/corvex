package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/giovannialves/corvex/internal/types"
)

func TestTruncate_ASCII(t *testing.T) {
	got := truncate("hello world", 5)
	if ansi.StringWidth(got) > 5 {
		t.Errorf("width(%q) = %d, want <= 5", got, ansi.StringWidth(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate(%q) = %q, want trailing …", "hello world", got)
	}
}

func TestTruncate_MultibyteNotCorrupted(t *testing.T) {
	in := "café résumé 日本語テスト"
	got := truncate(in, 6)
	// Must remain valid UTF-8 (no rune split) and fit the width budget.
	if !utf8Valid(got) {
		t.Errorf("truncate produced invalid UTF-8: %q", got)
	}
	if ansi.StringWidth(got) > 6 {
		t.Errorf("width(%q) = %d, want <= 6", got, ansi.StringWidth(got))
	}
}

func TestTruncate_ANSIWidthNotBytes(t *testing.T) {
	// A short visible string wrapped in ANSI color codes: byte length is large
	// but display width is small, so it must NOT be truncated.
	styled := "\x1b[31mhi\x1b[0m"
	got := truncate(styled, 10)
	if got != styled {
		t.Errorf("styled string within width budget was altered: %q", got)
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestComputeLayout_Invariants(t *testing.T) {
	for _, size := range [][2]int{{40, 10}, {120, 40}, {200, 120}} {
		w, h := size[0], size[1]
		d := computeLayout(w, h)
		if d.dagHeight < 4 || d.dagHeight > 12 {
			t.Errorf("size %v: dagHeight = %d, want [4,12]", size, d.dagHeight)
		}
		if d.workerHeight < 1 {
			t.Errorf("size %v: workerHeight = %d, want >= 1", size, d.workerHeight)
		}
		if d.dagHeight+d.workerHeight+1 > d.mainHeight {
			t.Errorf("size %v: dag+worker+sep = %d exceeds mainHeight %d", size, d.dagHeight+d.workerHeight+1, d.mainHeight)
		}
	}
}

func TestAppendStream_RingBuffer(t *testing.T) {
	w := NewWorkerPanel().SetSize(80, 10)
	for i := 0; i < maxStreamLines+250; i++ {
		w = w.AppendStream(&types.StreamEvent{Type: types.EventText, Content: "line"})
	}
	w = w.AppendStream(&types.StreamEvent{Type: types.EventText, Content: "LAST"})
	if len(w.lines) != maxStreamLines {
		t.Errorf("lines = %d, want capped at %d", len(w.lines), maxStreamLines)
	}
	if !strings.Contains(w.lines[len(w.lines)-1], "LAST") {
		t.Errorf("most recent line not retained: %q", w.lines[len(w.lines)-1])
	}
}

func TestView_DoneBanner(t *testing.T) {
	m := NewWithCommands(nil, nil, func() {}, "proj")
	m.ready = true
	m.width = 100
	m.height = 30
	m = m.resize()
	m.done = true
	out := m.View()
	if !strings.Contains(out, "complete") {
		t.Errorf("View() when done should show a completion banner; got:\n%s", out)
	}
}
