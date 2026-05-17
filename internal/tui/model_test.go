package tui

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/types"
)

func approxEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

func setupTestModel(t *testing.T) Model {
	t.Helper()
	events := make(chan orchestrator.Event, 10)
	_, cancel := context.WithCancel(context.Background())
	m := New(events, cancel, "test-project")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return updated.(Model)
}

func addTestTasks(m Model, ids ...string) Model {
	entries := make([]TaskEntry, len(ids))
	for i, id := range ids {
		entries[i] = TaskEntry{ID: id, Title: "Task " + id, Status: types.StatusPending}
	}
	m.dag = m.dag.AddTasks(entries)
	return m
}

// --- Model lifecycle ---

func TestModelInit(t *testing.T) {
	t.Parallel()
	events := make(chan orchestrator.Event, 10)
	_, cancel := context.WithCancel(context.Background())
	m := New(events, cancel, "test-project")

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned nil cmd, expected tea.Batch with waitForEvent + tickCmd")
	}
}

func TestModelWindowResize(t *testing.T) {
	t.Parallel()
	events := make(chan orchestrator.Event, 10)
	_, cancel := context.WithCancel(context.Background())
	m := New(events, cancel, "test-project")

	if m.ready {
		t.Fatal("expected ready=false before WindowSizeMsg")
	}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	m = updated.(Model)

	if !m.ready {
		t.Error("expected ready=true after WindowSizeMsg")
	}
	if m.width != 100 {
		t.Errorf("width = %d, want 100", m.width)
	}
	if m.height != 50 {
		t.Errorf("height = %d, want 50", m.height)
	}
}

// --- Event handling ---

func TestModelEventDAGResolved(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	ev := eventMsg(orchestrator.Event{Type: orchestrator.EventDAGResolved, Total: 5})
	updated, _ := m.Update(ev)
	m = updated.(Model)

	if m.dag.totalTasks != 5 {
		t.Errorf("totalTasks = %d, want 5", m.dag.totalTasks)
	}
	if m.dag.completed != 0 {
		t.Errorf("completed = %d, want 0", m.dag.completed)
	}
}

func TestModelEventTaskStart(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)
	m = addTestTasks(m, "S01", "S02")

	ev := eventMsg(orchestrator.Event{Type: orchestrator.EventTaskStart, TaskID: "S01", Attempt: 1})
	updated, _ := m.Update(ev)
	m = updated.(Model)

	found := false
	for _, task := range m.dag.tasks {
		if task.ID == "S01" {
			found = true
			if task.Status != types.StatusRunning {
				t.Errorf("task S01 status = %q, want RUNNING", task.Status)
			}
			if task.Attempt != 1 {
				t.Errorf("task S01 attempt = %d, want 1", task.Attempt)
			}
		}
	}
	if !found {
		t.Error("task S01 not found in DAG")
	}

	if m.worker.activeTask != "S01" {
		t.Errorf("worker activeTask = %q, want S01", m.worker.activeTask)
	}
	if m.worker.phase != "worker" {
		t.Errorf("worker phase = %q, want worker", m.worker.phase)
	}
}

func TestModelEventTaskStream(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	stream := &types.StreamEvent{
		Type:    types.EventToolUse,
		Tool:    "Read",
		Content: "x.go",
	}
	ev := eventMsg(orchestrator.Event{Type: orchestrator.EventTaskStream, Stream: stream})
	updated, _ := m.Update(ev)
	m = updated.(Model)

	if len(m.worker.lines) != 1 {
		t.Fatalf("worker lines = %d, want 1", len(m.worker.lines))
	}
}

