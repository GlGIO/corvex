// Package server is `corvex ui`: an HTTP surface over the same operations the
// CLI calls, for a single user on their own machine.
//
// # The rule that shapes everything here
//
// Parity (roadmap 2h): every button has a CLI equivalent and no path exists only
// in the UI. That is not a slogan, it is why F0 dragged the rules out of `cmd/`
// into `internal/ops` — so this package can call exactly what the CLI calls
// rather than reimplement it. A handler here that contains business logic is a
// bug: it means the CLI and the UI can drift, and the first symptom of that
// drift is a gate approved by one and not by the other.
//
// So every handler is the same three lines: decode the request, call ops, encode
// the result. The interesting code in this package is not the handlers — it is
// the auth (auth.go), the detached spawn (spawn.go) and the action log
// (actions.go), which are the three things the CLI never needed.
//
// # No daemon
//
// The server supervises runs, it does not own them. A run started here is a
// detached process with its own identity on disk (F1), which is what makes
// "close the UI, the run keeps going" true and what lets the CLI and the UI see
// the same run. See spawn.go.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/giovannialves/corvex/internal/ops"
)

// Options configures one server. The zero value is not usable: the workspace and
// the binary path have no sane defaults from in here.
type Options struct {
	// WorkDir is the repository the UI opens in. Cross-repository listings come
	// from the global index, so this is the local repo, not a boundary.
	WorkDir string
	// Addr is the listen address; empty means 127.0.0.1 on a free port.
	Addr string
	// Binary is the corvex executable used to spawn detached runs. Empty means
	// os.Executable().
	Binary string
	// Now is injectable for tests.
	Now func() time.Time
	// StreamInterval is how often the event stream re-reads the state, and
	// StreamHeartbeat how long it may stay silent before saying so anyway. Zero
	// means the defaults in stream.go. They are options rather than constants
	// only so a test does not have to wait a real second for a real change.
	StreamInterval  time.Duration
	StreamHeartbeat time.Duration
}

// Server is the assembled UI server.
type Server struct {
	opts     Options
	auth     *Auth
	mux      *http.ServeMux
	http     *http.Server
	listener net.Listener
	actions  *ActionLog
}

// New assembles the server and its routes. It does not listen yet.
func New(opts Options) (*Server, error) {
	if opts.WorkDir == "" {
		return nil, fmt.Errorf("server: WorkDir is required")
	}
	auth, err := NewAuth()
	if err != nil {
		return nil, err
	}
	s := &Server{
		opts:    opts,
		auth:    auth,
		mux:     http.NewServeMux(),
		actions: NewActionLog(opts.WorkDir, opts.Now),
	}
	s.routes()
	s.http = &http.Server{
		Handler: s.Handler(),
		// A UI that hangs on a slow read is a UI that stops answering the gate
		// inbox, which is the one screen that has to work.
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s, nil
}

// Token is the secret that has to appear in the URL the user opens.
func (s *Server) Token() string { return s.auth.Token() }

// Listen binds the address without serving, so the caller can print the real URL
// (including the port the OS picked) before any request can arrive.
func (s *Server) Listen() (string, error) {
	addr := s.opts.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("server: listening on %s: %w", addr, err)
	}
	s.listener = ln
	return ln.Addr().String(), nil
}

// URL is what the user opens: the bound address plus the token.
func (s *Server) URL() string {
	if s.listener == nil {
		return ""
	}
	return fmt.Sprintf("http://%s/?token=%s", s.listener.Addr().String(), s.auth.Token())
}

// Serve blocks until the context is cancelled or the server fails.
func (s *Server) Serve(ctx context.Context) error {
	if s.listener == nil {
		if _, err := s.Listen(); err != nil {
			return err
		}
	}
	errc := make(chan error, 1)
	go func() { errc <- s.http.Serve(s.listener) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	case err := <-errc:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// Handler is the single place the mux gets wrapped, and it is what the listener
// serves — so a test driving it through httptest gets exactly what a browser
// gets. Two call sites for two different compositions is how a layer ends up on
// one of them only.
func (s *Server) Handler() http.Handler { return withCSP(s.auth.Guard(s.mux)) }

func (s *Server) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now().UTC()
}

// lister is the one place the read side is constructed, so every handler reads
// through the same resolver.
func (s *Server) lister() ops.RunLister { return ops.RunLister{Now: s.opts.Now} }

func (s *Server) gates() ops.GateLister { return ops.GateLister{Now: s.opts.Now} }

// writeJSON is the only encoder in the package: one place to keep the shape
// stable and to make sure an error never leaks a stack trace to a browser.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// fail answers with a message the UI can show verbatim. The status is chosen by
// the caller because only it knows whether the user asked for something absent
// (404) or impossible (409).
func fail(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
