package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/types"
)

func freshModel(t *testing.T) (Model, chan orchestrator.Command) {
	t.Helper()
	events := make(chan orchestrator.Event, 16)
	commands := make(chan orchestrator.Command, 16)
	_, cancel := context.WithCancel(context.Background())
	m := NewWithCommands(events, commands, cancel, "demo")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)
	m = m.AddDAGTasks([]TaskEntry{
		{ID: "S01", Title: "Pending task", Status: types.StatusPending},
		{ID: "S02", Title: "Failed task", Status: types.StatusFailed, Duration: time.Second},
		{ID: "S03", Title: "Another pending", Status: types.StatusPending},
	})
	return m, commands
}

func pressKey(t *testing.T, m Model, s string) Model {
	t.Helper()
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	switch s {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	}
	updated, _ := m.Update(msg)
	return updated.(Model)
}

func TestHelpModalToggle(t *testing.T) {
	t.Parallel()
	m, _ := freshModel(t)

	m = pressKey(t, m, "?")
	if m.modal != modalHelp {
		t.Fatalf("after ?, modal = %v, want modalHelp", m.modal)
	}

	m = pressKey(t, m, "esc")
	if m.modal != modalNone {
		t.Fatalf("after esc, modal = %v, want modalNone", m.modal)
	}
}

func TestDetailModalOpensOnEnter(t *testing.T) {
	t.Parallel()
	m, _ := freshModel(t)

	m = pressKey(t, m, "enter")
	if m.modal != modalDetail {
		t.Fatalf("after enter, modal = %v, want modalDetail", m.modal)
	}

	m = pressKey(t, m, "esc")
	if m.modal != modalNone {
		t.Fatalf("after esc, modal = %v, want modalNone", m.modal)
	}
}

func TestFilterNarrowsDAG(t *testing.T) {
	t.Parallel()
	m, _ := freshModel(t)

	m = pressKey(t, m, "/")
	if !m.status.Filtering() {
		t.Fatal("expected status bar to be in filter mode")
	}

	// Type "failed" — should match only S02
	for _, r := range "failed" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	if got := len(m.dag.visibleIndexes()); got != 1 {
		t.Fatalf("filtered visible = %d, want 1", got)
	}

	m = pressKey(t, m, "esc")
	if m.status.Filtering() {
		t.Error("filter mode should exit on esc")
	}
	if m.dag.Filter() != "" {
		t.Error("filter should clear on esc")
	}
}

func TestPauseSendsCommand(t *testing.T) {
	t.Parallel()
	m, commands := freshModel(t)

	m = pressKey(t, m, "p")
	if !m.paused {
		t.Fatal("expected paused = true after p")
	}

	select {
	case cmd := <-commands:
		if cmd.Type != orchestrator.CmdPause {
			t.Errorf("command = %v, want CmdPause", cmd.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected CmdPause on commands channel")
	}

	m = pressKey(t, m, "p")
	if m.paused {
		t.Fatal("expected paused = false after second p")
	}
	select {
	case cmd := <-commands:
		if cmd.Type != orchestrator.CmdResume {
			t.Errorf("command = %v, want CmdResume", cmd.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected CmdResume on commands channel")
	}
}

func TestSkipPendingTaskSendsCommand(t *testing.T) {
	t.Parallel()
	m, commands := freshModel(t)

	// Cursor starts at S01 (pending) — skip should fire.
	m = pressKey(t, m, "s")

	select {
	case cmd := <-commands:
		if cmd.Type != orchestrator.CmdSkip || cmd.TaskID != "S01" {
			t.Errorf("command = %+v, want skip S01", cmd)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected CmdSkip on commands channel")
	}
}

func TestRetryFailedTaskSendsCommand(t *testing.T) {
	t.Parallel()
	m, commands := freshModel(t)

	// Move cursor to S02 (failed) and press r.
	m = pressKey(t, m, "j")
	m = pressKey(t, m, "r")

	select {
	case cmd := <-commands:
		if cmd.Type != orchestrator.CmdRetry || cmd.TaskID != "S02" {
			t.Errorf("command = %+v, want retry S02", cmd)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected CmdRetry on commands channel")
	}
}

func TestSkipIgnoresNonPending(t *testing.T) {
	t.Parallel()
	m, commands := freshModel(t)

	// Cursor on S02 (failed) → skip is ignored.
	m = pressKey(t, m, "j")
	m = pressKey(t, m, "s")

	select {
	case cmd := <-commands:
		t.Errorf("unexpected command for non-pending skip: %+v", cmd)
	case <-time.After(50 * time.Millisecond):
	}
}