func TestModelEventTaskComplete(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)
	m = addTestTasks(m, "S01")
	m.dag = m.dag.SetProgress(0, 1)

	startEv := eventMsg(orchestrator.Event{Type: orchestrator.EventTaskStart, TaskID: "S01", Attempt: 1})
	updated, _ := m.Update(startEv)
	m = updated.(Model)

	completeEv := eventMsg(orchestrator.Event{
		Type:       orchestrator.EventTaskComplete,
		TaskID:     "S01",
		Status:     types.StatusPassed,
		CostUSD:    0.15,
		TokensIn:   1000,
		TokensOut:  500,
		DurationMs: 5000,
		Attempt:    1,
	})
	updated, _ = m.Update(completeEv)
	m = updated.(Model)

	for _, task := range m.dag.tasks {
		if task.ID == "S01" {
			if task.Status != types.StatusPassed {
				t.Errorf("task S01 status = %q, want PASSED", task.Status)
			}
			if task.Duration != 5*time.Second {
				t.Errorf("task S01 duration = %v, want 5s", task.Duration)
			}
		}
	}

	if !approxEqual(m.status.totalCost, 0.15, 1e-9) {
		t.Errorf("totalCost = %f, want 0.15", m.status.totalCost)
	}
	if m.status.tokensIn != 1000 {
		t.Errorf("tokensIn = %d, want 1000", m.status.tokensIn)
	}
	if m.status.tokensOut != 500 {
		t.Errorf("tokensOut = %d, want 500", m.status.tokensOut)
	}
	if m.dag.completed != 1 {
		t.Errorf("dag completed = %d, want 1", m.dag.completed)
	}
}

func TestModelEventDone(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	ev := eventMsg(orchestrator.Event{Type: orchestrator.EventDone})
	updated, _ := m.Update(ev)
	m = updated.(Model)

	if !m.done {
		t.Error("expected done=true after EventDone")
	}
}

// --- Key handling ---

func TestModelKeyQuit(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)

	if !m.quitting {
		t.Error("expected quitting=true after ctrl+c")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd after ctrl+c")
	}
}

func TestModelKeyQuitQ(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)

	if !m.quitting {
		t.Error("expected quitting=true after 'q'")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd after 'q'")
	}
}

func TestModelKeyPause(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = updated.(Model)
	if !m.paused {
		t.Error("expected paused=true after first 'p'")
	}
	if !m.status.paused {
		t.Error("expected status.paused=true")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = updated.(Model)
	if m.paused {
		t.Error("expected paused=false after second 'p'")
	}
}

func TestModelTick(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	now := time.Now().Add(30 * time.Second)
	updated, cmd := m.Update(tickMsg(now))
	m = updated.(Model)

	if m.status.elapsed <= 0 {
		t.Error("expected elapsed > 0 after tick")
	}
	if cmd == nil {
		t.Error("expected tickCmd to be scheduled after tick")
	}
}

func TestModelChannelClosed(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	updated, cmd := m.Update(channelClosedMsg{})
	m = updated.(Model)

	if !m.done {
		t.Error("expected done=true after channelClosedMsg")
	}
	if cmd == nil {
		t.Error("expected tea.Quit after channelClosedMsg")
	}
}

// --- DAG Panel ---

func TestDAGPanelScroll(t *testing.T) {
	t.Parallel()

	d := NewDAGPanel()
	entries := make([]TaskEntry, 20)
	for i := range entries {
		entries[i] = TaskEntry{
			ID:     time.Now().Format("T") + string(rune('A'+i)),
			Title:  "Task",
			Status: types.StatusPending,
		}
	}
	d = d.AddTasks(entries)
	d = d.SetSize(40, 5) // 5 visible rows; panel parent draws header/divider

	if d.cursor != 0 {
		t.Errorf("initial cursor = %d, want 0", d.cursor)
	}

	for i := 0; i < 6; i++ {
		d = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if d.cursor != 6 {
		t.Errorf("cursor after 6 downs = %d, want 6", d.cursor)
	}
	if d.scrollOff < 2 {
		t.Errorf("scrollOff = %d, expected >= 2 after scrolling", d.scrollOff)
	}

	for i := 0; i < 6; i++ {
		d = d.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if d.cursor != 0 {
		t.Errorf("cursor after 6 ups = %d, want 0", d.cursor)
	}
}

func TestDAGPanelBoundary(t *testing.T) {
	t.Parallel()

	d := NewDAGPanel()
	d = d.AddTasks([]TaskEntry{
		{ID: "T1", Title: "A", Status: types.StatusPending},
		{ID: "T2", Title: "B", Status: types.StatusPending},
	})
	d = d.SetSize(40, 10)

	d = d.Update(tea.KeyMsg{Type: tea.KeyUp})
	if d.cursor != 0 {
		t.Errorf("cursor should stay at 0 on up at top, got %d", d.cursor)
	}

	d = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	if d.cursor != 1 {
		t.Errorf("cursor should clamp at last item (1), got %d", d.cursor)
	}
}

func TestDAGPanelSelectedTaskID(t *testing.T) {
	t.Parallel()

	d := NewDAGPanel()
	if id := d.SelectedTaskID(); id != "" {
		t.Errorf("empty panel selected = %q, want empty", id)
	}

	d = d.AddTasks([]TaskEntry{
		{ID: "T1", Title: "A", Status: types.StatusPending},
		{ID: "T2", Title: "B", Status: types.StatusPending},
	})
	if id := d.SelectedTaskID(); id != "T1" {
		t.Errorf("selected = %q, want T1", id)
	}

	d = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	if id := d.SelectedTaskID(); id != "T2" {
		t.Errorf("selected after down = %q, want T2", id)
	}
}

// --- Worker Panel ---

func TestWorkerPanelAutoScroll(t *testing.T) {
	t.Parallel()

	w := NewWorkerPanel()
	w = w.SetSize(80, 10)
	w = w.SetActiveTask("S01", "Worker")

	for i := 0; i < 30; i++ {
		w = w.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: "line content",
		})
	}

	if len(w.lines) != 30 {
		t.Errorf("lines = %d, want 30", len(w.lines))
	}
	if !w.autoScroll {
		t.Error("expected autoScroll=true")
	}
}

func TestWorkerPanelClear(t *testing.T) {
	t.Parallel()

	w := NewWorkerPanel()
	w = w.SetSize(80, 10)
	w = w.AppendStream(&types.StreamEvent{Type: types.EventText, Content: "hello"})

	if len(w.lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(w.lines))
	}

	w = w.Clear()
	if len(w.lines) != 0 {
		t.Errorf("lines after clear = %d, want 0", len(w.lines))
	}
}

