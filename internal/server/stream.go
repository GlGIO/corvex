package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/giovannialves/corvex/internal/ops"
)

// Server-sent events: the page stops asking every five seconds, and the server
// tells it when something changed.
//
// # What this is not
//
// It is not push all the way down, and saying so is the point of this comment.
// The server SUPERVISES runs and never owns them (spawn.go: Setsid and Release,
// never Wait), so there is no in-memory event here to forward — the process that
// writes a run's state is a different process, and the only channel between them
// is the one on disk (F1/F2). Nothing in this file changes that.
//
// So the poll did not disappear. It CHANGED SIDES: this handler re-reads the
// same disk the browser was re-reading, at a shorter interval, and writes to the
// browser only when what it read is different. The trade, for one open page:
//
//   - screen latency: was up to 5s, is up to streamInterval (1s).
//   - HTTP requests: was 12 a minute, is one connection that stays open.
//   - reads of this machine's disk: was 12 a minute, is 60. Five times the work
//     for a fifth of the latency — and the work is one index file plus a handful
//     of stats and signal-0 probes, on a tool one person runs on their laptop.
//
// Whether that trade is worth it is a judgement. It is written down as one
// instead of being dressed up as a new architecture.
//
// # Why not watch the filesystem
//
// Because the change that matters most is not a file write. A run dies and
// nothing on disk moves: liveness is resolved by probing the pid, because F1
// established with a SIGKILL that a dead run keeps `running` written down
// forever. An fsnotify watcher would therefore sleep straight through the one
// transition the inbox exists to show. Any honest observer of this state has to
// ASK, and asking on a timer is a poll — so a new dependency would buy the same
// loop with a blind spot in it.

const (
	// defaultStreamInterval is how often the handler re-reads the state. One
	// second is chosen against the five the browser used to poll at: it is the
	// smallest number that is obviously "immediately" to a person watching a
	// step go green, and it keeps the extra disk work bounded at 5x a poll that
	// was already cheap.
	defaultStreamInterval = time.Second
	// defaultStreamHeartbeat is how long the stream may stay silent before it
	// says something anyway. The heartbeat is not decoration: it is what lets
	// the page's fallback poll stand down (app.js), because "the stream is
	// alive" is only ever a claim about the recent past. It is also the only
	// write that happens during a quiet stream, and a write is how a connection
	// whose client vanished without a FIN gets noticed and freed.
	defaultStreamHeartbeat = 10 * time.Second
	// streamRetry is what the browser is told to wait before reconnecting. Two
	// seconds because a reconnect storm against a local server is pointless and
	// a slow reconnect leaves the page on the fallback poll for longer.
	streamRetry = 2 * time.Second
)

