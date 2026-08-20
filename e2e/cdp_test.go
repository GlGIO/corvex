package e2e

// A browser, hand-driven. Nothing in this file is about the browser: it is the
// smallest thing that can run the shipped app.js the way a user runs it.
//
// Why it exists at all: internal/server/csp_test.go asserts on the BYTES of
// app.js, and that is the strongest a test without a JavaScript engine can be.
// It caught a policy violation once and it will again — but it cannot see a
// sequence. The defect this file's test covers (open a detail, the state moves,
// come back) passes every byte-level assertion there is, because every line
// involved is present and spelled correctly. Only running them in order shows it.
//
// Why no dependency: this package is the acceptance suite for a binary whose
// whole pitch is that a user can audit what they are trusting. A CDP client is
// ~200 lines of framing and a JSON envelope; a CDP library is a tree. The tree
// would be test-only, and it would still be in go.sum, which is the file someone
// reads to decide whether the tree is small enough to trust.
//
// It skips when there is no Chrome. That is a real hole and it is stated rather
// than hidden: on a machine with no browser this suite proves less.

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── a WebSocket client, client side only ────────────────────────────────────
// RFC 6455 minus everything CDP over loopback never uses: no extensions, no
// subprotocols, no continuation of our own writes. Frames FROM the server are
// unmasked and may be fragmented, which is the one case this does have to handle.

type wsConn struct {
	c  net.Conn
	br *bufio.Reader
}

func wsDial(rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	c, err := net.DialTimeout("tcp", u.Host, 10*time.Second)
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		c.Close()
		return nil, err
	}
	// No Origin header on purpose: Chrome refuses a DevTools socket that arrives
	// with an origin it was not told to allow, and refuses nothing when there is
	// none — which is what a non-browser client looks like.
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		u.RequestURI(), u.Host, base64.StdEncoding.EncodeToString(nonce[:]))
	if _, err := io.WriteString(c, req); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		c.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		c.Close()
		return nil, fmt.Errorf("websocket upgrade got %s", resp.Status)
	}
	return &wsConn{c: c, br: br}, nil
}

func (w *wsConn) writeFrame(opcode byte, payload []byte) error {
	head := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n < 126:
		head = append(head, 0x80|byte(n))
	case n < 1<<16:
		head = append(head, 0x80|126, byte(n>>8), byte(n))
	default:
		head = append(head, 0x80|127)
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(n))
		head = append(head, size[:]...)
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	head = append(head, mask[:]...)
	body := make([]byte, n)
	for i := range payload {
		body[i] = payload[i] ^ mask[i%4]
	}
	_, err := w.c.Write(append(head, body...))
	return err
}

// readMessage returns one whole message, reassembling fragments and answering
// pings itself so a long test cannot be dropped for going silent.
func (w *wsConn) readMessage() ([]byte, error) {
	var msg []byte
	for {
		var head [2]byte
		if _, err := io.ReadFull(w.br, head[:]); err != nil {
			return nil, err
		}
		fin := head[0]&0x80 != 0
		opcode := head[0] & 0x0f
		masked := head[1]&0x80 != 0
		size := int(head[1] & 0x7f)
		switch size {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(w.br, ext[:]); err != nil {
				return nil, err
			}
			size = int(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(w.br, ext[:]); err != nil {
				return nil, err
			}
			size = int(binary.BigEndian.Uint64(ext[:]))
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.br, mask[:]); err != nil {
				return nil, err
			}
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(w.br, buf); err != nil {
			return nil, err
		}
		if masked {
			for i := range buf {
				buf[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 0x8: // close
			return nil, io.EOF
		case 0x9: // ping
			if err := w.writeFrame(0xA, buf); err != nil {
				return nil, err
			}
			continue
		case 0xA: // pong
			continue
		}
		msg = append(msg, buf...)
		if fin {
			return msg, nil
		}
	}
}

func (w *wsConn) close() { _ = w.c.Close() }

// ── the DevTools protocol, the four calls this test makes ───────────────────

type cdpMessage struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type chrome struct {
	ws        *wsConn
	sessionID string

	mu   sync.Mutex
	next int
	// Replies and events share one socket. The buffer is large because a reader
	// that blocks here would stall the socket; if it ever did fill, a call times
	// out and says so rather than hanging.
	in chan []byte
}

// chromePath finds a browser to drive, or returns "" so the caller can skip.
func chromePath() string {
	if p := os.Getenv("CORVEX_CHROME"); p != "" {
		return p
	}
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}
	if runtime.GOOS != "darwin" {
		candidates = nil
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			if p, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, p)
			}
		}
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// noBrowser ends the test the way the environment asked for. A missing browser
// is a legitimate skip on a laptop and a LIE in CI: the browser half of the UI
// proof silently evaporates and the run still reports green. So the choice is
// the caller's, declared once: with CORVEX_REQUIRE_BROWSER set, "no browser" is
// a failure, not a skip. Nothing here guesses at CI env vars — a guess would
// either miss the CI that matters or turn every laptop run red.
func noBrowser(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CORVEX_REQUIRE_BROWSER") != "" {
		t.Fatalf("CORVEX_REQUIRE_BROWSER is set and "+format, args...)
	}
	t.Skipf(format, args...)
}

