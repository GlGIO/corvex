package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/server"
)

// Auth is the only part of this package that is not a thin call into ops, so it
// is the part that gets tested hardest. Each case below is one of the three
// attacks the design names: a request that never saw the token, a page reaching
// localhost through a hostile name, and the cookie exchange that keeps the token
// out of the address bar afterwards.

func newTestServer(t *testing.T) (*server.Server, http.Handler) {
	t.Helper()
	srv, err := server.New(server.Options{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return srv, srv.Handler()
}

func TestAuth_RefusesARequestWithoutTheToken(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://localhost/api/state", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — this server approves gates and starts runs", rec.Code)
	}
}

func TestAuth_RefusesAWrongToken(t *testing.T) {
	_, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://localhost/api/state?token=not-it", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuth_AcceptsTheTokenInTheURLAndHandsBackACookie(t *testing.T) {
	srv, h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://localhost/?token="+srv.Token(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != server.CookieName {
		t.Fatalf("no session cookie was set: %+v", cookies)
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("the session cookie is readable by script")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Error("the session cookie is not SameSite=Strict — a cross-site POST would carry it")
	}
}

func TestAuth_AcceptsCookieAndBearer(t *testing.T) {
	srv, h := newTestServer(t)

	withCookie := httptest.NewRequest(http.MethodGet, "http://localhost/api/state", nil)
	withCookie.AddCookie(&http.Cookie{Name: server.CookieName, Value: srv.Token()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withCookie)
	if rec.Code != http.StatusOK {
		t.Errorf("cookie auth: status = %d, want 200", rec.Code)
	}

	withBearer := httptest.NewRequest(http.MethodGet, "http://localhost/api/state", nil)
	withBearer.Header.Set("Authorization", "Bearer "+srv.Token())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, withBearer)
	if rec.Code != http.StatusOK {
		t.Errorf("bearer auth: status = %d, want 200", rec.Code)
	}
}

// DNS rebinding: the attacker's name resolves to 127.0.0.1, so the request
// really does arrive here — the Host header is what gives it away.
func TestAuth_RefusesANonLoopbackHostEvenWithTheToken(t *testing.T) {
	srv, h := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "http://evil.example.com/api/state?token="+srv.Token(), nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: a valid token must not rescue a foreign Host", rec.Code)
	}
}

func TestAuth_LoopbackSpellings(t *testing.T) {
	srv, h := newTestServer(t)
	for _, host := range []string{"localhost:7717", "127.0.0.1:7717", "[::1]:7717", "127.0.0.5:9000"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/state?token="+srv.Token(), nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("host %s: status = %d, want 200", host, rec.Code)
		}
	}
}

// The URL corvex prints has to be the URL that works: address the OS actually
// bound, and the token.
func TestURL_CarriesTheBoundPortAndTheToken(t *testing.T) {
	srv, err := server.New(server.Options{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	url := srv.URL()
	if !strings.Contains(url, addr) || !strings.Contains(url, srv.Token()) {
		t.Fatalf("URL %q is missing the address %q or the token", url, addr)
	}
	if strings.HasSuffix(addr, ":0") {
		t.Fatal("the printed URL still has port 0: it would not open")
	}
}
