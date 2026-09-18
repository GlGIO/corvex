package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/server"
)

// These are the three defects a dispatch from the UI used to have, each pinned
// by the shape that exposed it: it spawned in the wrong checkout, it reported
// `ok` for a run that died a second later, and the reason was only ever in a
// file the UI could not open.

// stubBinary writes a fake corvex that records its argv and its cwd, then exits
// with the given code after printing `body` on stdout.
func stubBinary(t *testing.T, argvFile string, body string, exit int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "corvex-stub")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$PWD\" > " + argvFile + "\n" +
		"printf '%s\\n' \"$@\" >> " + argvFile + "\n" +
		body + "\n" +
		"exit " + itoa(exit) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// waitFor polls until cond says yes or the deadline passes. The child is a real
// detached process, so everything it does is observed, never assumed.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func readActions(t *testing.T, workDir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workDir, ".corvex", "runs", "ui-actions.jsonl"))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var a map[string]any
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			t.Fatalf("action log holds a line that is not JSON: %q", line)
		}
		out = append(out, a)
	}
	return out
}

// A project with a worktree is dispatched INTO the worktree.
//
// The defect: the UI spawned in its own WorkDir, the CLI guard refused to run a
// project from the main checkout, and the child died on that refusal while the
// screen said `ok`. It is the exact shape of "one corvex in the main repo,
// coordinating one worktree per feature", which is how the tool is meant to be
// operated.
func TestStartRun_DispatchesIntoTheProjectWorktree(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "repo")
	worktree := filepath.Join(root, "repo-feat1") // the `corvex start` convention
	for _, dir := range []string{filepath.Join(workDir, ".git"), worktree} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	argv := filepath.Join(root, "argv")
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, argv, "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()

	rec, body := post(t, h, srv, "/api/runs", `{"target":"feat1"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body)
	}
	if dir, _ := body["dir"].(string); dir != worktree {
		t.Errorf("dispatch dir = %q, want the worktree %q", dir, worktree)
	}
	// The recorded line has to be the line that does the same thing when pasted,
	// and a `corvex run start` pasted in the main repo does NOT do the same
	// thing — it is refused. So the cd belongs in the audit.
	command, _ := body["command"].(string)
	if !strings.HasPrefix(command, "cd "+worktree+" && corvex run start feat1") {
		t.Errorf("recorded command = %q, want it to cd into the worktree", command)
	}

	waitFor(t, "the child to report its cwd", func() bool {
		data, err := os.ReadFile(argv)
		return err == nil && strings.Contains(string(data), worktree)
	})
	data, _ := os.ReadFile(argv)
	if cwd := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]; cwd != worktree {
		t.Errorf("the run executed in %q, want %q", cwd, worktree)
	}
}

// `here` is the escape hatch, and it must reach the child as `--here` rather
// than as a silently different working directory: the CLI guard is what decides,
// and it can only decide if it is told.
func TestStartRun_HereRunsInTheRepoAndSaysSo(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "repo")
	worktree := filepath.Join(root, "repo-feat1")
	for _, dir := range []string{filepath.Join(workDir, ".git"), worktree} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	argv := filepath.Join(root, "argv")
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, argv, "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()

	_, body := post(t, h, srv, "/api/runs", `{"target":"feat1","here":true}`)
	if dir, _ := body["dir"].(string); dir != workDir {
		t.Errorf("dispatch dir = %q, want the repo %q", dir, workDir)
	}
	command, _ := body["command"].(string)
	if strings.Contains(command, "cd ") || !strings.Contains(command, "--here") {
		t.Errorf("recorded command = %q, want a plain command carrying --here", command)
	}
	waitFor(t, "the child to run", func() bool {
		data, err := os.ReadFile(argv)
		return err == nil && strings.Contains(string(data), "--here")
	})
}

// A run that dies before it registers itself leaves no run row anywhere. Until
// the exit status was recorded, the ONLY trace of it was the `ok` written at
// spawn time — the UI reported a success for a process that had already failed.
func TestStartRun_RecordsWhyTheRunDied(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argv := filepath.Join(root, "argv")
	stub := stubBinary(t, argv, `echo "Error: run failed: working tree has 1 uncommitted change(s)"`, 1)
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stub})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()

	rec, body := post(t, h, srv, "/api/runs", `{"target":"demo"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body)
	}
	logName, _ := body["log_name"].(string)
	if logName == "" {
		t.Fatal("the dispatch did not name its log")
	}

	var outcome map[string]any
	waitFor(t, "the outcome line", func() bool {
		for _, a := range readActions(t, workDir) {
			if result, _ := a["result"].(string); strings.HasPrefix(result, "exit ") {
				outcome = a
				return true
			}
		}
		return false
	})
	result, _ := outcome["result"].(string)
	if !strings.HasPrefix(result, "exit 1") {
		t.Errorf("outcome = %q, want it to start with the exit status", result)
	}
	if !strings.Contains(result, "uncommitted change") {
		t.Errorf("outcome = %q, want the reason the run printed", result)
	}
	if got, _ := outcome["log"].(string); got != logName {
		t.Errorf("outcome names log %q, want %q — the reader has to be able to open it", got, logName)
	}
}

