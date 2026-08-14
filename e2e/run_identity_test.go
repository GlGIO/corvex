package e2e

// The F1 acceptance criterion, proved against the real binary and real
// processes.
//
// Everything here needs a process boundary to mean anything. "State survives the
// session" is a claim about a process that has exited; "a second process lists
// what is alive" is a claim about a reader that did not start the run; and "a run
// killed with SIGKILL is not reported alive" cannot be produced at all without a
// process to kill. A function call in the test's own process proves none of the
// three, so these tests exec `corvex` and then read what it left behind.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/run"
)

const identityConfigYAML = `project:
  name: identity
  description: run identity e2e
provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_retries: 1
  auto_commit: false
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`

const identitySpecMD = "# demo\n\n## Objective\n\nProve run identity.\n"

const identityTasksMD = "---\ngenerated_by: e2e\ndag:\n  S01: []\n---\n\n" +
	"## S01 — First Task ⬜ PENDING\n\n" +
	"```yaml\ntype: general\n```\n\n" +
	"### O que fazer\nFirst task\n\n" +
	"### Critérios de sucesso\n- [ ] Done\n"

// identityStubPass speaks the stream-json protocol the claude-cli provider
// parses, with a payload that satisfies both the worker's TASK-REPORT parser and
// the reviewer's verdict parser.
const identityStubPass = `cat <<'JSON'
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Implemented it.\n\nTASK-REPORT\nSUMMARY: implemented the first task\nDECISIONS: chose the boring option\nHANDOFF: nothing pending\n\nVERDICT: PASS\n"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"done","total_cost_usd":0.02,"total_input_tokens":100,"total_output_tokens":50,"duration_ms":1000}
JSON`

// identityStubSlow blocks so the parent can catch the run while it is genuinely
// running. The sleep is bounded: if the parent's kill leaves it orphaned (corvex
// only tears down its direct child — a known, frozen defect) it still exits on
// its own instead of lingering on the machine.
const identityStubSlow = `sleep 20`

// setupIdentityRepo builds a git repository holding one legacy-path project:
// spec.md written by hand, tasks.md alongside it, and an anchor whose hash
// matches the spec so `run` does not call the planner. This is invariant 3's
// shape — `corvex run <project>` with no recipe anywhere.
func setupIdentityRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".corvex", "config.yaml"), identityConfigYAML)
	pDir := filepath.Join(dir, ".corvex", "tasks", "demo")
	writeFile(t, filepath.Join(pDir, "spec.md"), identitySpecMD)
	writeFile(t, filepath.Join(pDir, "tasks.md"), identityTasksMD)
	sum := sha256.Sum256([]byte(identitySpecMD))
	writeFile(t, filepath.Join(pDir, "anchor.yaml"), fmt.Sprintf(
		"project: demo\nupdated_at: \"2026-01-02T03:04:05Z\"\nspec_hash: %s\nintent: e2e\n",
		hex.EncodeToString(sum[:])))

	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "t@t.com"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stubClaudeBin writes a fake claude CLI and returns its path. CORVEX_CLAUDE_BIN
