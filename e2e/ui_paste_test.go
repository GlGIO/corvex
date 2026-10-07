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
	c.waitFor(t, 15*time.Second, "the form", `document.querySelector('[data-recipe="incident"]')`)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === '+ worktree').click()`, nil)
	c.eval(t, `document.querySelector('[data-recipe="incident"]').click()`, nil)
	c.waitFor(t, 15*time.Second, "the recipe's own field", `document.querySelector('input[name="INCIDENT_ID"]')`)
	return c
}

// paste fires a REAL ClipboardEvent carrying a DataTransfer, on the element
// given — which is what a person's ⌘V produces and what the handler reads.
//
// The first version of this helper set the field's value and fired `change`.
// That is not a paste: it proved the parser and proved nothing about the path
// a person actually takes, and it would have gone on passing after the handler
// stopped reading the clipboard at all.
// It returns whether the handler CLAIMED the paste. A synthetic event runs no
// default action, so "the text ended up in the box" is not observable here —
// what is, and what actually separates the two cases, is whether the page
// cancelled the paste to handle it itself or let the browser insert it.
func paste(t *testing.T, c *chrome, selector, line string) bool {
	t.Helper()
	return c.evalBool(t, `(() => {
		const dt = new DataTransfer();
		dt.setData('text', `+jsString(line)+`);
		const ev = new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true });
		document.querySelector(`+jsString(selector)+`).dispatchEvent(ev);
		return ev.defaultPrevented;
	})()`)
}

func TestUI_PastedBoardLineFillsTheForm(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	c := openMaker(t, startUI(t, setupPasteRepo(t)))
	if !paste(t, c, `input[placeholder^="paste"]`, "Incident 73607: Pedido duplicado na aba Financeiro (Visão 360°)") {
		t.Fatal("the board line was not claimed: the whole line would have been inserted as the name")
	}

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
	if got := c.evalString(t, `document.querySelector('input[name="INCIDENT_ID"]').value`); got != "73607" {
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
	paste(t, c, `input[placeholder^="paste"]`, "Feature 59440: Distribuição de vendedor principal")
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
	c.eval(t, `document.querySelector('input[name="INCIDENT_ID"]').value = '11111'`, nil)
	paste(t, c, `input[placeholder^="paste"]`, "Incident 73607: Pedido duplicado na aba Financeiro")

	if got := c.evalString(t, `document.querySelector('input[placeholder^="branch"]').value`); got != "hotfix/73607-o-nome-que-eu-quero" {
		t.Errorf("the branch typed by hand became %q", got)
	}
	if got := c.evalString(t, `document.querySelector('input[name="INCIDENT_ID"]').value`); got != "11111" {
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
	// The handler does NOT claim it: there is nothing to spread, so the browser
	// inserts the text the ordinary way and the field ends up with the id.
	if paste(t, c, `input[placeholder^="paste"]`, "73607") {
		t.Error("a bare id was swallowed by the parser — nothing would land in the box")
	}
	if got := c.evalString(t, `document.querySelector('input[placeholder^="branch"]').value`); got != "" {
		t.Errorf("a bare id invented the branch %q — there is no title to slug", got)
	}
	if got := c.evalString(t, `document.querySelector('input[name="INCIDENT_ID"]').value`); got != "" {
		t.Errorf("a bare id reached INCIDENT_ID as %q without anyone asking", got)
	}
}

// ⌘V anywhere on the form, with the worktree panel still closed.
//
// This is the shape the ask had: "I should be able to paste this and have it
// fill itself." A handler bolted to one field inside a collapsed panel asks the
// person to find the field first, which is most of the work it was supposed to
// remove. The card listens, and it opens the panel it needs.
func TestUI_PasteLandsAnywhereOnTheFormAndOpensThePanel(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	url := startUI(t, setupPasteRepo(t))
	c := openRunsTab(t, url)
	c.waitFor(t, 15*time.Second, "the history screen",
		`[...document.querySelectorAll('button')].some(b => b.textContent === 'Dispatch a run')`)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Dispatch a run').click()`, nil)
	c.waitFor(t, 15*time.Second, "the form", `document.querySelector('[data-recipe="incident"]')`)
	c.eval(t, `document.querySelector('[data-recipe="incident"]').click()`, nil)
	c.waitFor(t, 15*time.Second, "the recipe's own field", `document.querySelector('input[name="INCIDENT_ID"]')`)

	// The worktree panel was never opened.
	if !c.evalBool(t, `document.querySelector('.maker').classList.contains('hidden')`) {
		t.Fatal("the worktree panel was already open — this test is about it opening itself")
	}
	// ⌘V over the recipe box, which is where the cursor happens to be.
	paste(t, c, `input[name="project"]`, "Incident 73607: Pedido duplicado na aba Financeiro (Visão 360°)")

	if c.evalBool(t, `document.querySelector('.maker').classList.contains('hidden')`) {
		t.Error("the worktree panel stayed closed, so the fields it filled are invisible")
	}
	if got := c.evalString(t, `document.querySelector('input[placeholder^="branch"]').value`); got != "hotfix/73607-pedido-duplicado-na-aba-financeiro-visao" {
		t.Errorf("branch = %q", got)
	}
	if got := c.evalString(t, `document.querySelector('input[name="INCIDENT_ID"]').value`); got != "73607" {
		t.Errorf("INCIDENT_ID = %q", got)
	}
	// And the project box the paste landed on was NOT filled with the board
	// line: the default action is prevented, so the text does not also end up
	// as a project name — which would also have unselected the recipe.
	if got := c.evalString(t, `document.querySelector('input[name="project"]').value`); got != "" {
		t.Errorf("the project box became %q — the pasted line was inserted as well as parsed", got)
	}
	if !c.evalBool(t, `document.querySelector('[data-recipe="incident"]').classList.contains('on')`) {
		t.Error("the recipe chosen before the paste is no longer chosen")
	}
}
