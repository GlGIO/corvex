package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/server"
)

// The API is deliberately thin — decode, call ops, encode — so what these tests
// assert is the part that is NOT in ops: that the shapes reach the wire, that a
// mutation records its CLI equivalent (the parity rule, roadmap 2h), and that a
// dispatched run is really detached.

func get(t *testing.T, h http.Handler, srv *server.Server, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
	req.Header.Set("Authorization", "Bearer "+srv.Token())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func post(t *testing.T, h http.Handler, srv *server.Server, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://localhost"+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+srv.Token())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestState_AnswersWithTheInboxAndTheRuns(t *testing.T) {
	srv, h := newTestServer(t)
	rec, body := get(t, h, srv, "/api/state")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	for _, key := range []string{"inbox", "runs", "repo"} {
		if _, ok := body[key]; !ok {
			t.Errorf("state is missing %q: %v", key, body)
		}
	}
}

func TestRun_UnknownIDIsA404NotA500(t *testing.T) {
	srv, h := newTestServer(t)
	rec, body := get(t, h, srv, "/api/runs/run_beef")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "run_beef") {
		t.Errorf("the error does not name what was missing: %v", body)
	}
}

// The parity rule, made checkable: a decision through the UI records the command
// that would have done the same thing from a terminal — and records it even when
// it FAILED, because "the UI tried to approve a gate whose run had died" is
// exactly what is invisible otherwise.
func TestApprove_RecordsTheCLIEquivalentEvenWhenItFails(t *testing.T) {
	workDir := t.TempDir()
	srv, err := server.New(server.Options{WorkDir: workDir, Now: func() time.Time {
		return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()

	rec, body := post(t, h, srv, "/api/gates/run_8f21/approve", `{"step":"S03","ack":["Migration"]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (no such gate): %s", rec.Code, rec.Body)
	}
	action, _ := body["action"].(map[string]any)
	command, _ := action["command"].(string)
	want := `corvex gate approve run_8f21 --step S03 --ack "Migration"`
	if command != want {
		t.Errorf("recorded command = %q, want %q", command, want)
	}
	if result, _ := action["result"].(string); result == "ok" {
		t.Error("a failed approval was recorded as ok")
	}

	// And it is on disk, in the gitignored scratch directory rather than in the
	// project's history.
	logPath := filepath.Join(workDir, ".corvex", "runs", "ui-actions.jsonl")
	data, rerr := os.ReadFile(logPath)
	if rerr != nil {
		t.Fatalf("reading the action log: %v", rerr)
	}
	if !strings.Contains(string(data), "corvex gate approve run_8f21") {
		t.Errorf("the action log does not hold the command: %s", data)
	}

	recActions, actions := get(t, h, srv, "/api/actions")
	if recActions.Code != http.StatusOK {
		t.Fatalf("GET /api/actions = %d", recActions.Code)
	}
	_ = actions
}

// A dispatched run is spawned detached and the handler returns immediately: the
// server supervises runs, it never owns them. The stub sleeps, so a handler that
// waited would still be blocked when the assertion runs.
func TestStartRun_SpawnsDetachedAndReturnsAtOnce(t *testing.T) {
	workDir := t.TempDir()
	marker := filepath.Join(workDir, "ran")
	stub := filepath.Join(t.TempDir(), "corvex-stub")
	script := "#!/bin/sh\necho \"$@\" > " + marker + "\nsleep 5\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stub})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	h := srv.Handler()

	start := time.Now()
	rec, body := post(t, h, srv, "/api/runs", `{"target":"demo","environment":"stack"}`)
	elapsed := time.Since(start)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the handler waited %s for the run: it must not own it", elapsed)
	}
	if cmd, _ := body["command"].(string); !strings.Contains(cmd, "run start demo --plain --yes --env stack") {
		t.Errorf("the recorded command is not what was spawned: %v", body["command"])
	}

	// Wait for CONTENT, not for the file: `echo > marker` creates it empty and
	// fills it a moment later, so reading on existence alone races the shell and
	// fails under load — the same test-side race the F-1 network already had to
	// remove once, in the stack fixtures.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			if !strings.Contains(string(data), "run start demo") {
				t.Errorf("the spawned process got %q", data)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the spawned run never ran")
}

// Every line of the action log has to be a command somebody could paste into a
// terminal. This is the guard for the defect that made it necessary: the first
// version printed the on-disk verdict (`approved`) where the CLI verb goes
// (`approve`), producing a log that looked like parity and was not.
func TestActionLog_RecordsExecutableVerbs(t *testing.T) {
	srv, h := newTestServer(t)
	for path, verb := range map[string]string{
		"/api/gates/run_8f21/approve": "approve",
		"/api/gates/run_8f21/reject":  "reject",
	} {
		_, body := post(t, h, srv, path, `{"step":"S03"}`)
		action, _ := body["action"].(map[string]any)
		command, _ := action["command"].(string)
		if !strings.HasPrefix(command, "corvex gate "+verb+" ") {
			t.Errorf("%s recorded %q, which is not a runnable command", path, command)
		}
	}
}

func TestStartRun_RefusesAnEmptyTarget(t *testing.T) {
	srv, h := newTestServer(t)
	rec, _ := post(t, h, srv, "/api/runs", `{"target":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// The UI is served from the binary and sits behind the same auth as the API: a
// page that loads without a token and then fails every request is worse than a
// page that does not load, because the failure looks like the tool being broken.
func TestUI_IsEmbeddedAndGuarded(t *testing.T) {
	srv, h := newTestServer(t)

	anon := httptest.NewRequest(http.MethodGet, "http://localhost/assets/app.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, anon)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("asset without a token: status = %d, want 401", rec.Code)
	}

	for path, want := range map[string]string{
		"/":               "<title>corvex</title>",
		"/assets/app.js":  "api/gates",
		"/assets/app.css": "--acc",
	} {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		req.Header.Set("Authorization", "Bearer "+srv.Token())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s does not look like the shipped asset (missing %q)", path, want)
		}
	}
}

// Invariant 4 reaches the UI too: no domain vocabulary may be embedded in the
// binary, and the SPA is embedded in the binary.
func TestUI_CarriesNoDomain(t *testing.T) {
	_, h := newTestServer(t)
	srv, _ := server.New(server.Options{WorkDir: t.TempDir()})
	for _, path := range []string{"/", "/assets/app.js", "/assets/app.css"} {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		req.Header.Set("Authorization", "Bearer "+srv.Token())
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		body := strings.ToLower(rec.Body.String())
		for _, word := range []string{"azure", "smartcare", "yandeh"} {
			if strings.Contains(body, word) {
				t.Errorf("%s carries domain vocabulary (%q)", path, word)
			}
		}
	}
	_ = h
}

// The two properties the dispatch sells, and that an audit found unproven: the
// child really is detached (its own session, so the UI's terminal signals do not
// reach it), and it does not become a zombie when it exits.
func TestStartRun_ChildIsItsOwnSessionAndIsReaped(t *testing.T) {
	workDir := t.TempDir()
	sid := filepath.Join(workDir, "sid")
	stub := filepath.Join(t.TempDir(), "corvex-stub")
	// Setsid makes the child the leader of a NEW session and process group, so
	// its pgid equals its own pid. `sess=` is not portable (darwin prints 0),
	// pgid is.
	script := "#!/bin/sh\nps -o pgid= -p $$ > " + sid + "\necho $$ >> " + sid + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	srv, err := server.New(server.Options{WorkDir: workDir, Binary: stub})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	rec, body := post(t, srv.Handler(), srv, "/api/runs", `{"target":"demo"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}

	pid := int(body["pid"].(float64))
	if pid <= 0 {
		t.Fatalf("the response reported pid %d — Release() zeroes it, so the caller gets a sentinel", pid)
	}

	// Wait for the stub to record its session, then check both halves.
	var fields []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(sid); err == nil {
			fields = strings.Fields(string(data))
			if len(fields) >= 2 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(fields) < 2 {
		t.Fatal("the spawned process never reported its session")
	}
	if fields[0] != fields[1] {
		t.Errorf("process group %s != pid %s: the child shares this process's group, so a Ctrl-C in the shell that started `corvex ui` would take the run down with it", fields[0], fields[1])
	}

	// And it is reaped: a zombie still answers signal 0, which is precisely how
	// a dead run would read as `alive`.
	zombieDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(zombieDeadline) {
		out, _ := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
		state := strings.TrimSpace(string(out))
		if state == "" {
			return // gone from the table: reaped
		}
		if !strings.HasPrefix(state, "Z") {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		time.Sleep(50 * time.Millisecond)
	}
	out, _ := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	t.Errorf("pid %d is still in the process table as %q — nobody reaped it", pid, strings.TrimSpace(string(out)))
}

// The safety property the action log's own comment asserts: what it writes is
// not committable. It was asserted and not established — F1's `*` guard is
// written when a run RECORD is written, and a UI that only approved a gate has
// no record.
func TestActionLog_EstablishesTheGitignoreItClaims(t *testing.T) {
	workDir := t.TempDir()
	srv, err := server.New(server.Options{WorkDir: workDir})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	// One refused approval is enough: the point is that the log exists at all.
	post(t, srv.Handler(), srv, "/api/gates/run_8f21/approve", `{"step":"S01"}`)

	guard := filepath.Join(workDir, ".corvex", "runs", ".gitignore")
	body, err := os.ReadFile(guard)
	if err != nil {
		t.Fatalf("the UI wrote into .corvex/runs/ without the ignore guard: %v", err)
	}
	if strings.TrimSpace(string(body)) != "*" {
		t.Errorf("guard is %q, want `*`", body)
	}
}

// The UI's reading lock has to start from what is on disk: `corvex gate ack`
// and the checkbox write to the same place, and a lock that ignored the
// persisted marks would tell somebody who already read the evidence that they
// had not. This asserts the wiring exists in the shipped asset — the behaviour
// itself belongs to a browser, which this suite does not have.
func TestUI_ReadingLockSeedsFromThePersistedMarks(t *testing.T) {
	srv, h := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/assets/app.js", nil)
	req.Header.Set("Authorization", "Bearer "+srv.Token())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{"g.reads", "alreadyRead"} {
		if !strings.Contains(body, want) {
			t.Errorf("the gate screen does not read the persisted marks (missing %q)", want)
		}
	}
}