// The log the audit line points at is fetchable, and only by name inside this
// repo's log directory.
func TestRunLog_ServesTheTailAndRefusesEverythingElse(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "repo")
	logs := filepath.Join(workDir, ".corvex", "runs", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "demo-20260918-221249.log"), []byte("first\nError: boom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.log"), []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Options{WorkDir: workDir})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()

	rec, body := get(t, h, srv, "/api/runs/logs/demo-20260918-221249.log")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if content, _ := body["content"].(string); !strings.Contains(content, "Error: boom") {
		t.Errorf("content = %q, want the log", body["content"])
	}

	for _, name := range []string{"..%2f..%2fsecret.log", "nope.log", "demo-20260918-221249.txt"} {
		rec, _ := get(t, h, srv, "/api/runs/logs/"+name)
		if rec.Code == http.StatusOK {
			t.Errorf("GET /api/runs/logs/%s = 200, want a refusal", name)
		}
	}
}

// The orchestrator question: a UI opened in one repository dispatches into
// another, but only into one the machine has already seen.
//
// The refusal is the load-bearing half. This surface exists to spawn a binary;
// letting the body name any path on disk would make it spawn that binary
// anywhere, and "the token stops a stranger" is an argument about attackers, not
// about a mistake in a form.
func TestStartRun_RefusesARepositoryTheMachineDoesNotKnow(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "repo")
	other := filepath.Join(root, "somewhere-else")
	for _, dir := range []string{workDir, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	argv := filepath.Join(root, "argv")
	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stubBinary(t, argv, "true", 0)})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()

	rec, body := post(t, h, srv, "/api/runs", `{"target":"demo","repo":"`+other+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "run corvex there once") {
		t.Errorf("the refusal does not say how to make it work: %v", body["error"])
	}
	if _, err := os.Stat(argv); err == nil {
		t.Error("a refused dispatch still spawned the binary")
	}

	// The repository the UI opened is always in the list, by either spelling.
	rec, _ = post(t, h, srv, "/api/runs", `{"target":"demo","repo":"`+workDir+`"}`)
	if rec.Code != http.StatusAccepted {
		t.Errorf("dispatching into the UI's own repository = %d, want 202", rec.Code)
	}
}

// GET /api/repos answers with the list the dispatch form offers, and the local
// repository is in it and marked.
func TestRepos_ListsTheLocalRepositoryAsCurrent(t *testing.T) {
	srv, h := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/repos", nil)
	req.Header.Set("Authorization", "Bearer "+srv.Token())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var repos []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &repos); err != nil {
		t.Fatalf("decoding: %v (%s)", err, rec.Body)
	}
	var current int
	for _, r := range repos {
		if cur, _ := r["current"].(bool); cur {
			current++
			if name, _ := r["name"].(string); name == "" {
				t.Error("the current repository has no name to show")
			}
		}
	}
	if current != 1 {
		t.Errorf("%d repositories marked current, want exactly 1: %v", current, repos)
	}
}
