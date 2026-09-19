package e2e

// The whole flow from the screen: cut a worktree, then run in it.
//
// This is the sentence the tool is built around — "one corvex in the main repo,
// one worktree per piece of work, all of them coordinated from one page" — and
// until now its first step happened somewhere else. The person opened a
// terminal, ran `git worktree add -b hotfix/73960 ../repo-73960 main`, and only
// then did the UI have a checkout to dispatch into. Everything the screen did
// well after that was downstream of a step it could not do.
//
// The recipe is pure shell and writes what it was told, so what is measured is
// the path from two form fields to a process running on a branch.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const inputRecipeYAML = `name: incidente
description: |
  Um passo que só sabe o que fazer se lhe disserem qual é o incidente.
requires:
  - env: INCIDENT_ID
    why: "o id do work item no board"
stages:
  - id: S01
    kind: tool
    title: "Registra o incidente"
    command: |
      set -euo pipefail
      printf 'incidente=%s\n' "${INCIDENT_ID:-<vazio>}" > registro.txt
      git rev-parse --abbrev-ref HEAD >> registro.txt
`

func setupWorktreeRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".corvex", "config.yaml"), identityConfigYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "incidente.yaml"), inputRecipeYAML)
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"config", "user.email", "e2e@corvex"}, {"config", "user.name", "e2e"},
		{"add", "-A"}, {"commit", "-m", "recipe"},
	} {
		if out, err := gitIn(dir, args...); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	return dir
}

func TestUI_CutsAWorktreeAndRunsInIt(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupWorktreeRepo(t)
	url := startUI(t, dir)
	// The history tab, where the dispatch button lives.
	c := openRunsTab(t, url)

	// The dispatch form, the way a person opens it.
	c.waitFor(t, 15*time.Second, "the history screen",
		`[...document.querySelectorAll('button')].some(b => b.textContent === 'Dispatch a run')`)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Dispatch a run').click()`, nil)
	c.waitFor(t, 15*time.Second, "the checkout list to fill",
		`document.querySelectorAll('select')[1] && document.querySelectorAll('select')[1].options.length >= 1`)

	// Cut the branch. The name is the incident, the branch is the repository's
	// own convention — the two are different on purpose, because a form that
	// derived one from the other would be choosing where the PR lands.
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === '+ worktree').click()`, nil)
	c.eval(t, `document.querySelector('input[placeholder^="name"]').value = '73960'`, nil)
	c.eval(t, `document.querySelector('input[placeholder^="branch"]').value = 'hotfix/73960-carrinho'`, nil)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Create').click()`, nil)

	worktree := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-73960")
	waitOnDisk(t, worktree, "the worktree the browser asked for")
	if head := headOf(t, worktree); head != "hotfix/73960-carrinho" {
		t.Fatalf("the checkout is on %q, want the branch typed in the browser", head)
	}

	// The new checkout is selected, and the form asks for what the recipe
	// declared. Neither is a given: the list is re-read from the server after
	// the create, and the fields are built from the recipe catalogue.
	c.waitFor(t, 15*time.Second, "the new checkout to be selected",
		`document.querySelectorAll('select')[1].value.endsWith('-73960')`)
	c.eval(t, `(() => { const t = document.querySelector('input[list="recipe-list"]'); t.value = 'incidente'; t.dispatchEvent(new Event('input')); })()`, nil)
	c.waitFor(t, 15*time.Second, "the recipe's own field",
		`document.querySelector('input[placeholder="INCIDENT_ID"]')`)
	if !c.evalBool(t, `document.body.innerText.includes('o id do work item no board')`) {
		t.Error("the field does not carry the recipe author's reason for asking")
	}

	c.eval(t, `document.querySelector('input[placeholder="INCIDENT_ID"]').value = '73960'`, nil)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Run').click()`, nil)

	// And the run happened IN the worktree, on that branch, knowing the id.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(worktree, "registro.txt"))
		if err == nil {
			got := strings.TrimSpace(string(data))
			want := "incidente=73960\nhotfix/73960-carrinho"
			if got != want {
				t.Fatalf("the step wrote:\n%s\nwant:\n%s", got, want)
			}
			// Nothing was written in the main checkout: the isolation is the
			// reason the worktree exists at all.
			if _, err := os.Stat(filepath.Join(dir, "registro.txt")); err == nil {
				t.Error("the run also wrote into the main checkout")
			}
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("the run never wrote into %s", worktree)
}

func waitOnDisk(t *testing.T, path, what string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (%s)", what, path)
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("reading HEAD of %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}
