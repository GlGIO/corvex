package server_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/server"
)

// The CSP is the fourth layer of the model auth_test.go covers: the first three
// decide whether a request gets in, this one decides what the page it got back
// may load, run and talk to.
//
// It is asserted by exact VALUE, never by "there is a header". The defect this
// package's own tests caught in F7 was an assertion that only looked like it
// checked something, and a header of `default-src *` would pass every weaker
// form of this test while protecting nothing.
const wantCSP = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"connect-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

// authed drives the server the way the browser will, with the token in a header
// so the request needs no cookie round trip.
func authed(t *testing.T, h http.Handler, srv *server.Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
	req.Header.Set("Authorization", "Bearer "+srv.Token())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The page, the two assets and the API all come out of one wrapper, so the
// interesting failure this catches is not "the header is wrong" but "a route was
// added and the header only reaches some of them".
func TestCSP_IsTheExactPolicyOnEveryResponse(t *testing.T) {
	srv, h := newTestServer(t)
	for _, path := range []string{"/", "/assets/app.css", "/assets/app.js", "/api/state", "/api/gates"} {
		rec := authed(t, h, srv, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("%s:\n got  %q\n want %q", path, got, wantCSP)
		}
	}
}

// A refusal is still a response a browser renders, and it is the response an
// attack produces — so it is the one that must not be the hole.
func TestCSP_IsOnTheRefusalsToo(t *testing.T) {
	srv, h := newTestServer(t)

	noToken := httptest.NewRequest(http.MethodGet, "http://localhost/api/state", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, noToken)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("401 carries CSP %q, want %q", got, wantCSP)
	}

	foreignHost := httptest.NewRequest(http.MethodGet, "http://evil.example.com/?token="+srv.Token(), nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, foreignHost)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("403 carries CSP %q, want %q", got, wantCSP)
	}
}

// The other half of the bargain, and the half a header test cannot see: a CSP
// that blocks something the page needs leaves a blank screen, which reads as the
// tool being broken rather than as a policy being wrong. This suite has no
// browser, so it asserts on the shipped bytes what the browser would enforce —
// every construct `default-src 'none'` forbids, and the positive control that
// the constructs the page replaced them with are really there.
func TestCSP_TheShippedPageNeedsNothingThePolicyForbids(t *testing.T) {
	srv, h := newTestServer(t)
	page := authed(t, h, srv, "/").Body.String()
	script := authed(t, h, srv, "/assets/app.js").Body.String()
	style := authed(t, h, srv, "/assets/app.css").Body.String()

	// Nothing external: with `default-src 'none'` any absolute URL to another
	// origin is a blocked request, and in this page it would also be a
	// dependency a user cannot audit.
	for name, body := range map[string]string{"index.html": page, "app.js": script, "app.css": style} {
		for _, forbidden := range []string{"http://", "https://", "//cdn", "@font-face"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s loads something external (%q) — default-src 'none' blocks it", name, forbidden)
			}
		}
	}

	// script-src 'self': every <script> is a src, and no event-handler
	// attribute (onclick="…" is inline script under CSP; addEventListener is
	// not).
	for _, tag := range regexp.MustCompile(`(?is)<script\b[^>]*>`).FindAllString(page, -1) {
		if !strings.Contains(tag, `src="/assets/`) {
			t.Errorf("inline script in index.html (%q) — script-src 'self' blocks it", tag)
		}
	}
	if m := regexp.MustCompile(`(?i)\son[a-z]+\s*=`).FindString(page); m != "" {
		t.Errorf("index.html carries an event-handler attribute (%q) — that is inline script", strings.TrimSpace(m))
	}
	for _, forbidden := range []string{"eval(", "new Function(", "document.write(", `setAttribute('style'`, `setAttribute("style"`} {
		if strings.Contains(script, forbidden) {
			t.Errorf("app.js uses %q, which the policy blocks", forbidden)
		}
	}

	// style-src 'self': every stylesheet is a link to our own origin, and the
	// widths app.js computes go through the CSSOM. The second half is the
	// positive control: if `Object.assign(node.style` were dropped for a style
	// attribute, the checks above would still pass while the bars silently
	// stopped rendering under the policy.
	if !strings.Contains(page, `<link rel="stylesheet" href="/assets/app.css">`) {
		t.Error("index.html no longer links its own stylesheet")
	}
	if m := regexp.MustCompile(`(?i)<[a-z][^>]*\sstyle\s*=`).FindString(page); m != "" {
		t.Errorf("index.html carries an inline style attribute (%q) — style-src 'self' blocks it", strings.TrimSpace(m))
	}
	if !strings.Contains(script, "Object.assign(node.style, v)") {
		t.Error("app.js no longer sets styles through the CSSOM: a style attribute would need 'unsafe-inline'")
	}

	// connect-src 'self': the only origin the page talks to is this one. A
	// relative path is same-origin by construction, which is why the check above
	// for absolute URLs is the whole check — this one just proves the page does
	// call the API, so the directive is not open for nothing.
	if !strings.Contains(script, "fetch(path") {
		t.Error("app.js no longer calls the API — connect-src 'self' would be open for nothing")
	}
}
