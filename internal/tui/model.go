package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

func (m Model) handleKey(msg tea.KeyMsg, cmds []tea.Cmd) (Model, []tea.Cmd) {
	// Filter mode captures keystrokes for the input.
	if m.status.Filtering() {
		switch msg.Type {
		case tea.KeyEsc:
			m.status = m.status.ExitFilter()
			m.dag = m.dag.SetFilter("")
		case tea.KeyEnter:
			m.status = m.status.ExitFilter()
		case tea.KeyBackspace:
			m.status = m.status.BackspaceFilter()
			m.dag = m.dag.SetFilter(m.status.Filter())
		case tea.KeyRunes:
			for _, r := range msg.Runes {
				m.status = m.status.AppendFilter(r)
			}
			m.dag = m.dag.SetFilter(m.status.Filter())
		}
		return m, cmds
	}

	// Modal-aware keys: only Esc / quit / help-toggle pass through.
	if m.modal != modalNone {
		switch {
		case key.Matches(msg, m.keys.Esc), key.Matches(msg, m.keys.Help) && m.modal == modalHelp:
			m.modal = modalNone
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			m.cancel()
			return m, append(cmds, tea.Quit)
		}
		return m, cmds
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		m.cancel()
		return m, append(cmds, tea.Quit)
	case key.Matches(msg, m.keys.Help):
		m.modal = modalHelp
	case key.Matches(msg, m.keys.Detail):
		if m.dag.SelectedTask() != nil {
			m.modal = modalDetail
		}
	case key.Matches(msg, m.keys.Filter):
		m.status = m.status.EnterFilter()
	case key.Matches(msg, m.keys.Pause):
		m.paused = !m.paused
		m.status = m.status.SetPaused(m.paused)
		m.sendCommand(orchestrator.Command{Type: pauseToggle(m.paused)})
	case key.Matches(msg, m.keys.Skip):
		if t := m.dag.SelectedTask(); t != nil && t.Status == types.StatusPending {
			m.sendCommand(orchestrator.Command{Type: orchestrator.CmdSkip, TaskID: t.ID})
		}
	case key.Matches(msg, m.keys.Retry):
		if t := m.dag.SelectedTask(); t != nil && t.Status == types.StatusFailed {
			m.sendCommand(orchestrator.Command{Type: orchestrator.CmdRetry, TaskID: t.ID})
		}
	case key.Matches(msg, m.keys.Logs):
		if t := m.dag.SelectedTask(); t != nil {
			if logsCmd := buildLogsCommand(m.project, t.ID); logsCmd != nil {
				cmds = append(cmds, tea.ExecProcess(logsCmd, nil))
			}
		}
	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Down):
		m.dag = m.dag.Update(msg)
	}
	return m, cmds
}

// buildLogsCommand assembles `corvex logs <project> <task> | $PAGER` for
// tea.ExecProcess. Returns nil when the running binary path cannot be
// resolved (in which case the `l` key becomes a no-op rather than crash).
func buildLogsCommand(project, taskID string) *exec.Cmd {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return nil
	}
	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less -R"
	}
	shellCmd := fmt.Sprintf("%q logs %q %q | %s", exe, project, taskID, pager)
	return exec.Command("sh", "-c", shellCmd)
}

func (m Model) sendCommand(cmd orchestrator.Command) {
	if m.commands == nil {
		return
	}
	select {
	case m.commands <- cmd:
	default:
		// Drop on full channel — control keys are advisory; user can
		// re-press.
	}
}

func pauseToggle(paused bool) orchestrator.CommandType {
	if paused {
		return orchestrator.CmdPause
	}
	return orchestrator.CmdResume
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
	statusView := m.status.View()

	// 1 header + status (2 lines: divider + body) = 3 lines; remainder for main.
	mainHeight := m.height - 3
	if mainHeight < 6 {
		mainHeight = 6
	}

	// DAG gets ~40% of remaining height, capped at 12 rows for readability.
	dagHeight := mainHeight * 40 / 100
	if dagHeight < 4 {
		dagHeight = 4
	}
	if dagHeight > 12 {
		dagHeight = 12
	}
	if dagHeight > mainHeight-3 {
		dagHeight = mainHeight - 3
	}

	workerHeight := mainHeight - dagHeight - 1 // 1 line for separator

	dagView := lipgloss.NewStyle().
		Width(m.width).
		Height(dagHeight).
		Render(m.dag.View())

	separator := Divider.Render(strings.Repeat("─", m.width))

	workerView := lipgloss.NewStyle().
		Width(m.width).
		Height(workerHeight).
		Render(m.worker.View())

	main := lipgloss.JoinVertical(lipgloss.Left, header, dagView, separator, workerView, statusView)

	if m.modal == modalHelp {
		return overlay(main, helpModalView(m.keys, m.width, m.height), m.width, m.height)
	}
	if m.modal == modalDetail {
		if t := m.dag.SelectedTask(); t != nil {
			return overlay(main, detailModalView(*t, m.width, m.height), m.width, m.height)
		}
	}

	return main
}

func (m Model) renderHeader() string {
	tasks := m.dag.Tasks()
	completed := 0
	for _, t := range tasks {
		if t.Status == types.StatusPassed {
			completed++
		}
	}
	total := len(tasks)

	left := HeaderTitle.Render("corvex") + TextMuted.Render(" · ") +
		Chip.Render(m.project)

	right := fmt.Sprintf("%s%s%s",
		TextMuted.Render(fmt.Sprintf("%d/%d", completed, total)),
		TextMuted.Render(" · "),
		CostStyle.Render(FormatCost(m.status.totalCost)),
	)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := m.width - leftW - rightW - 2
	if gap < 1 {
		gap = 1
	}

	return " " + left + strings.Repeat(" ", gap) + right + " "
}

func (m Model) resize() Model {
	mainHeight := m.height - 3
	if mainHeight < 6 {
		mainHeight = 6
	}
	dagHeight := mainHeight * 40 / 100
	if dagHeight < 4 {
		dagHeight = 4
	}
	if dagHeight > 12 {
		dagHeight = 12
	}
	if dagHeight > mainHeight-3 {
		dagHeight = mainHeight - 3
	}
	workerHeight := mainHeight - dagHeight - 1

	m.dag = m.dag.SetSize(m.width, dagHeight)
	m.worker = m.worker.SetSize(m.width, workerHeight)
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
		m.worker = m.worker.SetActiveTask(ev.TaskID, "worker")
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

// AddDAGTasks is a convenience to populate the DAG panel from orchestrator data.
func (m Model) AddDAGTasks(tasks []TaskEntry) Model {
	m.dag = m.dag.AddTasks(tasks)
	return m
}

// HeaderLine returns a simple header string for non-TUI contexts.
func HeaderLine(project string, completed, total int, cost float64) string {
	return fmt.Sprintf(
		"corvex · %s · %d/%d done · %s",
		project, completed, total, FormatCost(cost),
	)
}

// overlay centres `inner` over `background` so the modal floats above the
// main layout without breaking the rest of the screen geometry.
func overlay(background, inner string, w, h int) string {
	box := lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, inner,
		lipgloss.WithWhitespaceChars(" "),
	)
	_ = background
	return box
}
