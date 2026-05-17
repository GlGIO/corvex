package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

// helpModalView renders the full-screen keyboard reference overlay.
// Bindings are pulled from the live keymap so they cannot drift from
// what the model actually handles.
func helpModalView(km KeyMap, screenW, screenH int) string {
	groups := km.HelpGroups()
	labels := []string{"navigation", "modes", "actions", "exit"}

	var sections []string
	for i, group := range groups {
		var label string
		if i < len(labels) {
			label = labels[i]
		}

		var rows []string
		for _, b := range group {
			rows = append(rows, renderBinding(b))
		}

		section := ModalLabel.Render(strings.ToUpper(label)) + "\n" +
			strings.Join(rows, "\n")
		sections = append(sections, section)
	}

	body := strings.Join(sections, "\n\n")
	footer := TextFaint.Render("esc to close")

	content := ModalTitle.Render("Keyboard reference") + "\n" + body + "\n\n" + footer

	w := minInt(screenW-8, 56)
	if w < 30 {
		w = 30
	}
	h := minInt(screenH-6, lipgloss.Height(content)+2)

	return ModalStyle.Width(w).Height(h).Render(content)
}

func renderBinding(b key.Binding) string {
	help := b.Help()
	if help.Key == "" {
		return ""
	}
	return "  " + KeyChip.Render(pad(help.Key, 6)) + KeyHint.Render(help.Desc)
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
