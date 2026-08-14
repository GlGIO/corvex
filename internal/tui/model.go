// Package tui renders the Corvex run dashboard. This file holds the
// Bubbletea lifecycle: message types, the Model itself, Init/Update and the
// orchestrator-event reducer. Rendering lives in view.go, key handling in
// input.go, startup back-fill in seed.go.
package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/types"
)

type eventMsg orchestrator.Event

type channelClosedMsg struct{}

type tickMsg time.Time

// modalKind identifies which full-screen overlay (if any) is currently open.
type modalKind int

const (
	modalNone modalKind = iota
	modalHelp
	modalDetail
)

// Model is the top-level Bubbletea model. Layout is vertical:
// header → DAG → divider → worker stream → status bar. Modals are
// full-screen overlays drawn on top.
type Model struct {
	dag      DAGPanel
	worker   WorkerPanel
	status   StatusBar
	keys     KeyMap
	events   <-chan orchestrator.Event
	commands chan<- orchestrator.Command
	cancel   context.CancelFunc
	project  string
	ready    bool
	quitting bool
	done     bool
	paused   bool
	modal    modalKind
	width    int
	height   int
}

// New creates a TUI model connected to the orchestrator event channel.
// `commands` (optional) is the channel the model uses to deliver
// pause/skip/retry requests back to the orchestrator.
func New(events <-chan orchestrator.Event, cancel context.CancelFunc, project string) Model {
	return NewWithCommands(events, nil, cancel, project)
}

// NewWithCommands is like New but also wires a command channel so the
// orchestrator can react to runtime control keys.
func NewWithCommands(events <-chan orchestrator.Event, commands chan<- orchestrator.Command, cancel context.CancelFunc, project string) Model {
	keys := DefaultKeyMap()
	return Model{
		dag:      NewDAGPanel(),
		worker:   NewWorkerPanel(),
		status:   NewStatusBar(keys),
		keys:     keys,
		events:   events,
		commands: commands,
		cancel:   cancel,
		project:  project,
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
		m, cmds = m.handleKey(msg, cmds)

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

	// Delegate viewport updates only when no modal is open; otherwise the
	// modal owns the keyboard.
	if m.modal == modalNone {
		var vpCmd tea.Cmd
		m.worker, vpCmd = m.worker.Update(msg)
		if vpCmd != nil {
			cmds = append(cmds, vpCmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m Model) handleEvent(ev orchestrator.Event) Model {
	switch ev.Type {
	case orchestrator.EventDAGResolved:
		m.dag = m.dag.SetProgress(0, ev.Total)

	case orchestrator.EventTaskStart:
		m.dag = m.dag.UpdateTask(ev.TaskID, types.StatusRunning, 0, ev.Attempt)
		m.dag = m.dag.ScrollToTask(ev.TaskID)
		m.worker = m.worker.Clear()
		m.worker = m.worker.SetActiveTask(ev.TaskID, "worker")
		m.status = m.status.IncrTurns()

	case orchestrator.EventTaskStream:
		m.worker = m.worker.AppendStream(ev.Stream)

	case orchestrator.EventTaskWarn:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: "⚠ " + ev.Message,
		})

	case orchestrator.EventTaskComplete:
		dur := time.Duration(ev.DurationMs) * time.Millisecond
		m.dag = m.dag.UpdateTask(ev.TaskID, ev.Status, dur, ev.Attempt)
		m.status = m.status.AddTokens(ev.TokensIn, ev.TokensOut, ev.CostUSD)
		if ev.Status == types.StatusPassed {
			m.dag = m.dag.SetProgress(m.dag.completed+1, m.dag.totalTasks)
		}

	case orchestrator.EventReviewStart:
		m.worker = m.worker.SetActiveTask(ev.TaskID, "review")

	case orchestrator.EventReviewResult:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: "review: " + ev.Message,
		})

	case orchestrator.EventCheckpoint:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: fmt.Sprintf("checkpoint saved for %s", ev.TaskID),
		})

	case orchestrator.EventDone:
		m.done = true

	case orchestrator.EventError:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventError,
			Content: ev.Message,
		})

	case orchestrator.EventPlanStart:
		m.worker = m.worker.SetActiveTask("", "plan")
		content := ev.Message
		if content == "" {
			content = "Planning tasks..."
		}
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: content,
		})

	case orchestrator.EventPlanComplete:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: "Planning complete.",
		})

	case orchestrator.EventRecoveryCheck:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: "Checking working tree for leftover state...",
		})

	case orchestrator.EventRecoveryResult:
		msg := ev.Message
		if msg == "" {
			msg = "Recovery check complete."
		}
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: msg,
		})

	case orchestrator.EventRetry:
		m.worker = m.worker.AppendStream(&types.StreamEvent{
			Type:    types.EventText,
			Content: fmt.Sprintf("retrying %s (attempt %d)", ev.TaskID, ev.Attempt),
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
