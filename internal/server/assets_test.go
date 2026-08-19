package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The asset route is two files, and the test is that it is exactly two.
//
// What it replaced was an http.FileServer over the root of the embedded
// filesystem, which is a directory server and behaved like one: `/assets/`
// returned the whole of index.html as the directory index, and
// `/assets/index.html` returned a 301. Neither leaked anything — both carry the
// CSP, both sit behind the same guard as every other route — so the reason to
// close it is not a hole. It is that both are the page's bytes reached by a path
// that never runs handleIndex, and handleIndex is where the token in the URL is
// exchanged for the session cookie. A second door to the page that silently
// skips the page's one side effect is surface nobody chose.
func TestAssets_AreTheTwoFilesAndNothingElse(t *testing.T) {
	srv, h := newTestServer(t)

	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		req.Header.Set("Authorization", "Bearer "+srv.Token())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// The positive control first: without it, a route that 404s everything would
	// pass every assertion below while leaving the page blank.
	for path, wantType := range map[string]string{
		"/assets/app.js":  "text/javascript; charset=utf-8",
		"/assets/app.css": "text/css; charset=utf-8",
	} {
		rec := get(path)
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Fatalf("%s: status %d, %d bytes — the page cannot render", path, rec.Code, rec.Body.Len())
		}
		// Under `script-src 'self'` a script served as the wrong type is refused
		// by the browser, which looks exactly like the tool being broken.
		if got := rec.Header().Get("Content-Type"); got != wantType {
			t.Errorf("%s Content-Type = %q, want %q", path, got, wantType)
		}
	}

	// And the doors that used to exist. `/assets/` is the one that mattered: it
	// answered 200 with the entire page.
	for _, path := range []string{"/assets/", "/assets/index.html", "/assets/embed.go", "/assets/app.js/"} {
		rec := get(path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d, want 404 — the asset route is two names, not a directory", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("%s served the page:\n%s", path, rec.Body.String())
		}
	}

	// Traversal never reaches this handler at all: Go's mux cleans the path and
	// redirects, so what arrives here is already `/index.html`. Asserted rather
	// than assumed, because the assumption is the whole reason the handler does
	// not clean the path itself.
	if rec := get("/assets/../index.html"); rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "/index.html" {
		t.Errorf("a traversal answered %d to %q; the handler is relying on the mux to have cleaned it",
			rec.Code, rec.Header().Get("Location"))
	}
}

// The cookie is the reason the two are separate handlers at all. `/` exchanges
// the token in the address bar for a session cookie so the secret stops
// travelling where screenshots and pasted links can carry it; an asset request
// has no token to exchange and must not pretend it did.
func TestAssets_OnlyTheIndexOpensASession(t *testing.T) {
	srv, h := newTestServer(t)

	page := httptest.NewRequest(http.MethodGet, "http://localhost/?token="+srv.Token(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, page)
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("the index set no session cookie: the rest of this test would prove nothing")
	}

	for _, path := range []string{"/assets/app.js", "/assets/app.css", "/assets/"} {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path+"?token="+srv.Token(), nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s set a session cookie; only the index does that", path)
		}
	}
}
