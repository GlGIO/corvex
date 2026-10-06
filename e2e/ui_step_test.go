package e2e

// "Why did this run fail?" — answered on the screen, not only in a terminal.
//
// `corvex run show <id> --step S02` has always answered it: the ledger's
// timeline plus the tail of what the step printed. The API carried it under
// `?step=`, and the page never asked. So a run reported as `failed` was a dead
// end for anyone who had closed the terminal that started it — which is every
// run the UI itself dispatches, since those are detached by construction.
//
// The run here is pure shell: no provider, no tokens, no network. The failure is
// a command that prints a sentence and exits 1, which is the shape of every real
// failure this tool has to make legible.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The failing stage reads a marker that does not exist yet, so the first run
// fails and a retry AFTER the marker is committed passes. That is the workflow
// the button exists for: the step failed, a person fixed the cause, and only
// that step should run again.
const failingRecipeYAML = `name: falha
description: |
  Um stage que passa e um que falha, para o e2e do detalhe de step.
stages:
  - id: S01
    kind: test
    title: "Passa"
    command: "echo tudo bem"
  - id: S02
    kind: test
    title: "Falha dizendo por quê"
    depends_on: [S01]
    command: "test -f corrigido.txt || (echo 'a coluna operacao_origem nao existe na replica' && exit 1)"
`

func setupFailingRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".corvex", "config.yaml"), identityConfigYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "falha.yaml"), failingRecipeYAML)
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "e2e@corvex"},
		{"git", "config", "user.name", "e2e"},
		{"git", "add", "-A"},
		{"git", "commit", "-m", "recipe"},
	} {
		if out, err := gitIn(dir, args[1:]...); err != nil {
			t.Fatalf("git %v: %s: %v", args[1:], out, err)
		}
	}
	return dir
}

func TestUI_AFailedStepSaysWhatItPrinted(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupFailingRepo(t)

	// The run is expected to fail: that is the fixture, not an accident.
	out, err := corvexCLI(t, dir, "run", "start", "falha", "--plain", "--yes", "--skip-doctor")
	if err == nil {
		t.Fatalf("the fixture run was supposed to fail:\n%s", out)
	}
	if !strings.Contains(out, "S02") {
		t.Fatalf("the run never reached the failing step:\n%s", out)
	}

	url := startUI(t, dir)
	c := startChrome(t)
	c.navigate(t, url)
	c.waitFor(t, 20*time.Second, "the app to boot", "typeof el === 'function'")

	// Get to the run the way a person does: the history tab, then the card.
	c.eval(t, `[...document.querySelectorAll('.tab')].find(t => t.dataset.view === 'runs').click()`, nil)
	c.waitFor(t, 15*time.Second, "the failed run to be listed", `document.body.innerText.toLowerCase().includes('failed')`)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Open').click()`, nil)

	// The run screen names WHERE it failed rather than leaving it to be found.
	c.waitFor(t, 15*time.Second, "the run to name the failed step",
		`document.body.innerText.toLowerCase().includes('failed at') && document.body.innerText.includes('S02')`)

	// And one click gets the sentence the step actually printed — aimed after
	// the screen has stopped moving, for the reason `settled` explains: a run
	// that just failed is still settling, every settling step is a legitimate
	// redraw, and a redraw replaces the node the click is about to land on.
	settled(t, c)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Why').click()`, nil)
	c.waitFor(t, 15*time.Second, "the step output to appear", `document.querySelector('pre.log')`)
	printed := c.evalString(t, "document.querySelector('pre.log').innerText")
	if !strings.Contains(printed, "operacao_origem") {
		t.Errorf("the step screen does not show what the command printed:\n%s", printed)
	}
}

