package e2e

// Answering a question FROM THE SCREEN, and the work using the reply.
//
// The runner can ask now — a `question` gate parks the run, the answer reaches a
// command as $CORVEX_GATE_ANSWER and an agent as part of its prompt. All of that
// was built and exercised from the CLI. The screen has had the textarea and the
// Answer button since the UI was written, and nobody had ever typed into them,
// which for a tool whose point is "operate this without the terminal" leaves the
// most conversational thing it does reachable only from a terminal.
//
// The run here is pure shell: no provider, no tokens. What is under test is the
// path from a person's keystrokes to the step's environment.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const questionRecipeYAML = `name: pergunta
description: |
  Um stage que pergunta antes de agir, e usa a resposta.
stages:
  - id: S01
    kind: tool
    title: "Usa a resposta"
    gates:
      - nature: question
        when: before
        label: "Qual branch é o alvo?"
        prompt: "A release ativa não foi encontrada. Qual branch deve receber este PR?"
    command: |
      set -euo pipefail
      printf 'alvo=%s\n' "${CORVEX_GATE_ANSWER:-<vazio>}" > alvo.txt
`

func setupQuestionRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".corvex", "config.yaml"), identityConfigYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "pergunta.yaml"), questionRecipeYAML)
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

func TestUI_AnsweringAQuestionFromTheScreenReachesTheStep(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupQuestionRepo(t)

	// The run parks on the question and waits for a person. It is started
	// detached because the whole point is that the answer comes from somewhere
	// else — here, a browser.
	cmd, logOf := corvexRunDetached(t, dir, "run", "start", "pergunta", "--plain", "--yes", "--skip-doctor")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the run: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	url := startUI(t, dir)
	c := startChrome(t)
	c.navigate(t, url)
	c.waitFor(t, 20*time.Second, "the app to boot", "typeof el === 'function'")

	// The inbox shows it as something waiting on a person.
	c.waitFor(t, 30*time.Second, "the question to reach the inbox",
		`document.body.innerText.includes('Qual branch é o alvo?')`)

	// Open it, type, send — the way a person does.
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Review').click()`, nil)
	c.waitFor(t, 15*time.Second, "the answer box", "document.querySelector('textarea')")

	// A question gate offers the verb that answers it and not the one that
	// approves: an approval here says nothing, and the server refuses it.
	if c.evalBool(t, `[...document.querySelectorAll('button')].some(b => b.textContent === 'Approve')`) {
		t.Error("the screen offered Approve on a question — the server refuses it, and a button that is always refused is worse than none")
	}

	c.eval(t, `document.querySelector('textarea').value = 'release/1.8.0'`, nil)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Answer').click()`, nil)

	// And the step ran with what was typed.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(dir, "alvo.txt")); err == nil {
			if got := strings.TrimSpace(string(data)); got != "alvo=release/1.8.0" {
				t.Fatalf("the step saw %q, want the answer typed in the browser", got)
			}
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("the step never ran with the answer\nrun log:\n%s", logOf())
}

// corvexRunDetached is corvexRun without the stub provider and with the argv the
// caller wants: this run is pure shell, and what has to happen elsewhere is a
// person answering.
func corvexRunDetached(t *testing.T, dir string, args ...string) (*exec.Cmd, func() string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "corvex.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logFile.Close() })

	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	return cmd, func() string {
		data, _ := os.ReadFile(logPath)
		return string(data)
	}
}

// Rejecting a human gate FROM THE SCREEN, with a reason that survives.
//
// Round 12 made the reason reach the ledger, measured from the CLI. The screen
// has a "why (optional)" box that nobody had ever typed into — and the reason is
// worth more from the screen than from a terminal, because the person who
// refuses in a browser is the one whose terminal is closed when somebody else
// reads the run tomorrow.
func TestUI_RejectingWithAReasonFromTheScreenSurvives(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".corvex", "config.yaml"), identityConfigYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "freio.yaml"), `name: freio
description: |
  Um passo irreversível atrás de um gate humano.
stages:
  - id: S01
    kind: tool
    title: "O passo irreversível"
    command: "echo 'NAO PODE TER RODADO' > executou.txt"
    gates:
      - nature: human
        when: before
        label: "Aplicar a migration em STG?"
        prompt: "Isto aplica a migration. Aprova?"
`)
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "e2e@corvex"}, {"config", "user.name", "e2e"},
		{"add", "-A"}, {"commit", "-m", "recipe"},
	} {
		if out, err := gitIn(dir, args...); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}

	cmd, _ := corvexRunDetached(t, dir, "run", "start", "freio", "--plain", "--yes", "--skip-doctor")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the run: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	url := startUI(t, dir)
	c := startChrome(t)
	c.navigate(t, url)
	c.waitFor(t, 20*time.Second, "the app to boot", "typeof el === 'function'")
	c.waitFor(t, 30*time.Second, "the gate to reach the inbox",
		`document.body.innerText.includes('Aplicar a migration em STG?')`)

	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Review').click()`, nil)
	c.waitFor(t, 15*time.Second, "the reason box", `document.querySelector('input[placeholder="why (optional)"]')`)

	const reason = "a migration derruba a coluna sem backfill"
	c.eval(t, `document.querySelector('input[placeholder="why (optional)"]').value = '`+reason+`'`, nil)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Reject').click()`, nil)

	// The irreversible step never ran, and the sentence typed in the browser is
	// on the step's own screen — the one a different person reads later.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := corvexCLI(t, dir, "run", "show", "freio", "--step", "S01")
		if strings.Contains(out, reason) {
			if _, err := os.Stat(filepath.Join(dir, "executou.txt")); err == nil {
				t.Error("the refused step ran anyway")
			}
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	out, _ := corvexCLI(t, dir, "run", "show", "freio", "--step", "S01")
	t.Fatalf("the reason typed in the browser is not on the step screen:\n%s", out)
}
