package e2e

// The F2 acceptance criterion, proved against the real binary and real
// processes: a human gate blocks the run, and a *different process* releases it.
//
// A goroutine talking to another goroutine proves none of this. The contract
// that has to hold is the one on disk — the gate file, the run record and the
// global index — and the only way to test a cross-process contract is to cross
// the process boundary. Same reasoning as the F1 identity tests next door.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/run"
)

// gateTasksMD is a human gate guarding a second step, plus one piece of
// required-reading evidence so the approval lock is exercised end to end.
const gateTasksMD = "---\ngenerated_by: corvex-recipe:gate-e2e\ndag:\n  S01: []\n  S02:\n    - S01\n---\n\n" +
	"## S01 — Apply migration in STG ⬜ PENDING\n\n" +
	"```yaml\nkind: tool\ncommand: \"true\"\ngates:\n" +
	"    - nature: human\n      when: before\n      label: Aprovação de STG\n      prompt: Aplicar esta migration em STG?\n" +
	"evidence:\n" +
	"    - kind: sql\n      label: Migration\n      required_reading: true\n      from: echo 'alter table users add column x int;'\n" +
	"```\n\n" +
	"### O que fazer\nApply it\n\n" +
	"---\n\n" +
	"## S02 — After the gate ⬜ PENDING\n\n" +
	"```yaml\nkind: tool\ncommand: \"true\"\ndepends_on: [S01]\n```\n\n" +
	"### O que fazer\nRuns only if the gate opened\n"

// setupGateRepo is setupIdentityRepo with a gated tasks.md.
func setupGateRepo(t *testing.T) string {
	t.Helper()
	dir := setupIdentityRepo(t)
	writeFile(t, filepath.Join(dir, ".corvex", "tasks", "demo", "tasks.md"), gateTasksMD)
	return dir
}

