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
    command: "echo 'a coluna operacao_origem nao existe na replica' && exit 1"
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
	c.waitFor(t, 15*time.Second, "the failed run to be listed", `document.body.innerText.includes('failed')`)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Open').click()`, nil)

	// The run screen names WHERE it failed rather than leaving it to be found.
	c.waitFor(t, 15*time.Second, "the run to name the failed step",
		`document.body.innerText.includes('failed at') && document.body.innerText.includes('S02')`)

	// And one click gets the sentence the step actually printed.
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
