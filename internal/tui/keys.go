package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines all keyboard shortcuts for the TUI. Each binding maps
// to a real action; nothing is purely decorative.
type KeyMap struct {
	Quit   key.Binding
	Help   key.Binding
	Filter key.Binding
	Detail key.Binding
	Esc    key.Binding
	Pause  key.Binding
	Skip   key.Binding
	Retry  key.Binding
	Logs   key.Binding
	Up     key.Binding
	Down   key.Binding
}

// DefaultKeyMap returns the standard keybindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter"),
		),
		Detail: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("↵", "detail"),
		),
		Esc: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Pause: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "pause"),
		),
		Skip: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "skip"),
		),
		Retry: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "retry"),
		),
		Logs: key.NewBinding(
			key.WithKeys("l"),
			key.WithHelp("l", "logs"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
	}
}

// HelpGroups returns the bindings grouped for the help modal.
func (k KeyMap) HelpGroups() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Detail, k.Esc},  // navigation
		{k.Filter, k.Help},                // modes
		{k.Pause, k.Skip, k.Retry, k.Logs}, // actions
		{k.Quit},                          // exit
	}
}
