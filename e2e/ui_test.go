package e2e

// `corvex ui` against the real binary, in its own process.
//
// The server exists to be talked to by a browser, so the only test that means
// anything is one that talks to it over TCP: an httptest handler proves the
// routes, not that the process binds, prints a URL somebody can open, and
// refuses what it should refuse on a real socket.

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

var uiURL = regexp.MustCompile(`http://127\.0\.0\.1:\d+/\?token=[0-9a-f]+`)

// wantCSP is the header a browser must actually receive from the real binary.
// The exact same string is pinned in internal/server/csp_test.go, and the
// duplication is the point: a policy that got weakened for one caller's
// convenience then fails in two places instead of being quietly renegotiated in
// the package that ships it.
const wantCSP = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"connect-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

func startUI(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command(binaryPath, "ui", "--addr", "127.0.0.1:0")
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "NO_COLOR=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting corvex ui: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// The printed URL is the contract with the user: it has to carry the port
	// the OS actually picked and the token, or nobody can open it.
	scanner := bufio.NewScanner(stdout)
	deadline := time.Now().Add(10 * time.Second)
	for scanner.Scan() && time.Now().Before(deadline) {
		if url := uiURL.FindString(scanner.Text()); url != "" {
			go io.Copy(io.Discard, stdout)
			return url
		}
	}
	t.Fatal("corvex ui never printed a URL")
	return ""
}

func TestUI_ServesTheAppAndRefusesTheUnauthorized(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	dir := setupIdentityRepo(t)
	url := startUI(t, dir)
	base := strings.SplitN(url, "/?token=", 2)
	origin, token := base[0], base[1]

	client := &http.Client{Timeout: 5 * time.Second}

	// 1. The page loads with the token from the URL, and hands back the cookie
	//    that keeps the token out of the address bar afterwards.
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>corvex</title>") {
		t.Fatalf("index: status %d, body starts %q", resp.StatusCode, first(string(body), 80))
	}
	if len(resp.Cookies()) == 0 {
		t.Error("no session cookie was set on the first load")
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("index CSP:\n got  %q\n want %q", got, wantCSP)
	}

	// 2. The API answers with the same shapes the CLI prints.
	resp, err = client.Get(origin + "/api/state?token=" + token)
	if err != nil {
		t.Fatalf("GET /api/state: %v", err)
	}
	var state struct {
		Repo string `json:"repo"`
		Runs []struct {
			RunID string `json:"run_id"`
		} `json:"runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decoding state: %v", err)
	}
	resp.Body.Close()
	if state.Repo == "" {
		t.Error("state does not say which repository it opened")
	}

	// 3. Without the token, nothing. This is the whole security model: the
	//    server approves gates and starts runs.
	resp, err = client.Get(origin + "/api/state")
	if err != nil {
		t.Fatalf("GET without token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request got %d, want 401", resp.StatusCode)
	}

	// 4. A request that arrived through a foreign name is refused even with the
	//    token — the DNS rebinding shape.
	req, _ := http.NewRequest(http.MethodGet, origin+"/api/state?token="+token, nil)
	req.Host = "evil.example.com"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("rebinding request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Host got %d, want 403", resp.StatusCode)
	}

	// 5. The page under the policy, driven the way a browser drives it: the first
	//    load takes the token, everything after it carries nothing but the
	//    cookie. Under `default-src 'none'` these three requests are the ENTIRE
	//    network surface of the UI — the stylesheet, the script and the API — so
	//    if one of them fails the screen is blank, and a CSP that blanks the
	//    screen is worse than no CSP: the failure looks like the tool being
	//    broken, not like a header being wrong.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{Timeout: 5 * time.Second, Jar: jar}
	first, err := browser.Get(url)
	if err != nil {
		t.Fatalf("browser first load: %v", err)
	}
	_, _ = io.Copy(io.Discard, first.Body)
	first.Body.Close()

	for _, path := range []string{"/assets/app.css", "/assets/app.js", "/api/state"} {
		resp, err := browser.Get(origin + path)
		if err != nil {
			t.Fatalf("GET %s with only the session cookie: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(body) == 0 {
			t.Errorf("%s: status %d, %d bytes — the page cannot render", path, resp.StatusCode, len(body))
		}
		if got := resp.Header.Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("%s CSP:\n got  %q\n want %q", path, got, wantCSP)
		}
	}

	// 6. The event stream, on the real socket. The unit suite proves that it
	//    emits on a change and frees itself on a disconnect; what only the real
	//    binary can show is that a streaming response reaches a client BEFORE the
	//    handler returns — and this handler never returns. A forgotten flush
	//    passes every httptest assertion and then hands a browser a connection
	//    that says nothing for as long as the process lives.
	resp, err = browser.Get(origin + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events with the session cookie: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("stream status %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("stream Content-Type = %q, want text/event-stream", got)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("stream CSP:\n got  %q\n want %q", got, wantCSP)
	}
	frames := bufio.NewReader(resp.Body)
	firstEvent := ""
	// A handful of lines is the whole preamble: `retry:`, a blank, then the
	// event. Bounded so a stream that says nothing fails here rather than
	// hanging on the client's timeout.
	for i := 0; i < 8 && firstEvent == ""; i++ {
		line, rerr := frames.ReadString('\n')
		if rerr != nil {
			break
		}
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "event: "); ok {
			firstEvent = name
		}
	}
	resp.Body.Close()
	if firstEvent != "state" {
		t.Errorf("the stream's first event was %q, want \"state\" — the page opens with nothing to react to", firstEvent)
	}

	// 7. And it is behind the same auth as the rest. An event endpoint anyone can
	//    open is the cheapest leak there is, on the surface whose other routes
	//    approve production migrations.
	resp, err = client.Get(origin + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events without a token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("stream without a token got %d, want 401", resp.StatusCode)
	}
}

func first(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