// startChrome launches a headless browser with its own profile and returns a
// session attached to one blank tab.
func startChrome(t *testing.T) *chrome {
	t.Helper()
	bin := chromePath()
	if bin == "" {
		noBrowser(t, "no Chrome or Chromium on this machine: the browser half of the UI is unproven here (set CORVEX_CHROME to a binary)")
	}
	profile := t.TempDir()
	cmd := exec.Command(bin,
		"--headless=new",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-background-networking",
		"--user-data-dir="+profile,
		"--remote-debugging-port=0",
		"about:blank",
	)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		noBrowser(t, "could not start %s: %v", bin, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Chrome writes the port it actually took into the profile.
	portFile := filepath.Join(profile, "DevToolsActivePort")
	var port string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(portFile); err == nil {
			if lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2); lines[0] != "" {
				port = lines[0]
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if port == "" {
		t.Fatal("chrome never wrote DevToolsActivePort")
	}

	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	resp, err := http.Get("http://127.0.0.1:" + port + "/json/version")
	if err != nil {
		t.Fatalf("asking chrome for its debugger URL: %v", err)
	}
	err = json.NewDecoder(resp.Body).Decode(&version)
	resp.Body.Close()
	if err != nil || version.WebSocketDebuggerURL == "" {
		t.Fatalf("chrome gave no debugger URL: %v", err)
	}

	ws, err := wsDial(version.WebSocketDebuggerURL)
	if err != nil {
		t.Fatalf("connecting to chrome: %v", err)
	}
	c := &chrome{ws: ws, in: make(chan []byte, 1024)}
	t.Cleanup(ws.close)
	go func() {
		for {
			msg, err := ws.readMessage()
			if err != nil {
				close(c.in)
				return
			}
			c.in <- msg
		}
	}()

	var created struct {
		TargetID string `json:"targetId"`
	}
	c.call(t, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &created)
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	c.call(t, "", "Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true}, &attached)
	c.sessionID = attached.SessionID
	return c
}

// call sends one command and waits for the reply with its id, dropping the
// events that arrive in between.
func (c *chrome) call(t *testing.T, session, method string, params map[string]any, out any) {
	t.Helper()
	c.mu.Lock()
	c.next++
	id := c.next
	c.mu.Unlock()

	envelope := map[string]any{"id": id, "method": method}
	if params != nil {
		envelope["params"] = params
	}
	if session != "" {
		envelope["sessionId"] = session
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ws.writeFrame(0x1, body); err != nil {
		t.Fatalf("%s: writing to chrome: %v", method, err)
	}

	deadline := time.After(30 * time.Second)
	for {
		select {
		case raw, ok := <-c.in:
			if !ok {
				t.Fatalf("%s: chrome hung up", method)
			}
			var msg cdpMessage
			if err := json.Unmarshal(raw, &msg); err != nil || msg.ID != id {
				continue
			}
			if msg.Error != nil {
				t.Fatalf("%s: %s", method, msg.Error.Message)
			}
			if out != nil {
				if err := json.Unmarshal(msg.Result, out); err != nil {
					t.Fatalf("%s: decoding %s: %v", method, msg.Result, err)
				}
			}
			return
		case <-deadline:
			t.Fatalf("%s: chrome never replied", method)
		}
	}
}

func (c *chrome) navigate(t *testing.T, target string) {
	t.Helper()
	c.call(t, c.sessionID, "Page.navigate", map[string]any{"url": target}, nil)
}

// eval runs an expression in the page and decodes the value it produced. It is
// the whole reason for this file: the expression runs against the real DOM the
// real app.js built.
func (c *chrome) eval(t *testing.T, expression string, out any) {
	t.Helper()
	var result struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails,omitempty"`
	}
	c.call(t, c.sessionID, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	}, &result)
	if result.Exception != nil {
		t.Fatalf("evaluating %s: %s", expression, result.Exception.Text)
	}
	if out != nil && len(result.Result.Value) > 0 {
		if err := json.Unmarshal(result.Result.Value, out); err != nil {
			t.Fatalf("decoding the value of %s: %v", expression, err)
		}
	}
}

func (c *chrome) evalBool(t *testing.T, expression string) bool {
	t.Helper()
	var v bool
	c.eval(t, expression, &v)
	return v
}

func (c *chrome) evalInt(t *testing.T, expression string) int {
	t.Helper()
	var v int
	c.eval(t, expression, &v)
	return v
}

func (c *chrome) evalString(t *testing.T, expression string) string {
	t.Helper()
	var v string
	c.eval(t, expression, &v)
	return v
}

// waitFor polls an expression until it is true. Deliberately a poll and not a
// sleep: the bound is what the assertion means, and a sleep long enough to be
// safe is also long enough to hide the thing being measured.
func (c *chrome) waitFor(t *testing.T, within time.Duration, why, expression string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if c.evalBool(t, "!!("+expression+")") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s did not happen within %s (%s)\nthe screen said: %s",
		why, within, expression, c.evalString(t, "document.body.innerText"))
}