// is the only reliable way to keep the provider off the real binary.
func stubClaudeBin(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// corvexRun builds a `corvex run demo` command wired to a stub provider, with
// output going to a file rather than a pipe: a grandchild holding the parent's
// stdout pipe can hang the test binary.
func corvexRun(t *testing.T, dir, stub string, args ...string) (*exec.Cmd, func() string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "corvex.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logFile.Close() })

	cmd := exec.Command(binaryPath, append([]string{"run", "demo", "--plain", "--yes"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CORVEX_CLAUDE_BIN="+stub, "NO_COLOR=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	return cmd, func() string {
		data, _ := os.ReadFile(logPath)
		return string(data)
	}
}

// repoRuns reads the run records a repository holds, as a foreign process would.
func repoRuns(t *testing.T, dir string) []run.View {
	t.Helper()
	views, err := run.Resolver{}.ListRepo(dir)
	if err != nil {
		t.Fatalf("ListRepo(%s): %v", dir, err)
	}
	return views
}

// waitForRecord blocks until the repository holds n run records, so the test
// synchronises on an observable fact instead of a fixed sleep.
func waitForRecord(t *testing.T, dir string, n int) []run.View {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if views := repoRuns(t, dir); len(views) >= n {
			return views
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d run record(s) in %s", n, dir)
	return nil
}

// TestRunIdentitySurvivesTheProcessThatWroteIt runs the real binary twice on the
// same project and then, from a third process (this test), lists what those two
// runs left behind. Both writers have exited by the time anything is read, which
// is the whole point: identity is state on disk, not state in a session.
func TestRunIdentitySurvivesTheProcessThatWroteIt(t *testing.T) {
	dir := setupIdentityRepo(t)
	stub := stubClaudeBin(t, identityStubPass)

	for i := 1; i <= 2; i++ {
		cmd, logOf := corvexRun(t, dir, stub)
		if err := cmd.Run(); err != nil {
			t.Fatalf("run #%d failed: %v\n%s", i, err, logOf())
		}
	}

	views := repoRuns(t, dir)
	if len(views) != 2 {
		t.Fatalf("two invocations left %d record(s), want 2", len(views))
	}
	ids := map[string]bool{}
	for _, v := range views {
		rec := v.Record
		ids[rec.RunID] = true
		if !run.ValidID(rec.RunID) {
			t.Errorf("malformed run id %q", rec.RunID)
		}
		if rec.Project != "demo" {
			t.Errorf("run %s project = %q, want demo", rec.RunID, rec.Project)
		}
		if rec.Recipe != "" {
			t.Errorf("run %s recipe = %q, want empty: the legacy spec.md path has no recipe", rec.RunID, rec.Recipe)
		}
		if rec.Status != run.StatusDone {
			t.Errorf("run %s status = %q, want done", rec.RunID, rec.Status)
		}
		if v.Liveness == run.LivenessAlive {
			t.Errorf("run %s reads as alive though its process exited", rec.RunID)
		}
		if rec.PID <= 0 {
			t.Errorf("run %s recorded no pid, so no reader can probe it", rec.RunID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("the two runs share an id: %v", ids)
	}

	// The global index is the cross-repository answer, and it is what a second
	// process reads without knowing which repository to look in.
	global, err := run.Resolver{}.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := 0
	for _, v := range global {
		if ids[v.Record.RunID] {
			found++
			if v.Record.Status != run.StatusDone {
				t.Errorf("index status for %s = %q, want done", v.Record.RunID, v.Record.Status)
			}
		}
	}
	if found != 2 {
		t.Errorf("global index knows %d of the 2 runs", found)
	}

	// One ledger, two runs, every line attributable.
	entries, err := activity.Read(dir, "demo")
	if err != nil {
		t.Fatalf("activity.Read: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the runs wrote no ledger entries")
	}
	perRun := map[string]int{}
	for _, e := range entries {
		if e.RunID == "" {
			t.Fatalf("ledger line %s carries no run id", e.Type)
		}
		if !ids[e.RunID] {
			t.Errorf("ledger line %s carries unknown run %q", e.Type, e.RunID)
		}
		perRun[e.RunID]++
	}
	if len(perRun) != 2 {
		t.Errorf("ledger holds %d distinct run(s), want 2: %v", len(perRun), perRun)
	}
}

// TestSigkilledRunIsNotReportedAlive is the case status alone cannot cover: the
// process is destroyed with no chance to write anything, so `running` stays on
// disk forever. A reader has to conclude death from the pid, and this test proves
// it does — including that the stale status is still sitting there, which is why
// liveness may never be read from the status field.
func TestSigkilledRunIsNotReportedAlive(t *testing.T) {
	dir := setupIdentityRepo(t)
	stub := stubClaudeBin(t, identityStubSlow)

	cmd, logOf := corvexRun(t, dir, stub)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting corvex: %v", err)
	}
	views := waitForRecord(t, dir, 1)
	rec := views[0].Record
	if rec.Status != run.StatusRunning {
		t.Fatalf("a running run recorded status %q, want running\n%s", rec.Status, logOf())
	}
	if views[0].Liveness != run.LivenessAlive {
		t.Fatalf("a live run reads as %q, want alive (pid %d)\n%s", views[0].Liveness, rec.PID, logOf())
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("SIGKILL: %v", err)
	}
	// Reap it. A killed-but-unreaped child stays a zombie, and a zombie still
	// answers signal 0 — it would be reported alive, and the test would pass for
	// the wrong reason.
	_ = cmd.Wait()

	after := repoRuns(t, dir)
	if len(after) != 1 {
		t.Fatalf("repo holds %d record(s), want 1", len(after))
	}
	if got := after[0].Record.Status; got != run.StatusRunning {
		t.Errorf("status after SIGKILL = %q; the point of this test is that it stays %q", got, run.StatusRunning)
	}
	if got := after[0].Liveness; got != run.LivenessDead {
		t.Errorf("liveness after SIGKILL = %q, want dead", got)
	}
	if after[0].Alive() {
		t.Error("a SIGKILLed run is being listed as alive")
	}
}

// TestInterruptedRunIsRecordedCanceled is the graceful half. Ctrl-C has to close
// the run out explicitly: relying on the pid probe here would leave `running` on
// disk for a run the operator ended deliberately, and a resume decision made
// against that record would be made against a lie.
func TestInterruptedRunIsRecordedCanceled(t *testing.T) {
	dir := setupIdentityRepo(t)
	stub := stubClaudeBin(t, identityStubSlow)

	cmd, logOf := corvexRun(t, dir, stub)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting corvex: %v", err)
	}
	waitForRecord(t, dir, 1)

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("SIGINT: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("corvex did not exit within 60s of SIGINT\n%s", logOf())
	}

	views := repoRuns(t, dir)
	if len(views) != 1 {
		t.Fatalf("repo holds %d record(s), want 1", len(views))
	}
	if got := views[0].Record.Status; got != run.StatusCanceled {
		t.Errorf("status after SIGINT = %q, want canceled\n%s", got, logOf())
	}
	if views[0].Alive() {
		t.Error("an interrupted run is being listed as alive")
	}

	// The same conclusion has to be reachable from the global index, which is
	// where a supervisor looks — the terminal status is a second appended line,
	// never a rewrite.
	v, found, err := run.Resolver{}.Get(views[0].Record.RunID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatalf("run %s is missing from the global index", views[0].Record.RunID)
	}
	if v.Record.Status != run.StatusCanceled {
		t.Errorf("index status = %q, want canceled", v.Record.Status)
	}
}
