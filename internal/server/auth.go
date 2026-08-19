package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Auth is the whole security model of `corvex ui`, and it is deliberately small:
// one token, one machine, one user.
//
// # Why a token at all, on localhost
//
// This server executes commands with the user's credentials — it approves gates
// and starts runs. A browser is a confused deputy: any page the user happens to
// have open can POST to http://localhost:7717 (CSRF), and a hostile DNS name can
// be made to resolve to 127.0.0.1 so that the attacker's origin talks to this
// server as if it were its own (DNS rebinding). Neither needs the attacker to be
// on this machine. So the roadmap fixed auth "desde a v1": retrofitting it later
// means shipping a window in which a web page can approve a production migration.
//
// # The three checks, and what each one stops
//
//  1. The token: a request without it is refused. Stops any origin that never
//     saw the URL corvex printed.
//  2. The Host header must be a loopback name. Stops DNS rebinding, which
//     survives check 1 only if the attacker also stole the token.
//  3. SameSite=Strict on the cookie. Stops the browser from attaching the
//     cookie to a cross-site request at all.
//
// A fourth layer lives in csp.go: those three decide who gets in, the Content
// Security Policy decides what the page they got is allowed to do.
type Auth struct {
	token string
}

// CookieName is the session cookie. It is HttpOnly so a script on a page that
// somehow renders inside this origin cannot read the token back out.
const CookieName = "corvex_ui"

// NewAuth mints a token from crypto/rand. 32 bytes because the token is the only
// secret; there is no rate limit to lean on and no second factor.
func NewAuth() (*Auth, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("minting the ui token: %w", err)
	}
	return &Auth{token: hex.EncodeToString(buf)}, nil
}

// Token is the secret to put in the URL corvex prints.
func (a *Auth) Token() string { return a.token }

// Guard wraps a handler with the three checks. The order matters only for the
// error a caller sees; each one is sufficient on its own.
func (a *Auth) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "corvex ui only answers on localhost", http.StatusForbidden)
			return
		}
		if !a.authorized(r) {
			http.Error(w, "unauthorized: open the URL corvex printed, token included", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authorized accepts the token from the query string (the first load, from the
// URL corvex printed), from the cookie it then sets, or from a bearer header
// (curl, and whatever automation the user writes).
func (a *Auth) authorized(r *http.Request) bool {
	if tok := r.URL.Query().Get("token"); a.matches(tok) {
		return true
	}
	if c, err := r.Cookie(CookieName); err == nil && a.matches(c.Value) {
		return true
	}
	header := r.Header.Get("Authorization")
	return a.matches(strings.TrimPrefix(header, "Bearer "))
}

// matches is constant-time: a timing oracle on a 32-byte token is not a
// realistic attack here, but the cheap version of this comparison is the one
// people copy into places where it is.
func (a *Auth) matches(candidate string) bool {
	if candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(a.token)) == 1
}

// SetCookie hands the browser the token so the rest of the session does not
// carry it in every URL — a token in the address bar ends up in screenshots,
// shell history and pasted links.
func (a *Auth) SetCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    a.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// loopbackHost reports whether the Host header names this machine. A hostname
// that is not loopback means the request arrived through a name that resolved
// here — the DNS rebinding shape — and is refused even with a valid token.
func loopbackHost(host string) bool {
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.Trim(name, "[]")
	switch strings.ToLower(name) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	if ip := net.ParseIP(name); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