func TestWorkerPanelNilStream(t *testing.T) {
	t.Parallel()

	w := NewWorkerPanel()
	w = w.AppendStream(nil)
	if len(w.lines) != 0 {
		t.Errorf("lines after nil stream = %d, want 0", len(w.lines))
	}
}

func TestWorkerPanelStreamTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ev   *types.StreamEvent
	}{
		{"tool_use", &types.StreamEvent{Type: types.EventToolUse, Tool: "Read", Content: "file.go"}},
		{"tool_result", &types.StreamEvent{Type: types.EventToolResult, Content: "ok"}},
		{"text", &types.StreamEvent{Type: types.EventText, Content: "hello"}},
		{"file_event", &types.StreamEvent{Type: "unknown", File: "x.go"}},
		{"default", &types.StreamEvent{Type: "other", Content: "raw"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := NewWorkerPanel()
			w = w.SetSize(80, 10)
			w = w.AppendStream(tt.ev)
			if len(w.lines) != 1 {
				t.Errorf("lines = %d, want 1", len(w.lines))
			}
			if w.lines[0] == "" {
				t.Error("expected non-empty line")
			}
		})
	}
}

// --- Format functions ---

func TestFormatTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{45200, "45.2k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := FormatTokens(tt.input)
			if got != tt.want {
				t.Errorf("FormatTokens(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input time.Duration
		want  string
	}{
		{0, "0s"},
		{500 * time.Millisecond, "0s"},
		{1 * time.Second, "1s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m00s"},
		{92 * time.Second, "1m32s"},
		{3600 * time.Second, "60m00s"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := FormatDuration(tt.input)
			if got != tt.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatCost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input float64
		want  string
	}{
		{0, "$0.00"},
		{0.1, "$0.10"},
		{2.3456, "$2.35"},
		{100.999, "$101.00"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := FormatCost(tt.input)
			if got != tt.want {
				t.Errorf("FormatCost(%f) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStatusGlyph(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status types.TaskStatus
		want   string
	}{
		{types.StatusPassed, GlyphPassed},
		{types.StatusRunning, GlyphRunning},
		{types.StatusPending, GlyphPending},
		{types.StatusFailed, GlyphFailed},
		{types.StatusSkipped, GlyphSkipped},
		{"unknown", GlyphUnknown},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			t.Parallel()
			got := StatusGlyph(tt.status)
			if got != tt.want {
				t.Errorf("StatusGlyph(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

// --- View rendering ---

func TestModelViewNotReady(t *testing.T) {
	t.Parallel()
	events := make(chan orchestrator.Event, 10)
	_, cancel := context.WithCancel(context.Background())
	m := New(events, cancel, "test-project")

	view := m.View()
	if view != "\n  Initializing..." {
		t.Errorf("pre-ready view = %q, want initializing message", view)
	}
}

func TestModelViewQuitting(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)

	view := m.View()
	if view != "\n  Cancelled.\n" {
		t.Errorf("quitting view = %q, want cancelled message", view)
	}
}

func TestModelViewReady(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	view := m.View()
	if view == "" {
		t.Error("ready view should not be empty")
	}
	if view == "\n  Initializing..." {
		t.Error("ready view should not show initializing")
	}
}

// --- StatusBar ---

func TestStatusBarAddTokens(t *testing.T) {
	t.Parallel()
	keys := DefaultKeyMap()
	s := NewStatusBar(keys)

	s = s.AddTokens(100, 50, 0.05)
	s = s.AddTokens(200, 100, 0.10)

	if s.tokensIn != 300 {
		t.Errorf("tokensIn = %d, want 300", s.tokensIn)
	}
	if s.tokensOut != 150 {
		t.Errorf("tokensOut = %d, want 150", s.tokensOut)
	}
	if !approxEqual(s.totalCost, 0.15, 1e-9) {
		t.Errorf("totalCost = %f, want 0.15", s.totalCost)
	}
}

func TestStatusBarIncrTurns(t *testing.T) {
	t.Parallel()
	keys := DefaultKeyMap()
	s := NewStatusBar(keys)

	s = s.IncrTurns()
	s = s.IncrTurns()

	if s.turns != 2 {
		t.Errorf("turns = %d, want 2", s.turns)
	}
}

func TestStatusBarPaused(t *testing.T) {
	t.Parallel()
	keys := DefaultKeyMap()
	s := NewStatusBar(keys)

	s = s.SetPaused(true)
	if !s.paused {
		t.Error("expected paused=true")
	}

	s = s.SetPaused(false)
	if s.paused {
		t.Error("expected paused=false")
	}
}

// --- KeyMap ---

func TestKeyMapHelpGroups(t *testing.T) {
	t.Parallel()
	km := DefaultKeyMap()
	groups := km.HelpGroups()

	if len(groups) < 3 {
		t.Errorf("HelpGroups() len = %d, want at least 3", len(groups))
	}
	for i, g := range groups {
		if len(g) == 0 {
			t.Errorf("HelpGroups()[%d] is empty", i)
		}
	}
}

// --- HeaderLine utility ---

func TestHeaderLine(t *testing.T) {
	t.Parallel()

	got := HeaderLine("myproject", 3, 10, 1.50)
	want := "corvex · myproject · 3/10 done · $1.50"
	if got != want {
		t.Errorf("HeaderLine() = %q, want %q", got, want)
	}
}

// --- Event flow integration ---

func TestModelEventReviewStart(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)
	m = addTestTasks(m, "S01")

	ev := eventMsg(orchestrator.Event{Type: orchestrator.EventReviewStart, TaskID: "S01"})
	updated, _ := m.Update(ev)
	m = updated.(Model)

	if m.worker.phase != "review" {
		t.Errorf("worker phase = %q, want review", m.worker.phase)
	}
	if m.worker.activeTask != "S01" {
		t.Errorf("worker activeTask = %q, want S01", m.worker.activeTask)
	}
}

func TestModelEventReviewResult(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	ev := eventMsg(orchestrator.Event{Type: orchestrator.EventReviewResult, Message: "looks good"})
	updated, _ := m.Update(ev)
	m = updated.(Model)

	if len(m.worker.lines) != 1 {
		t.Fatalf("worker lines = %d, want 1", len(m.worker.lines))
	}
}

func TestModelEventCheckpoint(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	ev := eventMsg(orchestrator.Event{Type: orchestrator.EventCheckpoint, TaskID: "S01"})
	updated, _ := m.Update(ev)
	m = updated.(Model)

	if len(m.worker.lines) != 1 {
		t.Fatalf("worker lines = %d, want 1", len(m.worker.lines))
	}
}

func TestModelEventError(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	ev := eventMsg(orchestrator.Event{Type: orchestrator.EventError, Message: "something broke"})
	updated, _ := m.Update(ev)
	m = updated.(Model)

	if len(m.worker.lines) != 1 {
		t.Fatalf("worker lines = %d, want 1", len(m.worker.lines))
	}
}

func TestModelEventPlanStartComplete(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	ev := eventMsg(orchestrator.Event{Type: orchestrator.EventPlanStart})
	updated, _ := m.Update(ev)
	m = updated.(Model)

	if m.worker.phase != "plan" {
		t.Errorf("worker phase = %q, want plan", m.worker.phase)
	}
	if len(m.worker.lines) != 1 {
		t.Fatalf("worker lines after PlanStart = %d, want 1", len(m.worker.lines))
	}

	ev = eventMsg(orchestrator.Event{Type: orchestrator.EventPlanComplete})
	updated, _ = m.Update(ev)
	m = updated.(Model)

	if len(m.worker.lines) != 2 {
		t.Errorf("worker lines after PlanComplete = %d, want 2", len(m.worker.lines))
	}
}

func TestModelEventRetry(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	ev := eventMsg(orchestrator.Event{Type: orchestrator.EventRetry, TaskID: "S01", Attempt: 2})
	updated, _ := m.Update(ev)
	m = updated.(Model)

	if len(m.worker.lines) != 1 {
		t.Fatalf("worker lines = %d, want 1", len(m.worker.lines))
	}
}

// --- DAG panel SetProgress ---

func TestDAGPanelSetProgress(t *testing.T) {
	t.Parallel()

	d := NewDAGPanel()
	d = d.SetProgress(3, 10)

	if d.completed != 3 {
		t.Errorf("completed = %d, want 3", d.completed)
	}
	if d.totalTasks != 10 {
		t.Errorf("totalTasks = %d, want 10", d.totalTasks)
	}
}

// --- DAG panel UpdateTask ---

func TestDAGPanelUpdateTask(t *testing.T) {
	t.Parallel()

	d := NewDAGPanel()
	d = d.AddTasks([]TaskEntry{
		{ID: "T1", Title: "A", Status: types.StatusPending},
		{ID: "T2", Title: "B", Status: types.StatusPending},
	})

	d = d.UpdateTask("T1", types.StatusRunning, 0, 1)
	if d.tasks[0].Status != types.StatusRunning {
		t.Errorf("T1 status = %q, want RUNNING", d.tasks[0].Status)
	}

	d = d.UpdateTask("T1", types.StatusPassed, 5*time.Second, 1)
	if d.tasks[0].Duration != 5*time.Second {
		t.Errorf("T1 duration = %v, want 5s", d.tasks[0].Duration)
	}

	// Original T2 should be untouched
	if d.tasks[1].Status != types.StatusPending {
		t.Errorf("T2 status = %q, want PENDING", d.tasks[1].Status)
	}
}

// --- Truncate utility ---

func TestTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"over", "hello world", 5, "hell…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncate(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

// --- Model AddDAGTasks ---

func TestModelAddDAGTasks(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)

	tasks := []TaskEntry{
		{ID: "S01", Title: "First"},
		{ID: "S02", Title: "Second"},
	}
	m = m.AddDAGTasks(tasks)

	if len(m.dag.tasks) != 2 {
		t.Errorf("dag tasks = %d, want 2", len(m.dag.tasks))
	}
}

// --- StatusBar View ---

func TestStatusBarView(t *testing.T) {
	t.Parallel()
	keys := DefaultKeyMap()
	s := NewStatusBar(keys)
	s = s.SetSize(80)
	s = s.AddTokens(1500, 800, 0.25)
	s = s.IncrTurns()

	view := s.View()
	if view == "" {
		t.Error("StatusBar.View() should not be empty")
	}
}

// --- Immutability: value receiver copies ---

func TestDAGPanelImmutability(t *testing.T) {
	t.Parallel()

	d1 := NewDAGPanel()
	d1 = d1.AddTasks([]TaskEntry{
		{ID: "T1", Title: "A", Status: types.StatusPending},
	})

	d2 := d1.UpdateTask("T1", types.StatusRunning, 0, 1)

	if d1.tasks[0].Status != types.StatusPending {
		t.Error("original DAGPanel was mutated")
	}
	if d2.tasks[0].Status != types.StatusRunning {
		t.Error("new DAGPanel should have RUNNING")
	}
}

func TestWorkerPanelImmutability(t *testing.T) {
	t.Parallel()

	w1 := NewWorkerPanel()
	w1 = w1.SetSize(80, 10)
	w2 := w1.AppendStream(&types.StreamEvent{Type: types.EventText, Content: "hello"})

	if len(w1.lines) != 0 {
		t.Error("original WorkerPanel was mutated")
	}
	if len(w2.lines) != 1 {
		t.Error("new WorkerPanel should have 1 line")
	}
}

func TestStatusBarImmutability(t *testing.T) {
	t.Parallel()

	keys := DefaultKeyMap()
	s1 := NewStatusBar(keys)
	s2 := s1.AddTokens(100, 50, 0.05)

	if s1.tokensIn != 0 {
		t.Error("original StatusBar was mutated")
	}
	if s2.tokensIn != 100 {
		t.Error("new StatusBar should have tokensIn=100")
	}
}

// --- DAG panel View rendering ---

func TestDAGPanelViewEmpty(t *testing.T) {
	t.Parallel()
	d := NewDAGPanel()
	view := d.View()
	if view == "" {
		t.Error("empty DAG panel should show 'No tasks loaded'")
	}
}

func TestDAGPanelViewWithTasks(t *testing.T) {
	t.Parallel()
	d := NewDAGPanel()
	d = d.AddTasks([]TaskEntry{
		{ID: "S01", Title: "First task", Status: types.StatusPassed, Duration: 5 * time.Second},
		{ID: "S02", Title: "Second task", Status: types.StatusPending},
	})
	d = d.SetProgress(1, 2)
	d = d.SetSize(60, 20)

	view := d.View()
	if view == "" {
		t.Error("DAG panel view should not be empty with tasks")
	}
}

// --- Multiple complete events accumulate cost ---

func TestModelMultipleTaskCompleteCostAccumulation(t *testing.T) {
	t.Parallel()
	m := setupTestModel(t)
	m = addTestTasks(m, "S01", "S02")
	m.dag = m.dag.SetProgress(0, 2)

	for _, id := range []string{"S01", "S02"} {
		ev := eventMsg(orchestrator.Event{Type: orchestrator.EventTaskStart, TaskID: id, Attempt: 1})
		updated, _ := m.Update(ev)
		m = updated.(Model)

		ev = eventMsg(orchestrator.Event{
			Type:      orchestrator.EventTaskComplete,
			TaskID:    id,
			Status:    types.StatusPassed,
			CostUSD:   0.10,
			TokensIn:  500,
			TokensOut: 250,
		})
		updated, _ = m.Update(ev)
		m = updated.(Model)
	}

	if !approxEqual(m.status.totalCost, 0.20, 1e-9) {
		t.Errorf("totalCost = %f, want 0.20", m.status.totalCost)
	}
	if m.status.tokensIn != 1000 {
		t.Errorf("tokensIn = %d, want 1000", m.status.tokensIn)
	}
	if m.dag.completed != 2 {
		t.Errorf("completed = %d, want 2", m.dag.completed)
	}
}

// --- StatusStyle ---

func TestStatusStyle(t *testing.T) {
	t.Parallel()

	statuses := []types.TaskStatus{
		types.StatusPassed,
		types.StatusRunning,
		types.StatusFailed,
		types.StatusSkipped,
		types.StatusPending,
	}
	expected := []lipgloss.Style{
		StatusPassed,
		StatusRunning,
		StatusFailed,
		StatusSkippedStyle,
		StatusPending,
	}

	for i, s := range statuses {
		t.Run(string(s), func(t *testing.T) {
			t.Parallel()
			got := StatusStyle(s)
			if !reflect.DeepEqual(got, expected[i]) {
				t.Errorf("StatusStyle(%q) mismatch", s)
			}
		})
	}
}
