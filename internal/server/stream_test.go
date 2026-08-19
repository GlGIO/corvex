package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/server"
)

// The event stream (stream.go) is the F7 debt paid at its real size: the poll did
// not disappear, it moved to the server. So the tests here are about the two
// claims that make that worth doing and the three that make it safe.
//
// Worth doing: an event arrives when the observed state REALLY changed, and no
// event arrives when it did not. The second one is the whole thing — a stream
// that fires on its own tick is the poll it replaced with more moving parts.
//
// Safe: it is behind the same guard as every other route, the payload is a nudge
// and not content, and a client that walks away takes its poller with it.
//
// None of it is driven by a sleep standing in for an event. The change these
// tests make is a file appearing on disk, which is not a stand-in for the
// product's cross-process channel — it IS the channel (F1/F2).

// newStreamServer builds a server whose stream ticks fast enough for a test, on a
// real socket, with its own corvex home.
//
// The home matters: the fingerprint reads the global index, and a test that read
// the developer's real one would see their runs change liveness underneath it and
// call that a pass.
func newStreamServer(t *testing.T, workDir string, interval, heartbeat time.Duration) (*server.Server, *httptest.Server) {
	t.Helper()
	t.Setenv(run.HomeEnv, t.TempDir())
	srv, err := server.New(server.Options{
		WorkDir:         workDir,
		StreamInterval:  interval,
		StreamHeartbeat: heartbeat,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	// A real listener, not a recorder: handleEvents returns only when the request
	// context is cancelled, and a recorder's context never is.
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// writeEscalation puts a human escalation where internal/step puts it. An
// escalation is the cheapest REAL state change available to a test: it needs no
// child process and no index, and the inbox screen lists it.
func writeEscalation(t *testing.T, workDir, project, step, body string) {
	t.Helper()
	dir := filepath.Join(workDir, ".corvex", "escalations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, project+"-"+step+".md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

type sseFrame struct {
	Event string
	Data  string
}

// sseStream is one open stream, read in the background so a test can wait for a
// frame with a deadline instead of blocking forever on a stream that went quiet.
type sseStream struct {
	frames chan sseFrame
	cancel context.CancelFunc
	body   io.ReadCloser

	mu  sync.Mutex
	raw strings.Builder
}

func openStream(t *testing.T, client *http.Client, origin, token string) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/events", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("opening the stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("stream Content-Type = %q, want text/event-stream", got)
	}
	// The fourth layer reaches here too. The CSP test cannot cover this route —
	// it drives an httptest recorder, and this handler would never return — so
	// this is the only place that sees the header on a streaming response.
	if got := resp.Header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("stream CSP:\n got  %q\n want %q", got, wantCSP)
	}

	s := &sseStream{frames: make(chan sseFrame, 128), cancel: cancel, body: resp.Body}
	go s.read()
	t.Cleanup(s.close)
	return s
}

func (s *sseStream) read() {
	defer close(s.frames)
	br := bufio.NewReader(s.body)
	event, data := "", ""
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		s.mu.Lock()
		s.raw.WriteString(line)
		s.mu.Unlock()

		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			// End of a frame. The `retry:` preamble is a frame with no event and
			// no data; it is not something a test waits for.
			if event != "" || data != "" {
				s.frames <- sseFrame{Event: event, Data: data}
			}
			event, data = "", ""
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

// next waits for one frame.
func (s *sseStream) next(t *testing.T, within time.Duration) sseFrame {
	t.Helper()
	select {
	case f, ok := <-s.frames:
		if !ok {
			t.Fatal("the stream closed before it said anything")
		}
		return f
	case <-time.After(within):
		t.Fatalf("no event within %s", within)
		return sseFrame{}
	}
}

// quiet asserts the stream says nothing for a while.
func (s *sseStream) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case f, ok := <-s.frames:
		if ok {
			t.Fatalf("the stream sent %s/%s with nothing changed — it is firing on its own tick, which is the poll it replaced", f.Event, f.Data)
		}
	case <-time.After(d):
	}
}

// all is every byte the stream has sent so far, protocol included.
func (s *sseStream) all() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raw.String()
}

func (s *sseStream) close() {
	s.cancel()
	_ = s.body.Close()
}

func fingerprint(t *testing.T, f sseFrame) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(f.Data), &payload); err != nil {
		t.Fatalf("event data is not JSON (%q): %v", f.Data, err)
	}
	fp, _ := payload["fingerprint"].(string)
	if fp == "" {
		t.Fatalf("event carries no fingerprint: %q", f.Data)
	}
	return fp
}

// The claim the debt is paid against, in both directions: silence while nothing
// moves, an event when something does.
func TestStream_SpeaksWhenTheStateChangesAndNotOtherwise(t *testing.T) {
	workDir := t.TempDir()
	// The heartbeat is pushed out of reach so that anything this test hears is a
	// state change and not a keepalive.
	srv, ts := newStreamServer(t, workDir, 20*time.Millisecond, time.Hour)
	st := openStream(t, ts.Client(), ts.URL, srv.Token())

	// On connect the stream states where it is. That is the client's baseline —
	// without it a page that opened the stream before anything happened would
	// have nothing to compare a later fingerprint against.
	baseline := st.next(t, 5*time.Second)
	if baseline.Event != "state" {
		t.Fatalf("first frame is %q, want a state event", baseline.Event)
	}
	before := fingerprint(t, baseline)

	// Negative control, and the one that matters: dozens of ticks pass, the
	// handler re-reads the disk on every one of them, and the client hears
	// nothing because nothing is different.
	st.quiet(t, 500*time.Millisecond)

	writeEscalation(t, workDir, "demo", "S02", "# escalated\nthe step could not decide\n")

	changed := st.next(t, 5*time.Second)
	if changed.Event != "state" {
		t.Fatalf("after a real change the stream sent %q, want a state event", changed.Event)
	}
	if after := fingerprint(t, changed); after == before {
		t.Errorf("the stream fired with the same fingerprint %q: it woke the page for nothing", after)
	}
}

