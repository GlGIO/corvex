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
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

var uiURL = regexp.MustCompile(`http://127\.0\.0\.1:\d+/\?token=[0-9a-f]+`)

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
}

func first(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
