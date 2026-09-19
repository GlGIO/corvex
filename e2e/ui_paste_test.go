package e2e

// One pasted line, three fields.
//
// The line a person has in hand when an incident starts is the board's own —
// `Incident 73607: Pedido duplicado na aba Financeiro (Visão 360°)` — and the
// form was asking them to retype three pieces of it: the id twice, and a slug
// of the title into the field that decides where the pull request goes.
//
// That last one is the reason this exists rather than being a convenience.
// Typing a branch name from memory at the start of an incident is how
// `hotfix/Incident-73607` gets created: valid git, and off the convention that
// `ship-target.sh` reads to route the PR to main.

import (
	"path/filepath"
	"testing"
	"time"
)

const pasteRecipeYAML = `name: incident
description: |
  Diagnostica um incidente.
requires:
  - env: INCIDENT_ID
    why: "qual incidente este run diagnostica"
stages:
  - id: S01
    kind: tool
    title: "Contexto"
    command: "echo ${INCIDENT_ID} > ctx.txt"
`

func setupPasteRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".corvex", "config.yaml"), identityConfigYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "incident.yaml"), pasteRecipeYAML)
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

// openMaker gets to the form with the worktree fields and the recipe chosen.
func openMaker(t *testing.T, url string) *chrome {
	t.Helper()
	c := openRunsTab(t, url)
	c.waitFor(t, 15*time.Second, "the history screen",
		`[...document.querySelectorAll('button')].some(b => b.textContent === 'Dispatch a run')`)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Dispatch a run').click()`, nil)
	c.waitFor(t, 15*time.Second, "the form", `document.querySelector('input[list="recipe-list"]')`)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === '+ worktree').click()`, nil)
	c.eval(t, `(() => { const t = document.querySelector('input[list="recipe-list"]'); t.value = 'incident'; t.dispatchEvent(new Event('input')); })()`, nil)
	c.waitFor(t, 15*time.Second, "the recipe's own field", `document.querySelector('input[placeholder="INCIDENT_ID"]')`)
	return c
}

func paste(t *testing.T, c *chrome, line string) {
	t.Helper()
	c.eval(t, `(() => {
		const n = document.querySelector('input[placeholder^="paste"]');
		n.value = `+jsString(line)+`;
		n.dispatchEvent(new Event('change'));
	})()`, nil)
}

func TestUI_PastedBoardLineFillsTheForm(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	c := openMaker(t, startUI(t, setupPasteRepo(t)))
	paste(t, c, "Incident 73607: Pedido duplicado na aba Financeiro (Visão 360°)")

	// The name is the id alone, because it becomes a directory beside the repo.
	if got := c.evalString(t, `document.querySelector('input[placeholder^="paste"]').value`); got != "73607" {
		t.Errorf("name = %q, want the bare id", got)
	}
	// The branch carries the prefix this repository routes on, the id, and a
	// slug of the title with its accents folded and its punctuation gone.
	got := c.evalString(t, `document.querySelector('input[placeholder^="branch"]').value`)
	if want := "hotfix/73607-pedido-duplicado-na-aba-financeiro-visao"; got != want {
		t.Errorf("branch = %q, want %q", got, want)
	}
	// And the recipe's own field, which is the third thing that was being typed
	// by hand.
	if got := c.evalString(t, `document.querySelector('input[placeholder="INCIDENT_ID"]').value`); got != "73607" {
		t.Errorf("INCIDENT_ID = %q, want 73607", got)
	}
}

// A Feature routes somewhere else, and the prefix is the only thing that says
// so: `feature/*` goes to the active release, `hotfix/*` goes to main.
func TestUI_PastedFeatureGetsTheFeaturePrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	c := openMaker(t, startUI(t, setupPasteRepo(t)))
	paste(t, c, "Feature 59440: Distribuição de vendedor principal")
	got := c.evalString(t, `document.querySelector('input[placeholder^="branch"]').value`)
	if want := "feature/59440-distribuicao-de-vendedor-principal"; got != want {
		t.Errorf("branch = %q, want %q", got, want)
	}
}

// What was typed by hand wins. A parse that overwrote a branch somebody had
// corrected would be worse than no parse: the correction is the one piece of
// this the machine cannot check.
func TestUI_PasteNeverOverwritesWhatWasTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	c := openMaker(t, startUI(t, setupPasteRepo(t)))
	c.eval(t, `document.querySelector('input[placeholder^="branch"]').value = 'hotfix/73607-o-nome-que-eu-quero'`, nil)
	c.eval(t, `document.querySelector('input[placeholder="INCIDENT_ID"]').value = '11111'`, nil)
	paste(t, c, "Incident 73607: Pedido duplicado na aba Financeiro")

	if got := c.evalString(t, `document.querySelector('input[placeholder^="branch"]').value`); got != "hotfix/73607-o-nome-que-eu-quero" {
		t.Errorf("the branch typed by hand became %q", got)
	}
	if got := c.evalString(t, `document.querySelector('input[placeholder="INCIDENT_ID"]').value`); got != "11111" {
		t.Errorf("the id typed by hand became %q", got)
	}
}

// A bare id is not a line to parse, and must pass through untouched — it is
// what a person types when the work item has no title worth sluggifying.
func TestUI_ABareIdIsLeftAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	c := openMaker(t, startUI(t, setupPasteRepo(t)))
	paste(t, c, "73607")
	if got := c.evalString(t, `document.querySelector('input[placeholder^="paste"]').value`); got != "73607" {
		t.Errorf("name = %q", got)
	}
	if got := c.evalString(t, `document.querySelector('input[placeholder^="branch"]').value`); got != "" {
		t.Errorf("a bare id invented the branch %q — there is no title to slug", got)
	}
}