// gitIn is the smallest possible git helper for this file; the other e2e files
// keep their own for the same reason (a shared one would have to grow options
// for every caller's environment).
func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The whole point of retrying ONE step: the run's other steps are not redone.
func TestUI_RetryReExecutesOnlyTheFailedStep(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupFailingRepo(t)
	if out, err := corvexCLI(t, dir, "run", "start", "falha", "--plain", "--yes", "--skip-doctor"); err == nil {
		t.Fatalf("the fixture run was supposed to fail:\n%s", out)
	}

	// The cause, fixed by a person — committed, because corvex refuses to run on
	// a dirty tree and that refusal is not what this test is about.
	if err := os.WriteFile(filepath.Join(dir, "corrigido.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "conserta a causa"}} {
		if out, err := gitIn(dir, args...); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}

	url := startUI(t, dir)
	c := startChrome(t)
	c.navigate(t, url)
	c.waitFor(t, 20*time.Second, "the app to boot", "typeof el === 'function'")
	c.eval(t, `[...document.querySelectorAll('.tab')].find(t => t.dataset.view === 'runs').click()`, nil)
	c.waitFor(t, 15*time.Second, "the failed run to be listed", `document.body.innerText.toLowerCase().includes('failed')`)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Open').click()`, nil)
	c.waitFor(t, 15*time.Second, "the run screen", `document.body.innerText.toLowerCase().includes('failed at')`)

	// `confirm` is a modal in a headless browser: answer it before clicking.
	c.eval(t, `window.confirm = () => true`, nil)
	// WAIT FOR THE SCREEN TO STOP MOVING before aiming at a button on it.
	//
	// The run screen redraws when the run moves, and a run that has just failed
	// is still settling for a moment — the record's liveness goes from alive to
	// finished, the ledger's last lines land. Each of those is a legitimate
	// redraw, and a redraw replaces the node the click is about to land on.
	// This was an intermittent failure under full-suite load, roughly one run
	// in three, and reading it as flakiness would have been reading it wrong:
	// the page was doing exactly what it should, and the test was clicking
	// into the middle of it.
	//
	// `state.runSig` is the page's own answer to "has anything changed", so
	// two equal reads of it are the screen saying it is done moving.
	settled(t, c)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Retry step').click()`, nil)

	// The step runs again and passes this time, and the step that had already
	// passed is not touched: its ledger keeps exactly one completion.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := corvexCLI(t, dir, "run", "show", "falha")
		if strings.Contains(out, "2/2 steps") {
			// One COMPLETION per execution. The first version of this counted
			// every ledger line mentioning S01 — task_start, tool_use,
			// tool_result, checkpoint — and reported five executions of a step
			// that ran once. The ledger was right; the assertion was reading it
			// as if each line were a run.
			ledger, _ := os.ReadFile(filepath.Join(dir, ".corvex", "tasks", "falha", "activity.jsonl"))
			if got := completions(string(ledger), "S01"); got != 1 {
				t.Errorf("S01 completed %d times: a retry of S02 re-executed a step that had already passed", got)
			}
			if got := completions(string(ledger), "S02"); got != 2 {
				t.Errorf("S02 completed %d times, want 2 (the failure and the retry)", got)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	out, _ := corvexCLI(t, dir, "run", "show", "falha")
	t.Fatalf("the retried step never passed:\n%s", out)
}

// completions counts how many times a step finished, which is one ledger line
// per execution — not one per event the execution emitted.
func completions(ledger, step string) int {
	n := 0
	for _, line := range strings.Split(ledger, "\n") {
		if strings.Contains(line, `"task_id":"`+step+`"`) && strings.Contains(line, `"type":"task_complete"`) {
			n++
		}
	}
	return n
}

// settled waits until the run screen reports the same signature twice in a row,
// which is the page's own statement that it has stopped redrawing.
func settled(t *testing.T, c *chrome) {
	t.Helper()
	last := ""
	stable := 0
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		sig := c.evalString(t, `String(state.runSig || '')`)
		if sig != "" && sig == last {
			if stable++; stable >= 2 {
				return
			}
		} else {
			stable = 0
		}
		last = sig
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("the run screen never stopped redrawing")
}
