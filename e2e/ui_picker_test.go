package e2e

// The checkout picker and the form around it, measured in a browser.
//
// The form it replaces had two native selects — repository, then checkout —
// and on a machine that had used corvex for a month the first one held 57
// repositories, 54 of them deleted scratch directories. Finding "the worktree
// for the incident I am on" meant scrolling. What is pinned here is the path a
// person takes now: type part of the id, press Enter, and the form has moved
// there — and that the form is still there, as left, after the page refreshes.

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUI_PickerFindsTheCheckoutByTypingAndTheFormSurvivesARefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupWorktreeRepo(t)
	for _, wt := range [][2]string{{"-61000", "feature/61000-outra-coisa"}, {"-73960", "hotfix/73960-carrinho"}} {
		if out, err := gitIn(dir, "worktree", "add", "-q", filepath.Join(filepath.Dir(dir), filepath.Base(dir)+wt[0]), "-b", wt[1], "fluxo"); err != nil {
			t.Fatalf("worktree add: %s: %v", out, err)
		}
	}
	url := startUI(t, dir)
	c := openRunsTab(t, url)
	c.waitFor(t, 15*time.Second, "the history screen",
		`[...document.querySelectorAll('button')].some(b => b.textContent === 'Dispatch a run')`)
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Dispatch a run').click()`, nil)
	c.waitFor(t, 15*time.Second, "the checkout list", `document.querySelector('.picker') && document.querySelector('.picker').dataset.value`)

	c.eval(t, `document.querySelector('.picker-trigger').click()`, nil)
	c.eval(t, `(() => { const i = document.querySelector('.picker-search'); i.value = '739 hot'; i.dispatchEvent(new Event('input')); })()`, nil)
	if n := c.evalInt(t, `document.querySelectorAll('.picker-item').length`); n != 1 {
		t.Fatalf("'739 hot' left %d rows, want exactly the hotfix worktree", n)
	}
	c.eval(t, `document.querySelector('.picker-search').dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', bubbles: true}))`, nil)
	c.waitFor(t, 5*time.Second, "Enter to choose the row", `document.querySelector('.picker').dataset.value.endsWith('-73960')`)

	// The recipes are re-read from the chosen checkout, and its branch already
	// names the work item, so the recipe's id comes filled in.
	c.waitFor(t, 15*time.Second, "the checkout's recipes", `document.querySelector('[data-recipe="incidente"]')`)
	c.eval(t, `document.querySelector('[data-recipe="incidente"]').click()`, nil)
	if got := c.evalString(t, `document.querySelector('input[name="INCIDENT_ID"]').value`); got != "73960" {
		t.Errorf("INCIDENT_ID = %q, want the id the branch carries", got)
	}

	// A refresh is what every stream event triggers. It used to clear the form.
	c.eval(t, `refresh()`, nil)
	c.waitFor(t, 15*time.Second, "the refresh to land", `document.querySelector('#view h2')`)
	if !c.evalBool(t, `!!document.querySelector('.dispatch') && document.querySelector('[data-recipe="incidente"]').classList.contains('on') && document.querySelector('.picker').dataset.value.endsWith('-73960')`) {
		t.Error("the form did not survive a refresh with what was chosen in it")
	}
}