// A quiet stream still has to say something, and not only to be polite: the ping
// is what lets the page's fallback poll stand down (app.js), and it is the write
// that discovers a client which vanished without closing.
func TestStream_PingsWhileNothingHappens(t *testing.T) {
	srv, ts := newStreamServer(t, t.TempDir(), 10*time.Millisecond, 50*time.Millisecond)
	st := openStream(t, ts.Client(), ts.URL, srv.Token())

	if f := st.next(t, 5*time.Second); f.Event != "state" {
		t.Fatalf("first frame is %q, want a state event", f.Event)
	}
	if f := st.next(t, 5*time.Second); f.Event != "ping" {
		t.Fatalf("a quiet stream sent %q, want a ping — without it the page cannot tell a live stream from a dead one", f.Event)
	}
}

// The stream is behind the same three checks as every other route. An event
// endpoint left open is the cheapest leak there is, and this one would leak from
// the surface whose other routes approve production migrations.
func TestStream_RefusesTheUnauthorized(t *testing.T) {
	srv, ts := newStreamServer(t, t.TempDir(), 20*time.Millisecond, time.Hour)
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatalf("GET without a token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("stream without a token = %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/events?token="+srv.Token(), nil)
	req.Host = "evil.example.com"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("rebinding request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("stream through a foreign Host = %d, want 403", resp.StatusCode)
	}
}

// The payload is a nudge, never content. The positive control is what makes this
// test mean anything: the stream really did react to the file, so the absence of
// the file's text below is a property of the payload and not of a stream that
// said nothing.
func TestStream_CarriesNoContent(t *testing.T) {
	const secret = "PASTE-THIS-INTO-PRODUCTION-Sensitive-Body-1234"
	workDir := t.TempDir()
	srv, ts := newStreamServer(t, workDir, 20*time.Millisecond, time.Hour)
	st := openStream(t, ts.Client(), ts.URL, srv.Token())
	st.next(t, 5*time.Second) // baseline

	writeEscalation(t, workDir, "demo", "S02", "# escalated\n"+secret+"\n")
	changed := st.next(t, 5*time.Second) // positive control: it noticed
	if changed.Event != "state" {
		t.Fatalf("after the change the stream sent %q", changed.Event)
	}

	if strings.Contains(st.all(), secret) {
		t.Errorf("the stream carried the body of an escalation:\n%s", st.all())
	}
	// And the shape is closed rather than merely clean today: a state event has
	// one field, a ping has one field, and anything else is a payload that grew.
	var payload map[string]any
	if err := json.Unmarshal([]byte(changed.Data), &payload); err != nil {
		t.Fatalf("event data is not JSON: %v", err)
	}
	for key := range payload {
		if key != "fingerprint" {
			t.Errorf("a state event carries %q: the payload is supposed to say only that something changed", key)
		}
	}
}

// A leaked goroutine per request is how a server with no daemon dies slowly: the
// handler polls the disk forever for a browser tab that closed an hour ago.
// Nothing in a response can be asserted about that, so this counts goroutines —
// with a positive control first, because a count that cannot see twenty open
// streams cannot see twenty leaked ones either.
func TestStream_ADisconnectedClientTakesItsPollerWithIt(t *testing.T) {
	srv, ts := newStreamServer(t, t.TempDir(), 20*time.Millisecond, time.Hour)
	client := ts.Client()

	// One stream up and down first, so the transport and the test server have
	// already grown whatever goroutines they keep.
	warm := openStream(t, client, ts.URL, srv.Token())
	warm.next(t, 5*time.Second)
	warm.close()
	client.CloseIdleConnections()
	baseline := stableGoroutines(3 * time.Second)

	const clients = 20
	streams := make([]*sseStream, 0, clients)
	for i := 0; i < clients; i++ {
		st := openStream(t, client, ts.URL, srv.Token())
		st.next(t, 5*time.Second) // it is inside the loop now, not still connecting
		streams = append(streams, st)
	}
	if open := runtime.NumGoroutine(); open < baseline+clients {
		t.Fatalf("%d goroutines with %d streams open, baseline %d: this count is too blunt to prove anything", open, clients, baseline)
	}

	for _, st := range streams {
		st.close()
	}
	// Ten of slack for the transport's own bookkeeping. Twenty leaked handlers
	// would be twice that and then some.
	client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := runtime.NumGoroutine()
		if got <= baseline+10 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines, baseline %d — a client that went away left its poller running", got, baseline)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// stableGoroutines waits for the count to stop moving and returns it. A single
// reading taken right after a connection closed measures the teardown, not the
// floor, and a floor measured too high is a leak this test would then miss.
func stableGoroutines(within time.Duration) int {
	deadline := time.Now().Add(within)
	last := -1
	for time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		now := runtime.NumGoroutine()
		if now == last {
			return now
		}
		last = now
	}
	return runtime.NumGoroutine()
}
