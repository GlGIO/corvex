package cmd

import (
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/types"
)

// Characterization for `gate audit`, in the same shape as the rest of the gate
// net: a golden per observable output.
//
// # Why these goldens may print durations
//
// The rest of the gate net scrubs every duration, because `gate list` and
// `gate show` compute "waiting" against the real clock — a frozen record would
// read `stale` and the number would move on every run. The audit computes from
// the TWO STAMPS ON THE GATE FILE and never from now(), so 4s is 4s on any
// machine at any time. These goldens therefore use scrubExcept(…, "dur"), which
// is the whole point: a net that scrubbed the measurement would characterise a
// layout instead of a sensor.
//
// $CORVEX_HOME is always a scratch dir here. The audit walks every repository the
// global index knows, so a test that inherited the real home would read the
// author's own gates and the golden would be different on every machine.

// seedAuditGates writes the corpus the audit screens are characterised against:
// one keystroke approval, one rejection that was read first, one expiry, and one
// gate still open.
//
// The three decided ones go through the real gate.Decide (and gate.MarkRead), not
// through a hand-written file, because two of the four cases only exist as a
// consequence of what Decide does with an inline --ack.
func seedAuditGates(t *testing.T, repo string) {
	t.Helper()
	t.Setenv("CORVEX_HOME", t.TempDir())
	now := time.Now().UTC()

	open := func(runID, stepID, label string, evidence []types.Evidence, openedAt time.Time) {
		if err := gate.Open(gate.Pending{
			RunID: runID, StepID: stepID, Repo: repo, Project: "alpha", Recipe: "gate-demo",
			Nature: types.GateHuman, Label: label, Title: "Aplicar migration em STG",
			Evidence: evidence, OpenedAt: openedAt,
		}); err != nil {
			t.Fatalf("opening gate %s/%s: %v", runID, stepID, err)
		}
	}
	decide := func(runID, stepID string, d gate.Decision) {
		if _, err := gate.Decide(repo, runID, stepID, d); err != nil {
			t.Fatalf("deciding gate %s/%s: %v", runID, stepID, err)
		}
	}

	// Approved four seconds after it opened, acknowledging the required reading
	// in the same command: read gap exactly 0. This is the row the whole screen
	// exists to surface.
	open("run_1111", "S01", "Aprovacao de STG", gateEvidence(), now.Add(-2*time.Hour))
	decide("run_1111", "S01", gate.Decision{
		Verdict: gate.Approved, DecidedAt: now.Add(-2 * time.Hour).Add(4 * time.Second), Acked: []string{"Migration"},
	})

	// Read seven minutes before it was rejected: a separate `gate ack` happened,
	// and the gap proves it.
	open("run_2222", "S01", "Aprovacao de STG", gateEvidence(), now.Add(-3*time.Hour))
	if _, _, err := gate.MarkRead(repo, "run_2222", "S01", []string{"Migration"}, now.Add(-3*time.Hour).Add(2*time.Minute)); err != nil {
		t.Fatalf("marking read: %v", err)
	}
	decide("run_2222", "S01", gate.Decision{
		Verdict: gate.Rejected, DecidedAt: now.Add(-3 * time.Hour).Add(9 * time.Minute), Reason: "migration is wrong",
	})

	// Nobody appeared: an hour later the run wrote the expiry itself.
	open("run_3333", "S01", "Aprovacao de STG", gateEvidence(), now.Add(-5*time.Hour))
	decide("run_3333", "S01", gate.Decision{
		Verdict: gate.Expired, DecidedAt: now.Add(-4 * time.Hour), Reason: "no decision before expires_after",
	})

	// Still waiting: an open gate has no latency and no gap, and must not be
	// counted as either.
	open("run_4444", "S02", "Deploy em producao", gateEvidence(), now.Add(-30*time.Minute))
}

func TestCharacterizeGateAuditHelp(t *testing.T) {
	args := []string{"gate", "audit", "--help"}
	stdout, stderr, err := runCLI(t, args...)
	// "dur" kept so the golden pins the DEFAULT of --suspect-under: the policy
	// threshold is the one number in this command that is an opinion, and a
	// silent change to it should fail a test.
	goldenAssert(t, "gate_audit_help", scrubExcept(transcript(args, stdout, stderr, err), "dur"))
}

func TestCharacterizeGateAuditEmpty(t *testing.T) {
	f := newFixture(t)
	t.Setenv("CORVEX_HOME", t.TempDir())
	args := []string{"gate", "audit"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_audit_empty", scrubExcept(transcript(args, stdout, stderr, err), "dur"))
}

func TestCharacterizeGateAudit(t *testing.T) {
	f := newFixture(t)
	seedAuditGates(t, f.Dir)
	args := []string{"gate", "audit"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_audit", scrubExcept(transcript(args, stdout, stderr, err), "dur"))
}

func TestCharacterizeGateAuditJSON(t *testing.T) {
	f := newFixture(t)
	seedAuditGates(t, f.Dir)
	args := []string{"gate", "audit", "--json"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_audit_json", scrubExcept(transcript(args, stdout, stderr, err), "dur"))
}

// TestGateAuditNeverPrintsEvidence is the leak guard at the CLI level. `gate show`
// prints evidence because a person asked for it; `gate audit` is a routine pull
// over every gate in every repository, and the two must not have the same blast
// radius.
func TestGateAuditNeverPrintsEvidence(t *testing.T) {
	f := newFixture(t)
	t.Setenv("CORVEX_HOME", t.TempDir())
	if err := gate.Open(gate.Pending{
		RunID: "run_9999", StepID: "S01", Repo: f.Dir, Project: "alpha", Recipe: "gate-demo",
		Nature: types.GateHuman, Label: "Aprovacao de STG", OpenedAt: time.Now().UTC(),
		Evidence: []types.Evidence{{Kind: types.EvidenceTestOutput, Label: "Testes", RequiredReading: true,
			Content: "ANTHROPIC_API_KEY=sk-ant-CANARY111 /Users/someone/secret"}},
	}); err != nil {
		t.Fatalf("opening the gate: %v", err)
	}
	for _, args := range [][]string{{"gate", "audit"}, {"gate", "audit", "--json"}} {
		stdout, stderr, err := runCLIIn(t, f.Dir, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for _, canary := range []string{"CANARY111", "sk-ant", "/Users/someone", "Testes"} {
			if contains(stdout+stderr, canary) {
				t.Errorf("%v printed %q — the audit carries labels and counts, never evidence", args, canary)
			}
		}
	}
}

// TestGateAuditRefusesTwoWindows: --all and --since are two answers to one
// question, and the flag that loses must not lose silently.
func TestGateAuditRefusesTwoWindows(t *testing.T) {
	f := newFixture(t)
	t.Setenv("CORVEX_HOME", t.TempDir())
	if _, _, err := runCLIIn(t, f.Dir, "gate", "audit", "--all", "--since", "2d"); err == nil {
		t.Fatal("--all with an explicit --since was accepted; one of the two windows was silently discarded")
	}
	if _, _, err := runCLIIn(t, f.Dir, "gate", "audit", "--all"); err != nil {
		t.Fatalf("--all alone must work: %v", err)
	}
}