// handleEvents streams state changes to one client until that client goes away.
//
// It sits behind the same guard as everything else — the token and the loopback
// Host check in auth.go, applied once around the whole mux (Server.Handler) — for
// a reason worth stating: an event endpoint left open is the cheapest possible
// leak, and it would leak from the one surface whose other routes approve
// production migrations.
//
// The handler does not return until the request context is cancelled, which is
// what net/http does when the client disconnects. That is the entire lifecycle:
// no registry of clients, no broadcast goroutine, nothing to unsubscribe from.
// One request, one goroutine, one ticker, all of them owned by this frame — so a
// disconnect cannot leak a poller into a server that has no daemon to clean up
// after it.
//
// Note for a future test: this loop only ends when the context ends, so it has to
// be driven through a real server or a cancellable request. An httptest recorder
// with a background context would spin forever.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: %d\n\n", streamRetry.Milliseconds())
	if err := rc.Flush(); err != nil {
		// Without flushing, every event would sit in a buffer until the handler
		// returns — and this handler does not return. Better to hang up now than
		// to hold a connection open that will never deliver anything.
		return
	}

	interval := s.streamInterval()
	heartbeatEvery := int(s.streamHeartbeat() / interval)
	if heartbeatEvery < 1 {
		heartbeatEvery = 1
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ctx := r.Context()
	last := ""
	quiet := 0
	for {
		if fp := s.stateFingerprint(); fp != last {
			last = fp
			// The payload is the whole point of the shape: "something changed,
			// re-read /api/state". It carries no gate, no evidence and no
			// command — the client already has an authenticated route for each
			// of those, and an event is the wrong place to duplicate them.
			if err := writeEvent(w, "state", map[string]string{"fingerprint": fp}); err != nil {
				return
			}
			quiet = 0
			if err := rc.Flush(); err != nil {
				return
			}
		} else {
			quiet++
			if quiet >= heartbeatEvery {
				if err := writeEvent(w, "ping", map[string]any{"at": s.now()}); err != nil {
					return
				}
				quiet = 0
				if err := rc.Flush(); err != nil {
					return
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// stateFingerprint digests what the two list screens are drawn from, and nothing
// else. Two reads that produce the same fingerprint produce the same screen, so
// the client hears nothing.
//
// What is deliberately absent is the clock. RunRow.Age and GateView.Waiting are
// recomputed on every read, so a fingerprint that included them would differ
// every second — a stream that fires every tick is the poll it replaced with
// extra steps in front of it. The page renders those two from its own clock
// anyway.
//
// What is deliberately present is Liveness, which is not on disk: it is the pid
// probe. That is the change no file-watcher would see (see the note at the top of
// this file), and it is the difference between an inbox that says a run is alive
// and one that admits it died.
//
// The escalation half hashes size and modification time rather than the file's
// text. A hash of the body would work as a change detector too, but this digest
// is content nobody needs to hash: the fingerprint is a "re-read X" signal, and
// keeping bodies out of it means the signal cannot be used to confirm a guess
// about what an escalation says.
//
// A read that fails is itself a change worth waking the page for: the page
// re-reads /api/state, gets the same failure, and SHOWS it. A stream that
// swallowed the error would leave a screen that is merely stale, which is the
// failure mode that reads as the tool being fine.
func (s *Server) stateFingerprint() string {
	h := sha256.New()

	inbox, err := s.gates().LoadInbox(s.opts.WorkDir)
	if err != nil {
		fmt.Fprintf(h, "inbox-error\x00%s\n", err)
	}
	for _, g := range inbox.Gates {
		// A gate's identity and text are fixed once it is open; the one field
		// that moves under it is the reading state, which `corvex gate ack`
		// writes from the other side.
		fmt.Fprintf(h, "gate\x00%s\x00%s\x00%s\x00%d\x00%t\n",
			g.Gate.RunID, g.Gate.StepID, g.Liveness, len(g.Gate.Reads), g.Gate.Decided())
	}
	for _, e := range inbox.Escalations {
		fmt.Fprintf(h, "escalation\x00%s\x00%s\x00%s", e.Repo, e.Project, e.Step)
		if st, serr := os.Stat(e.Path); serr == nil {
			fmt.Fprintf(h, "\x00%d\x00%d", st.Size(), st.ModTime().UnixNano())
		}
		fmt.Fprint(h, "\n")
	}

	runs, err := s.lister().ListRuns(ops.RunListOptions{Since: stateWindow})
	if err != nil {
		fmt.Fprintf(h, "runs-error\x00%s\n", err)
	}
	for _, r := range runs {
		fmt.Fprintf(h, "run\x00%s\x00%s\x00%s\x00%d\n",
			r.RunID, r.Status, r.Liveness, r.UpdatedAt.UnixNano())
	}

	// A dispatch that died is on NEITHER of the two lists above — that is the
	// whole defect it belongs to: no run row, no gate, nothing to change. Left
	// out of the fingerprint it would be a card the screen only draws the next
	// time something ELSE moved, which for a person watching a run they just
	// started is the same as not drawing it.
	dispatches, derr := s.actions.Dispatches(20)
	if derr != nil {
		fmt.Fprintf(h, "dispatch-error\x00%s\n", derr)
	}
	for _, d := range dispatches {
		fmt.Fprintf(h, "dispatch\x00%s\x00%s\x00%d\n", d.Log, d.Result, d.At.UnixNano())
	}

	return hex.EncodeToString(h.Sum(nil))
}

// writeEvent frames one SSE message. The payload goes through encoding/json, so
// it can never contain a bare newline and therefore never needs more than one
// `data:` line — which is also what keeps a value from ever being read as a
// second field of the protocol.
func writeEvent(w io.Writer, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err
}

func (s *Server) streamInterval() time.Duration {
	if s.opts.StreamInterval > 0 {
		return s.opts.StreamInterval
	}
	return defaultStreamInterval
}

func (s *Server) streamHeartbeat() time.Duration {
	if s.opts.StreamHeartbeat > 0 {
		return s.opts.StreamHeartbeat
	}
	return defaultStreamHeartbeat
}
