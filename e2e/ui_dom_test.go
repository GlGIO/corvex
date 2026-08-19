package e2e

// The el() style contract, executed instead of grepped.
//
// internal/server/csp_test.go checks that the string `Object.assign(node.style,
// v)` is still in the shipped file. That check earns its place — it is the
// positive control for `style-src 'self'`, and it costs nothing — but it cannot
// see behaviour, and behaviour is where this contract was thin.
//
// It was reported as a SILENT failure. It is not, quite, and the difference is
// worth writing down rather than fixing past: a string throws immediately, from
// inside Object.assign, with a message about an indexed property setter on a
// CSSStyleDeclaration — a sentence that names neither `style` nor the element
// and aborts the render it appeared in. A primitive that is not a string is the
// silent half: null, undefined and numbers apply nothing and throw nothing.
//
// Both are now refused by one line in el(), and both are pinned here — in a
// browser, against the page the real binary serves, under the real policy.

import (
	"strings"
	"testing"
	"time"
)

func TestUI_StyleTakesAnObjectAndSaysSoWhenItDoesNot(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real process and a browser")
	}
	url := startUI(t, setupIdentityRepo(t))
	c := startChrome(t)
	c.navigate(t, url)
	c.waitFor(t, 20*time.Second, "app.js running under the policy", "typeof el === 'function'")

	// 1. The positive control. The shape the page actually uses applies through
	//    the CSSOM with the CSP in force — without this, everything below would
	//    be satisfied by an el() that refuses its own callers.
	if got := c.evalString(t, "el('span', { style: { width: '12ch' } }).style.width"); got != "12ch" {
		t.Fatalf("el() set width to %q, want \"12ch\" — the cost bars in 2f do not render", got)
	}

	// 2. What the raw call does, recorded as the reason the guard exists rather
	//    than asserted as a requirement: this is Chrome's message, and it is the
	//    one a reader would have had to work back from.
	raw := c.evalString(t, "(() => { const n = document.createElement('span'); "+
		"try { Object.assign(n.style, 'width: 12ch'); } catch (e) { return 'threw ' + e.name; } "+
		"return 'applied ' + JSON.stringify(n.style.cssText); })()")
	t.Logf("Object.assign(style, 'width: 12ch') without the guard: %s", raw)

	// 3. The string form, refused with a sentence that names the contract.
	got := c.evalString(t,
		"(() => { try { el('span', { style: 'width: 12ch' }); return 'nothing was thrown'; } "+
			"catch (e) { return e.name + ': ' + e.message; } })()")
	if !strings.HasPrefix(got, "TypeError") || !strings.Contains(got, "style") || !strings.Contains(got, "span") {
		t.Errorf("el() answered %q to a string style, want a TypeError naming `style` and the tag", got)
	}

	// 4. The half that really was silent. A primitive that is not a string
	//    applies nothing and throws nothing, so nothing anywhere reports it.
	for _, v := range []string{"null", "12", "true", "undefined"} {
		got := c.evalString(t,
			"(() => { try { const n = el('span', { style: "+v+" }); return 'applied ' + JSON.stringify(n.style.cssText); } "+
				"catch (e) { return e.name; } })()")
		if got != "TypeError" {
			t.Errorf("el() answered %q to `style: %s`, want a TypeError", got, v)
		}
	}
}
