package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/types"
)

type eventMsg orchestrator.Event

type channelClosedMsg struct{}

type tickMsg time.Time

// Model is the top-level Bubbletea model composing DAG, Worker, and Status panels.
type Model struct {
	dag      DAGPanel
	worker   WorkerPanel
	status   StatusBar
	keys     KeyMap
	events   <-chan orchestrator.Event
	cancel   context.CancelFunc
	project  string
	ready    bool
	quitting bool
	done     bool
	paused   bool
	width    int
	height   int
}

// New creates a TUI model connected to the orchestrator event channel.
func New(events <-chan orchestrator.Event, cancel context.CancelFunc, project string) Model {
	keys := DefaultKeyMap()
	return Model{
		dag:    NewDAGPanel(),
		worker: NewWorkerPanel(),
		status: NewStatusBar(keys),
		keys:   keys,
		events: events,
		cancel: cancel,
		project: project,
	}
}

// Init starts the event listener and tick timer.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		waitForEvent(m.events),
		tickCmd(),
	)
}

// Update handles all incoming messages and dispatches to sub-models.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m = m.resize()

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			m.cancel()
			return m, tea.Quit
		case key.Matches(msg, m.keys.Pause):
			m.paused = !m.paused
			m.status = m.status.SetPaused(m.paused)
		case key.Matches(msg, m.keys.Up):
			m.dag = m.dag.Update(msg)
		case key.Matches(msg, m.keys.Down):
			m.dag = m.dag.Update(msg)
		}

	case eventMsg:
		m = m.handleEvent(orchestrator.Event(msg))
		cmds = append(cmds, waitForEvent(m.events))

	case channelClosedMsg:
		m.done = true
		return m, tea.Quit

	case tickMsg:
		m.status = m.status.Tick(time.Time(msg))
		cmds = append(cmds, tickCmd())
	}

	// Delegate viewport updates
	var vpCmd tea.Cmd
	m.worker, vpCmd = m.worker.Update(msg)
	if vpCmd != nil {
		cmds = append(cmds, vpCmd)
	}

	return m, tea.Batch(cmds...)
}

// View renders the full TUI layout.
func (m Model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	if m.quitting {
		return "\n  Cancelled.\n"
	}

	header := m.renderHeader()

	dagWidth := m.width * 40 / 100
	workerWidth := m.width - dagWidth

	// Main content height: total minus header (1) and status bar (3 lines)
	mainHeight := m.height - 4
	if mainHeight < 3 {
		mainHeight = 3
	}

	dagView := DAGPanelStyle.
		Width(dagWidth - 2). // minus border
		Height(mainHeight - 2). // minus border
		Render(m.dag.View())

	workerView := WorkerPanelStyle.
		Width(workerWidth - 2).
		Height(mainHeight - 2).
		Render(m.worker.View())

	main := lipgloss.JoinHorizontal(lipgloss.Top, dagView, workerView)
	statusView := m.status.View()

	return lipgloss.JoinVertical(lipgloss.Left, header, main, statusView)
}

func (m Model) renderHeader() string {
	completed := 0
	total := 0
	for _, t := range m.dag.tasks {
		total++
		if t.Status == types.StatusPassed {
			completed++
		}
	}

	headerText := fmt.Sprintf(
		"corvex ─── %s ─── %d/%d done ─── %s",
		m.project,
		completed,
		total,
		CostStyle.Render(FormatCost(m.status.totalCost)),
	)

	return HeaderStyle.Render(headerText)
}

func (m Model) resize() Model {
	dagWidth := m.width * 40 / 100
	workerWidth := m.width - dagWidth

	mainHeight := m.height - 4
	if mainHeight < 3 {
		mainHeight = 3
	}

	// Panel inner height (minus border top/bottom)
	innerHeight := mainHeight - 2
	if innerHeight < 1 {
		innerHeight = 1
	}

	m.dag = m.dag.SetSize(dagWidth, innerHeight)
	m.worker = m.worker.SetSize(workerWidth, innerHeight)
	m.status = m.status.SetSize(m.width)
	return m
}

func (m Model) handleEvent(ev orchestrator.Event) Model {
	switch ev.Type {
	case orchestrator.EventDAGResolved:
		m.dag = m.dag.SetProgress(0, ev.Total)

	case orchestrator.EventTaskStart:
		m.dag = m.dag.UpdateTask(ev.TaskID, types.StatusRunning, 0, ev.Attempt)
		m.worker = m.worker.Clear()
		m.worker = m.worker.SetActiveTask(ev.TaskID, "Worker")
		m.status = m.status.IncrTurns()

	case orchestrator.EventTaskStream:
		m.worker = m.worker.AppendStream(ev.Stream)

	case orchestrator.EventTaskComplete:
		dur := time.Duration(ev.DurationMs) * time.Millisecond
		m.dag = m.dag.UpdateTask(ev.TaskID, ev.Status, dur, ev.Attempt)
		m.status = m.status.AddTokens(ev.TokensIn, ev.TokensOut, ev.CostUSD)
		if ev.Status == types.StatusPassed {
			m.dag = m.dag.SetProgress(m.dag.completed+1, m.dag.totalTasks)
		}

	case orchestrator.EventReviewStart:
		m.worker = m.worker.SetActiveTask(ev.TaskID, "Reviewer")

	case orchestrator.EventReviewResult:
		line := fmt.Sprintf("Review: %s", ev.Message)
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: line,
		})

	case orchestrator.EventCheckpoint:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: fmt.Sprintf("✅ Checkpoint saved for %s", ev.TaskID),
		})

	case orchestrator.EventDone:
		m.done = true

	case orchestrator.EventError:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventError,
			Content: fmt.Sprintf("❌ Error: %s", ev.Message),
		})

	case orchestrator.EventPlanStart:
		m.worker = m.worker.SetActiveTask("", "Planner")
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: "Planning tasks...",
		})

	case orchestrator.EventPlanComplete:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: "Planning complete.",
		})

	case orchestrator.EventRetry:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: fmt.Sprintf("🔄 Retrying %s (attempt %d)", ev.TaskID, ev.Attempt),
		})
	}

	return m
}

func waitForEvent(ch <-chan orchestrator.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return channelClosedMsg{}
		}
		return eventMsg(ev)
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// AddDAGTasks is a convenience to populate the DAG panel from orchestrator data.
func (m Model) AddDAGTasks(tasks []TaskEntry) Model {
	m.dag = m.dag.AddTasks(tasks)
	return m
}

// HeaderLine returns a simple header string for non-TUI contexts.
func HeaderLine(project string, completed, total int, cost float64) string {
	return fmt.Sprintf(
		"corvex ─── %s ─── %d/%d done ─── %s",
		project, completed, total, FormatCost(cost),
	)
}
