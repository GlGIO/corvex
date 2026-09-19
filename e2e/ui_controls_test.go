package e2e

// The three run controls, pressed in a browser.
//
// Pause, Resume and Stop have been exercised from the CLI and through the API.
// The buttons that carry them on the screen had never been clicked, and they are
// the buttons an operator reaches for at the worst moment — the run is doing
// something expensive and they want it to stop, or to wait.
//
// What the two tests below pin is the DIFFERENCE between the two verbs, because
// it is the thing a person has to be able to trust under pressure: pause holds
// the run at its next wave and keeps what is in flight; stop signals it and
// loses that work. A screen where those two feel the same is a screen where
// somebody picks the wrong one.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// slowRecipe has two waves of a slow shell step, so there is always something in
// flight to pause against and a barrier to hold at.
const slowRecipeYAML = `name: devagar
description: |
  Dois passos lentos, para exercitar pausa e parada.
stages:
  - id: S01
    kind: tool
    title: "Primeiro"
    command: "echo um >> passos.txt && sleep 3"
  - id: S02
    kind: tool
    title: "Segundo"
    depends_on: [S01]
    command: "echo dois >> passos.txt && sleep 3"
`

// slowestRecipeYAML is for the STOP test, and the long sleep is the point: the
// first version of that test used the three-second recipe, the run finished
// while Chrome was still booting, and the screen honestly reported `done` — a
// fixture measuring the test harness's startup time instead of the button.
const slowestRecipeYAML = `name: bem-devagar
description: |
  Um passo longo o bastante para ser interrompido.
stages:
  - id: S01
    kind: tool
    title: "Primeiro"
    command: "echo um >> passos.txt && sleep 30"
  - id: S02
    kind: tool
    title: "Segundo"
    depends_on: [S01]
    command: "echo dois >> passos.txt"
`

func setupSlowRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".corvex", "config.yaml"), identityConfigYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "devagar.yaml"), slowRecipeYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "bem-devagar.yaml"), slowestRecipeYAML)
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "e2e@corvex"}, {"config", "user.name", "e2e"},
		{"add", "-A"}, {"commit", "-m", "recipe"},
	} {
		if out, err := gitIn(dir, args...); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	return dir
}

// openRunsTab boots the page and lands on the history, where the run cards and
// their controls live.
func openRunsTab(t *testing.T, url string) *chrome {
	t.Helper()
	c := startChrome(t)
	c.navigate(t, url)
	c.waitFor(t, 20*time.Second, "the app to boot", "typeof el === 'function'")
	c.eval(t, `window.confirm = () => true`, nil) // Stop asks; a headless browser has nobody to ask
	c.eval(t, `[...document.querySelectorAll('.tab')].find(t => t.dataset.view === 'runs').click()`, nil)
	return c
}

func TestUI_PauseAndResumeFromTheScreen(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupSlowRepo(t)
	cmd, logOf := corvexRunDetached(t, dir, "run", "start", "devagar", "--plain", "--yes", "--skip-doctor")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the run: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	url := startUI(t, dir)
	c := openRunsTab(t, url)
	c.waitFor(t, 30*time.Second, "a live run on the screen",
		`[...document.querySelectorAll('.card .pill')].some(p => p.textContent === 'alive')`)

	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Pause').click()`, nil)
	// The CARD, not the page: the status bar at the bottom says "paused — corvex
	// run pause …" the instant the button is pressed, and waiting on the page
	// text would be waiting on this page's own echo of the click instead of on
	// the run's record. The first version of this test did exactly that and then
	// blamed the screen for not offering Resume yet.
	c.waitFor(t, 30*time.Second, "the run's own card to report it paused",
		`[...document.querySelectorAll('.card .pill')].some(p => p.textContent === 'paused')`)

	// Pause KEEPS the work: the step that was in flight finished, so the file
	// the first step writes is there. This is the half that separates pause
	// from stop, and the half a person is trusting when they press it.
	data, err := os.ReadFile(filepath.Join(dir, "passos.txt"))
	if err != nil || !strings.Contains(string(data), "um") {
		t.Fatalf("the step in flight did not finish before the pause held: %v (%q)\n%s", err, data, logOf())
	}

	// And Resume is offered in its place, not beside it: a paused run cannot be
	// paused again.
	if c.evalBool(t, `[...document.querySelectorAll('button')].some(b => b.textContent === 'Pause')`) {
		t.Error("a paused run still offers Pause")
	}
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Resume').click()`, nil)

	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(dir, "passos.txt")); err == nil && strings.Contains(string(data), "dois") {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("the resumed run never reached its second step\n%s", logOf())
}

func TestUI_StopFromTheScreen(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupSlowRepo(t)
	cmd, logOf := corvexRunDetached(t, dir, "run", "start", "bem-devagar", "--plain", "--yes", "--skip-doctor")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the run: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	url := startUI(t, dir)
	c := openRunsTab(t, url)
	c.waitFor(t, 30*time.Second, "a live run on the screen",
		`[...document.querySelectorAll('.card .pill')].some(p => p.textContent === 'alive')`)

	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Stop').click()`, nil)

	// `canceling` first — the run writes it and unwinds — and the screen must
	// show that state rather than jumping to a tidy ending it has not reached.
	c.waitFor(t, 30*time.Second, "the run's own card to report it is stopping",
		`[...document.querySelectorAll('.card .pill')].some(p => p.textContent === 'canceling' || p.textContent === 'canceled')`)

	// The second step never started: stopping is not "finish the current wave".
	if data, err := os.ReadFile(filepath.Join(dir, "passos.txt")); err == nil && strings.Contains(string(data), "dois") {
		t.Errorf("the run walked into its next step after Stop:\n%s\n%s", data, logOf())
	}
}
