package e2e

// The run screen is the WATCH screen, and it has to move on its own.
//
// The page goes deaf while a detail is open, and that rule is load-bearing on
// the gate screen: 2b is what someone reads BEFORE approving, it carries a
// reading lock, and swapping the evidence mid-read is how a person approves
// something they did not read.
//
// It was applied to every detail, and the run screen is not that kind of
// screen. MEASURED on the first real incident: the owner approved the gate, the
// run walked on through three more steps over the following minutes, and their
// page went on showing `3/9 · S04 RUNNING` for thirty-seven hours while the CLI
// said 5/9. Their reading of it was "parece que parou de novo" — the wrong
// conclusion, and the one the screen supported.

import (
	"path/filepath"
	"testing"
	"time"
)

// watchRecipeYAML has a step slow enough that the screen can be opened while it
// is still running, and a second one after it so there is something to see
// arrive.
const watchRecipeYAML = `name: anda
description: |
  Dois passos, o primeiro devagar, para assistir de fora.
stages:
  - id: S01
    kind: tool
    title: "O passo devagar"
    command: "sleep 6"
  - id: S02
    kind: tool
    title: "O passo que chega depois"
    depends_on: [S01]
    command: "echo chegou"
`

func TestUI_TheRunScreenMovesWithoutBeingTouched(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".corvex", "config.yaml"), identityConfigYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "anda.yaml"), watchRecipeYAML)
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"config", "user.email", "e2e@corvex"}, {"config", "user.name", "e2e"},
		{"add", "-A"}, {"commit", "-m", "recipe"},
	} {
		if out, err := gitIn(dir, args...); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}

	cmd, logOf := corvexRunDetached(t, dir, "run", "start", "anda", "--plain", "--yes", "--skip-doctor")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the run: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	c := openRunsTab(t, startUI(t, dir))
	c.waitFor(t, 30*time.Second, "the live run on the history", `document.body.innerText.includes('anda')`)

	// Open the run and then DO NOT TOUCH ANYTHING. Every assertion below is
	// about what the page does by itself.
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Open').click()`, nil)
	c.waitFor(t, 15*time.Second, "the run screen", `document.querySelector('.steps')`)

	// The slow step is still going when the screen opens; that is the fixture
	// working. If it had already finished there would be nothing to watch, and
	// the test would pass without proving anything.
	if !c.evalBool(t, `document.body.innerText.includes('RUNNING')`) {
		t.Fatalf("the run had already finished when the screen opened — nothing to watch\n%s", logOf())
	}

	// And now, with no click, no keystroke and no navigation, the screen has to
	// catch up on its own.
	c.waitFor(t, 40*time.Second, "the second step to arrive on the open screen",
		`[...document.querySelectorAll('.step')].some(s => s.innerText.includes('S02') && s.innerText.includes('PASSED'))`)
}

// A screen that redraws on a timer replaces the button under the cursor.
//
// Making the run screen live had a cost that does not show in the diff: every
// tick replaced every node, so a button somebody was reaching for could be
// swapped a millisecond before the click landed. It showed up first as a flake
// in the retry test — the click found the button, the page refreshed, and the
// click landed on a node that was no longer in the document. A person does the
// same thing more slowly and has no way to tell it happened.
//
// So the screen redraws only when the run moved. This test pins that: a
// FINISHED run cannot move, and its nodes must therefore survive.
func TestUI_AStillRunScreenDoesNotReplaceItsOwnNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".corvex", "config.yaml"), identityConfigYAML)
	writeFile(t, filepath.Join(dir, ".corvex", "recipes", "anda.yaml"), watchRecipeYAML)
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"config", "user.email", "e2e@corvex"}, {"config", "user.name", "e2e"},
		{"add", "-A"}, {"commit", "-m", "recipe"},
	} {
		if out, err := gitIn(dir, args...); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	// Run it to completion FIRST: a finished run is the thing that cannot move.
	corvexCLI(t, dir, "run", "start", "anda", "--plain", "--yes", "--skip-doctor")

	c := openRunsTab(t, startUI(t, dir))
	c.waitFor(t, 30*time.Second, "the finished run", `document.body.innerText.includes('anda')`)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Open').click()`, nil)
	c.waitFor(t, 15*time.Second, "the run screen", `document.querySelector('.steps')`)

	// Hold on to the node the way a click does: find it now, use it later.
	c.eval(t, `window.__held = document.querySelector('.steps')`, nil)

	// Something else on the machine has to MOVE, or the page never redraws at
	// all and this test measures nothing. The first version of it just waited,
	// and passed against the broken code for exactly that reason: with a
	// finished run and an idle page, the fingerprint never changed and render
	// was never called.
	//
	// A second run is the move. It changes the set the stream fingerprints, so
	// the page refreshes — with a run screen open whose own run did not change.
	second, _ := corvexRunDetached(t, dir, "run", "start", "anda", "--plain", "--yes", "--skip-doctor")
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Process.Kill() })

	// The page noticed: the history behind this screen now knows about it.
	c.waitFor(t, 30*time.Second, "the page to refresh for the second run",
		`fetch('/api/state').then(r => r.json()).then(d => (d.runs || []).length >= 2)`)
	time.Sleep(2 * time.Second) // and a tick or two beyond the first notice

	if !c.evalBool(t, `document.contains(window.__held)`) {
		t.Error("the node was replaced while nothing about THIS run changed — a click aimed at it would have missed")
	}
	// And the screen is still the right screen, not an empty one.
	if !c.evalBool(t, `document.querySelectorAll('.step').length >= 2`) {
		t.Error("the run screen lost its steps")
	}
}