// waitForOpenGate polls the repository the way a second process would, and
// returns the first gate waiting for a decision.
func waitForOpenGate(t *testing.T, dir string) gate.Pending {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if open, err := gate.ListOpen(dir); err == nil && len(open) > 0 {
			return open[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no gate ever opened in %s", dir)
	return gate.Pending{}
}

// corvexGate runs a `corvex gate ...` invocation as its own process.
func corvexGate(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binaryPath, append([]string{"gate"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestGate_ParksAndIsReleasedByASecondProcess is the phase's acceptance
// criterion, whole: a run parks on a human gate, a second process sees it in
// `corvex gate list`, approving from that second process releases it, and the
// run finishes on its own.
func TestGate_ParksAndIsReleasedByASecondProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	dir := setupGateRepo(t)
	stub := stubClaudeBin(t, identityStubPass)

	cmd, readLog := corvexRun(t, dir, stub)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the run: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	pending := waitForOpenGate(t, dir)
	if pending.StepID != "S01" || pending.Prompt == "" {
		t.Fatalf("gate on disk is missing detail: %+v", pending)
	}

	// The evidence the recipe declared was resolved and is waiting to be read.
	if len(pending.Evidence) == 0 {
		t.Fatal("the gate carries no evidence; the required-reading lock has nothing to lock on")
	}
	var sawRequired bool
	for _, e := range pending.Evidence {
		if e.RequiredReading {
			sawRequired = true
			if !strings.Contains(e.Content, "alter table users") {
				t.Errorf("declared `from` was not resolved: %q", e.Content)
			}
		}
	}
	if !sawRequired {
		t.Fatal("required_reading did not survive to disk")
	}

	// The run reported itself parked, and the append-only index kept the proof.
	waitForStatus(t, dir, pending.RunID, run.StatusParked)

	// A second process sees it.
	list, err := corvexGate(t, dir, "list")
	if err != nil {
		t.Fatalf("gate list failed: %v\n%s", err, list)
	}
	if !strings.Contains(list, pending.RunID) || !strings.Contains(list, "Aprovação de STG") {
		t.Fatalf("gate list did not show the waiting gate:\n%s", list)
	}

	// The run is genuinely held: it has not finished, and S02 has not run.
	if exited := cmd.ProcessState != nil; exited {
		t.Fatalf("the run exited instead of parking:\n%s", readLog())
	}

	// Approving without acknowledging the required reading is refused.
	out, err := corvexGate(t, dir, "approve", pending.RunID, "--step", "S01")
	if err == nil {
		t.Fatalf("approve without --ack succeeded; the lock is not armed:\n%s", out)
	}
	if !strings.Contains(out, "Migration") {
		t.Errorf("the refusal does not name what is missing:\n%s", out)
	}

	// Approving properly, from that second process, releases the run.
	out, err = corvexGate(t, dir, "approve", pending.RunID, "--step", "S01", "--ack", "Migration")
	if err != nil {
		t.Fatalf("approve failed: %v\n%s", err, out)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		if waitErr != nil {
			t.Fatalf("the released run exited %v:\n%s", waitErr, readLog())
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the run never noticed the approval:\n%s", readLog())
	}

	// The decision is on disk, and the gate is no longer open.
	after, err := gate.Read(dir, pending.RunID, "S01")
	if err != nil {
		t.Fatalf("reading the decided gate: %v", err)
	}
	if !after.Decided() || after.Decision.Verdict != gate.Approved {
		t.Fatalf("decision not recorded: %+v", after.Decision)
	}
	if len(after.Decision.Acked) != 1 || after.Decision.Acked[0] != "Migration" {
		t.Errorf("the acknowledgement was not recorded: %+v", after.Decision.Acked)
	}
	if open, _ := gate.ListOpen(dir); len(open) != 0 {
		t.Errorf("gate still listed as open after approval: %+v", open)
	}

	// And the run really did continue past the gate.
	body, err := os.ReadFile(filepath.Join(dir, ".corvex", "tasks", "demo", "tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "## S02 — After the gate ✅ PASSED") {
		t.Fatalf("S02 did not run after the gate opened:\n%s", body)
	}
}

// TestGate_RejectFromASecondProcessFailsTheStep: rejecting is a normal task
// failure, which is what makes the dependent cascade come for free.
func TestGate_RejectFromASecondProcessFailsTheStep(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	dir := setupGateRepo(t)
	stub := stubClaudeBin(t, identityStubPass)

	cmd, readLog := corvexRun(t, dir, stub)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the run: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	pending := waitForOpenGate(t, dir)
	// Rejecting needs no acknowledgement: you do not have to read the evidence
	// to say no.
	out, err := corvexGate(t, dir, "reject", pending.RunID, "--step", "S01", "--reason", "migration is wrong")
	if err != nil {
		t.Fatalf("reject failed: %v\n%s", err, out)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("the run never noticed the rejection:\n%s", readLog())
	}

	body, err := os.ReadFile(filepath.Join(dir, ".corvex", "tasks", "demo", "tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "## S01 — Apply migration in STG ❌ FAILED") {
		t.Errorf("the rejected step is not FAILED:\n%s", body)
	}
	if !strings.Contains(string(body), "## S02 — After the gate ⏭") {
		t.Errorf("the dependent step was not skipped:\n%s", body)
	}
}

// TestGate_ApproveGatesFlagStillSkipsTheWait keeps the CI path honest: without
// it a pipeline that reaches a gate would block until something killed it.
func TestGate_ApproveGatesFlagStillSkipsTheWait(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	dir := setupGateRepo(t)
	stub := stubClaudeBin(t, identityStubPass)

	cmd, readLog := corvexRun(t, dir, stub, "--approve-gates")
	if err := cmd.Run(); err != nil {
		t.Fatalf("--approve-gates run exited %v:\n%s", err, readLog())
	}
	if open, _ := gate.ListOpen(dir); len(open) != 0 {
		t.Errorf("--approve-gates still opened a gate file: %+v", open)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".corvex", "tasks", "demo", "tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "## S02 — After the gate ✅ PASSED") {
		t.Fatalf("the auto-approved run did not finish:\n%s", body)
	}
}

// waitForStatus blocks until a run's record reports the wanted status.
func waitForStatus(t *testing.T, dir, runID string, want run.Status) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if rec, err := run.ReadRecord(dir, runID); err == nil && rec.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s never reached status %s", runID, want)
}
