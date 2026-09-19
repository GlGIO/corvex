package e2e

// The sequence a unit test cannot see: open a detail screen, let the state move
// underneath it, come back.
//
// This is the regression the event stream introduced. Before it, a five-second
// poll ran unconditionally and healed any staleness within five seconds. After
// it the stream is the only observer, the page deliberately ignores events while
// a detail screen is open (so the evidence does not move under the reader's
// eyes), and the fallback poll stands down because the stream is alive and
// proving it. Each of those three decisions is right on its own; together they
// meant that coming back from a gate screen re-rendered a CACHED inbox — showing
// an already-approved gate as still waiting, with nothing scheduled to ever
// correct it.
//
// The word that makes this a product defect rather than a slow refresh is
// "nothing scheduled". So the test is built around proving exactly that: a
// SECOND event stream, opened by the test itself, witnesses that the server said
// nothing at all during the window in which the screen corrected itself. Without
// that witness this test passes on the broken code — the run resumes after the
// approval, keeps changing state, and one of those later events heals the page
// by accident. That accident is not the fix, and an early draft of this file
// mistook it for one.
//
// Nothing here is asserted about the SOURCE of app.js. The page is loaded by a
// browser from the real binary, the state is changed by a third process the way
// the product changes it (`corvex gate approve`, a file appearing on disk), and
// the assertions read the DOM that resulted.

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── an independent witness ──────────────────────────────────────────────────

// serverStream is a second /api/events connection, held by the test. It exists
// to answer one question the browser cannot be asked about itself: did the
// SERVER say anything just now? A screen that became correct while this observer
// heard silence became correct on its own.
type serverStream struct {
	mu     sync.Mutex
	states int
	last   time.Time
	body   io.ReadCloser
}

func watchEvents(t *testing.T, client *http.Client, origin, token string) *serverStream {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, origin+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// No timeout on this client: the response never ends.
	resp, err := (&http.Client{Jar: client.Jar}).Do(req)
	if err != nil {
		t.Fatalf("opening the witness stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("witness stream status %d, want 200", resp.StatusCode)
	}
	w := &serverStream{body: resp.Body, last: time.Now()}
	t.Cleanup(func() { _ = resp.Body.Close() })
	go func() {
		br := bufio.NewReader(resp.Body)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimSpace(line) == "event: state" {
				w.mu.Lock()
				w.states++
				w.last = time.Now()
				w.mu.Unlock()
			}
		}
	}()
	return w
}

func (w *serverStream) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.states
}

