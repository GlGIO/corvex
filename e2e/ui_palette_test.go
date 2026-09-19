package e2e

// ⌘K, driven from the keyboard.
//
// The panel was a read-only log of what the UI had done. The rule it carries is
// the product's own — no button does anything a person could not have typed —
// and the panel is where those lines are shown. What it could not do was let
// somebody type one, which for a keyboard-shaped surface in a tool used beside
// a terminal is the whole reason anyone presses ⌘K.
//
// Two things are measured here, and the second matters more than the first: the
// typed line RUNS, and the line that would bypass the gate's reading lock is
// NOT OFFERED.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const paletteRecipeYAML = `name: rapida
description: |
  Um passo curto, para exercitar o disparo pelo teclado.
stages:
  - id: S01
    kind: tool
    title: "Escreve"
    command: "echo disparado > palco.txt"
`

// paletteGateYAML parks on a human gate whose evidence must be READ before the
// approval is allowed. It is the recipe that makes the omission measurable.
const paletteGateYAML = `name: travada
description: |
  Um gate humano com leitura obrigatória.
stages:
  - id: S01
    kind: tool
    title: "O passo guardado"
    command: "echo ok > guardado.txt"
    gates:
      - nature: human
        when: before
        label: "A causa está confirmada?"
        prompt: "Leia a evidência antes de aprovar."
    evidence:
      - kind: diff
        label: "o estado da árvore"
        from: "git status --short"
        required_reading: true
`

func setupPaletteRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".corvex", "config.yaml"), identityConfigYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "rapida.yaml"), paletteRecipeYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "travada.yaml"), paletteGateYAML)
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"config", "user.email", "e2e@corvex"}, {"config", "user.name", "e2e"},
		{"add", "-A"}, {"commit", "-m", "recipes"},
	} {
		if out, err := gitIn(dir, args...); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	return dir
}

// openPalette presses ⌘K the way a person does — a real key event, not a call
// into the page's own function.
func openPaletteWith(t *testing.T, c *chrome, query string) {
	t.Helper()
	c.eval(t, `document.dispatchEvent(new KeyboardEvent('keydown', {key: 'k', metaKey: true, bubbles: true}))`, nil)
	c.waitFor(t, 10*time.Second, "the panel", `!document.querySelector('#palette').classList.contains('hidden')`)
	c.eval(t, `(() => { const i = document.querySelector('#palette-input'); i.value = `+jsString(query)+`; i.dispatchEvent(new Event('input')); })()`, nil)
}

func jsString(s string) string { return "'" + strings.ReplaceAll(s, "'", "\\'") + "'" }

func TestUI_PaletteRunsTheCommandThatWasTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupPaletteRepo(t)
	url := startUI(t, dir)
	c := startChrome(t)
	c.navigate(t, url)
	c.waitFor(t, 20*time.Second, "the app to boot", "typeof el === 'function'")

	openPaletteWith(t, c, "rapida")
	// The list narrowed to the line that starts it, and that line is the CLI
	// command — not a verb this UI invented.
	c.waitFor(t, 10*time.Second, "the start command",
		`[...document.querySelectorAll('#palette-list code')].some(x => x.textContent === 'corvex run start rapida')`)
	if n := c.evalInt(t, `document.querySelectorAll('#palette-list li').length`); n != 1 {
		t.Errorf("the query matched %d lines, want just the one that starts it", n)
	}

	// Enter, on the input, like a person.
	c.eval(t, `document.querySelector('#palette-input').dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', bubbles: true}))`, nil)

	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(dir, "palco.txt")); err == nil {
			if got := strings.TrimSpace(string(data)); got != "disparado" {
				t.Fatalf("the step wrote %q", got)
			}
			// And the panel closed itself: it is a launcher, not a window.
			if !c.evalBool(t, `document.querySelector('#palette').classList.contains('hidden')`) {
				t.Error("the panel stayed open after running a command")
			}
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("the run never started from the keyboard")
}

// The line that would walk past the reading lock is not on the list.
//
// The gate screen makes Approve wait until the evidence marked
// `required_reading` has been opened, and writes the acknowledgement with a
// timestamp. A palette entry `gate approve <run>` would be one keystroke
// through the middle of that — no evidence, and a ledger recording a reading
// that never happened. So the palette offers the way TO the gate and nothing
// else, and this test is what keeps it that way when somebody later thinks
// approving from the keyboard would be convenient.
func TestUI_PaletteWillNotApproveAGate(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupPaletteRepo(t)
	cmd, logOf := corvexRunDetached(t, dir, "run", "start", "travada", "--plain", "--yes", "--skip-doctor")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the run: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	url := startUI(t, dir)
	c := startChrome(t)
	c.navigate(t, url)
	c.waitFor(t, 20*time.Second, "the app to boot", "typeof el === 'function'")
	c.waitFor(t, 40*time.Second, "the gate to park", `document.body.innerText.includes('A causa está confirmada?')`)

	// "gate show" and not "gate": the panel's first line for a bare "gate" is
	// `gate list`, which is this very screen. The query names what is wanted.
	openPaletteWith(t, c, "gate show")
	c.waitFor(t, 10*time.Second, "the gate's own line",
		`[...document.querySelectorAll('#palette-list code')].some(x => x.textContent.startsWith('corvex gate show'))`)

	// The forbidden lines are looked for across the WHOLE candidate set, not
	// just the filtered view: a `gate approve` that only shows up under another
	// query is still a `gate approve`.
	c.eval(t, `(() => { const i = document.querySelector('#palette-input'); i.value = ''; i.dispatchEvent(new Event('input')); })()`, nil)
	lines := c.evalString(t, `[...document.querySelectorAll('#palette-list code')].map(x => x.textContent).join('\n')`)
	for _, forbidden := range []string{"gate approve", "gate reject", "gate answer"} {
		if strings.Contains(lines, forbidden) {
			t.Errorf("the palette offers %q — that is one keystroke past the reading lock:\n%s\n%s", forbidden, lines, logOf())
		}
	}

	// And what it DOES offer takes you to the screen that holds the lock.
	c.eval(t, `(() => { const i = document.querySelector('#palette-input'); i.value = 'gate show'; i.dispatchEvent(new Event('input')); })()`, nil)
	c.eval(t, `document.querySelector('#palette-input').dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', bubbles: true}))`, nil)
	c.waitFor(t, 15*time.Second, "the gate screen", `document.body.innerText.includes('Leia a evidência antes de aprovar')`)
	// The lock is doing its job on the screen it took us to: Approve is there
	// and refuses until the evidence is opened.
	if !c.evalBool(t, `[...document.querySelectorAll('button')].some(b => b.textContent === 'Approve' && b.hasAttribute('disabled'))`) {
		t.Error("the gate screen the palette opened does not hold its reading lock")
	}
}
