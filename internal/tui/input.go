package tui

// Keyboard handling for the run dashboard: the filter-mode capture, the
// modal-aware key filter and the control keys that talk back to the
// orchestrator. The KeyMap itself lives in keys.go.

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/types"
)

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