// waitQuiet blocks until the server has not announced a change for `quiet`. It
// is the precondition for every assertion below: while the state is still
// moving, an event arriving a moment after the click would heal the page by
// itself and the test would be measuring the stream, not the fix.
func (w *serverStream) waitQuiet(t *testing.T, within, quiet time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		since := time.Since(w.last)
		w.mu.Unlock()
		if since >= quiet {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the server never went quiet for %s: this test cannot tell a re-read from an event", quiet)
}

// ── reading the same server over the plain API ──────────────────────────────

// inboxOnTheWire is what the server would answer right now. It is the control
// for every DOM assertion below: a screen that disagrees with this is stale, and
// a screen that agrees with a server that never changed proves nothing.
func inboxOnTheWire(t *testing.T, client *http.Client, origin, token string) (gates, escalations int) {
	t.Helper()
	resp, err := client.Get(origin + "/api/state?token=" + token)
	if err != nil {
		t.Fatalf("GET /api/state: %v", err)
	}
	defer resp.Body.Close()
	var state struct {
		Inbox struct {
			Gates       []json.RawMessage `json:"gates"`
			Escalations []json.RawMessage `json:"escalations"`
		} `json:"inbox"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decoding /api/state: %v", err)
	}
	return len(state.Inbox.Gates), len(state.Inbox.Escalations)
}

func waitOnTheWire(t *testing.T, client *http.Client, origin, token string, within time.Duration, wantGates, wantEscalations int) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		g, e := inboxOnTheWire(t, client, origin, token)
		if g == wantGates && e == wantEscalations {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server still reports %d gate(s)/%d escalation(s), want %d/%d — the change never landed, so nothing below would mean anything",
				g, e, wantGates, wantEscalations)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ── driving the page ────────────────────────────────────────────────────────

// Every wait below is anchored on the DOM, never on `state`. That is not style:
// leaving a detail clears state.detail and THEN awaits a re-read, so there is a
// window in which the page reports no detail while the detail is still on the
// screen. A wait that trusted the variable would pass straight over the stale
// screen this test exists to catch.

// click presses the first button whose visible text is exactly label.
func click(t *testing.T, c *chrome, label string) {
	t.Helper()
	quoted, _ := json.Marshal(label)
	expr := "(() => { const b = [...document.querySelectorAll('button')].find(x => x.textContent.trim() === " +
		string(quoted) + "); if (!b) return false; b.click(); return true; })()"
	if !c.evalBool(t, expr) {
		t.Fatalf("no button labelled %q on the screen:\n%s", label, c.evalString(t, "document.body.innerText"))
	}
}

// clickTab presses one of the three tabs — by its data-view and not by its text,
// because the inbox tab carries a badge inside it and its textContent therefore
// moves with the count it is showing.
func clickTab(t *testing.T, c *chrome, view string) {
	t.Helper()
	quoted, _ := json.Marshal(`.tab[data-view="` + view + `"]`)
	expr := "(() => { const b = document.querySelector(" + string(quoted) + "); if (!b) return false; b.click(); return true; })()"
	if !c.evalBool(t, expr) {
		t.Fatalf("no %q tab on the screen", view)
	}
}

// screen is the rendered text, lower-cased: two headings are uppercased by CSS,
// so innerText delivers them in a case the source never contains, and a check
// written against the source spelling would pass no matter what is up.
func screen(t *testing.T, c *chrome) string {
	t.Helper()
	return strings.ToLower(c.evalString(t, "document.body.innerText"))
}

// TestUI_ComingBackFromADetailScreenIsNeverStale walks both ways out of a detail
// screen — the back button and the tab bar — across a real state change made by
// another process while the screen was open.
func TestUI_ComingBackFromADetailScreenIsNeverStale(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes and a browser")
	}
	dir := setupGateRepo(t)
	stub := stubClaudeBin(t, identityStubPass)

	runCmd, readLog := corvexRun(t, dir, stub)
	if err := runCmd.Start(); err != nil {
		t.Fatalf("starting the run: %v", err)
	}
	t.Cleanup(func() { _ = runCmd.Process.Kill() })
	pending := waitForOpenGate(t, dir)

	url := startUI(t, dir)
	parts := strings.SplitN(url, "/?token=", 2)
	origin, token := parts[0], parts[1]
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second, Jar: jar}
	witness := watchEvents(t, client, origin, token)

	c := startChrome(t)
	c.navigate(t, url)

	// ── 1. the gate, left through the back button ───────────────────────────
	c.waitFor(t, 20*time.Second, "the inbox showing the waiting gate",
		"document.querySelectorAll('.card.waiting').length === 1")
	c.waitFor(t, 5*time.Second, "the page opening its event stream", "streamAlive()")

	click(t, c, "Review")
	c.waitFor(t, 10*time.Second, "the gate screen with its evidence", "document.querySelector('.evidence')")

	// A third process decides the gate — the product's actual channel, not a
	// stand-in for it. The parked run then resumes and finishes on its own, and
	// waiting for it to exit is what makes the state stop moving.
	out, err := corvexGate(t, dir, "approve", pending.RunID, "--step", "S01", "--ack", "Migration")
	if err != nil {
		t.Fatalf("approving from a second process failed: %v\n%s\n%s", err, out, readLog())
	}
	done := make(chan error, 1)
	go func() { done <- runCmd.Wait() }()
	select {
	case waitErr := <-done:
		if waitErr != nil {
			t.Fatalf("the released run exited %v:\n%s", waitErr, readLog())
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the run never noticed the approval:\n%s", readLog())
	}
	waitOnTheWire(t, client, origin, token, 30*time.Second, 0, 0)
	witness.waitQuiet(t, 30*time.Second, 2*time.Second)

	// Four things have to be true at this instant or the assertion after the
	// click means nothing —
	//   the browser is still on the detail screen,
	//   the stream is alive, so the five-second fallback poll is standing down
	//     and cannot be what heals anything,
	//   the cached payload the old code re-rendered still holds the gate,
	//   and the server has nothing left to say, so no event is coming to do the
	//     page's work for it.
	if !c.evalBool(t, "!!document.querySelector('.evidence')") {
		t.Fatal("the gate screen went away on its own")
	}
	if !c.evalBool(t, "streamAlive()") {
		t.Fatal("the stream is not alive, so the 5s fallback poll is running — this test cannot tell a fix from a poll")
	}
	if cached := c.evalInt(t, "(state.data.inbox.gates || []).length"); cached != 1 {
		t.Fatalf("the cached inbox holds %d gate(s), want the stale 1 — without a stale cache there is nothing for the back button to get wrong", cached)
	}
	heardBefore := witness.count()

	click(t, c, "← back")
	c.waitFor(t, 3*time.Second, "the inbox coming back correct",
		"!document.querySelector('.evidence') && document.querySelectorAll('.card.waiting').length === 0 "+
			"&& document.body.innerText.includes('Nothing is waiting on you')")
	if heard := witness.count(); heard != heardBefore {
		t.Fatalf("the server announced %d change(s) during the click: the screen may have been healed by an event rather than by coming back", heard-heardBefore)
	}
	if body := screen(t, c); strings.Contains(body, "gate(s) waiting") {
		t.Errorf("the inbox still offers the approved gate for review:\n%s", body)
	}

	// ── 2. a run detail, left through the tab bar ───────────────────────────
	// The other exit, and a change of a different kind: an escalation appearing
	// on disk, which is how a step that could not decide reaches this screen.
	clickTab(t, c, "runs")
	// startsWith, not equality: the heading carries its own action button now
	// ("Dispatch a run"), so its textContent is the title plus the button's
	// label. The assertion is about which screen is up, not about what else the
	// heading holds.
	c.waitFor(t, 10*time.Second, "the run history",
		"[...document.querySelectorAll('h2')].some(h => h.textContent.startsWith('history'))")
	click(t, c, "Open")
	c.waitFor(t, 10*time.Second, "the run screen", "document.querySelector('.steps')")

	writeFile(t, filepath.Join(dir, ".corvex", "escalations", "demo-S02.md"),
		"# escalated\nthe step could not decide\n")
	waitOnTheWire(t, client, origin, token, 30*time.Second, 0, 1)
	witness.waitQuiet(t, 30*time.Second, 2*time.Second)

	if !c.evalBool(t, "!!document.querySelector('.steps')") {
		t.Fatal("the run screen went away on its own")
	}
	if cached := c.evalInt(t, "(state.data.inbox.escalations || []).length"); cached != 0 {
		t.Fatalf("the cached inbox already holds %d escalation(s) — the page re-read under a detail screen, which is the other bug", cached)
	}
	heardBefore = witness.count()

	clickTab(t, c, "inbox")
	c.waitFor(t, 3*time.Second, "the inbox showing the new escalation",
		"!document.querySelector('.steps') && document.querySelectorAll('.card.waiting').length === 1")
	if heard := witness.count(); heard != heardBefore {
		t.Fatalf("the server announced %d change(s) during the tab click", heard-heardBefore)
	}
	if body := screen(t, c); !strings.Contains(body, "escalation(s)") || !strings.Contains(body, "step s02") {
		t.Errorf("the tab bar did not bring back an inbox holding the new escalation:\n%s", body)
	}
}
