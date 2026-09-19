package e2e

// A dispatch that dies is VISIBLE, in a browser, on the screen a person watches.
//
// The defect this pins was the worst kind the UI had: pressing Run answered
// `ok`, the run died a second later on a guard, and the history stayed empty —
// so the screen showed a success and an empty list for the same event, and the
// reason existed only in a file on disk that nothing mentioned. Everything about
// that is invisible to a unit test of the handler: the handler DID answer 202,
// correctly. The claim being tested is about the page.

import (
	"strings"
	"testing"
	"time"
)

func TestUI_ADispatchThatDiesSaysSoOnTheScreen(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real process and a browser")
	}
	dir := setupIdentityRepo(t)
	url := startUI(t, dir)
	c := startChrome(t)
	c.navigate(t, url)
	c.waitFor(t, 20*time.Second, "the app to boot", "typeof el === 'function'")

	// Dispatch something that cannot run. `nope` is neither a recipe nor a
	// project, so the child exits almost immediately — which is exactly the
	// class of failure that used to vanish: too early to ever register a run.
	//
	// The POST goes through the page's own session, not a second client: the
	// thing under test is what THIS page does with the answer.
	c.eval(t, `fetch('/api/runs', {
		method: 'POST',
		headers: {'Content-Type': 'application/json'},
		body: JSON.stringify({target: 'nope'})
	}).then(r => r.json()).then(d => { window.__dispatch = d; })`, nil)
	c.waitFor(t, 10*time.Second, "the dispatch to be accepted", "window.__dispatch && window.__dispatch.log_name")

	// No click, no reload: the stream has to carry it. A dead dispatch changes
	// neither the run list nor the inbox on disk, so it is in the fingerprint
	// precisely because nothing else about it moves.
	c.waitFor(t, 20*time.Second, "the screen to admit the run died",
		`document.body.innerText.toLowerCase().includes('died')`)

	text := c.evalString(t, "document.body.innerText")
	if !strings.Contains(text, "exit ") {
		t.Errorf("the card does not carry the exit status:\n%s", text)
	}
	if !strings.Contains(strings.ToLower(text), "nope") {
		t.Errorf("the card does not name what was dispatched:\n%s", text)
	}

	// And the log is one click away, in the page, rather than a path the reader
	// is told to go and open in a terminal.
	c.eval(t, `[...document.querySelectorAll('button')].find(b => b.textContent === 'Log').click()`, nil)
	c.waitFor(t, 10*time.Second, "the log to open", "document.querySelector('pre.log')")
	logText := c.evalString(t, "document.querySelector('pre.log').innerText")
	if strings.TrimSpace(logText) == "" {
		t.Error("the log screen opened empty: the reader is back where they started")
	}
}

// The tab says what is waiting, from any screen.
//
// The runner's promise is that a run goes on without you and STOPS when it needs
// you. A gate that opens while the browser sits behind an editor then waits for
// a human eye to wander back, which is the same as nobody having been told. The
// title is the channel that needs no permission and cannot be denied, so it is
// the one pinned here — and it is pinned from the RUNS tab, because the first
// version of this only updated the title while the inbox happened to be open,
// which is the one moment the badge already says it.
func TestUI_TheTabTitleCarriesWhatIsWaiting(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupIdentityRepo(t)
	url := startUI(t, dir)
	c := startChrome(t)
	c.navigate(t, url)
	c.waitFor(t, 20*time.Second, "the app to boot", "typeof el === 'function'")

	// Nothing is waiting yet.
	c.waitFor(t, 10*time.Second, "a quiet title", `document.title === 'corvex'`)

	// Move to a screen that is NOT the inbox, then make something wait: a
	// dispatch that dies is work blocked on a person exactly like a gate.
	c.eval(t, `[...document.querySelectorAll('.tab')].find(t => t.dataset.view === 'runs').click()`, nil)
	c.eval(t, `fetch('/api/runs', {
		method: 'POST',
		headers: {'Content-Type': 'application/json'},
		body: JSON.stringify({target: 'nope'})
	})`, nil)

	c.waitFor(t, 20*time.Second, "the title to carry the count",
		`document.title.startsWith('(') && document.title.includes('corvex')`)
	if title := c.evalString(t, "document.title"); !strings.Contains(title, "(1)") {
		t.Errorf("title = %q, want it to count the one thing waiting", title)
	}
}
