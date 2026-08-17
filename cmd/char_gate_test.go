package cmd

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/types"
)

// Characterization for the `gate` surface, in the same shape as the F-1 net: a
// golden per observable output, so a change to what the person standing at a
// gate reads shows up as a diff rather than as a surprise.
//
// The evidence bodies here are deliberately boring. A golden that embeds a real
// diff would be rewritten on every unrelated change, and a net that gets
// rewritten routinely is a net nobody trusts.

// seedGate registers a parked run in the scratch home and opens a gate for it.
//
// The id is injected, so no concrete run id ever reaches a golden. The clock is
// NOT: liveness is a comparison against the real clock made by the resolver the
// CLI builds for itself, so a frozen record would read `stale` and every golden
// here would characterise a dead run instead of a waiting one. Nothing
// time-dependent survives into the goldens — the duration is scrubbed and no
// timestamp is printed.
//
// The machine id is likewise left to the registry, so writer and reader derive
// the same one from the scratch CORVEX_HOME. Overriding it on one side only is
// how you get `unknown`.
func seedGate(t *testing.T, repo string, evidence []types.Evidence) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CORVEX_HOME", home)
	now := time.Now().UTC()
	const runID = "run_8f21"

	reg := run.Registry{
		Repo: repo, Home: home,
		NewID: func() (string, error) { return runID, nil },
	}
	h, err := reg.Start(run.StartOptions{Project: "alpha", Recipe: "gate-demo"})
	if err != nil {
		t.Fatalf("registering the run: %v", err)
	}
	if err := h.SetStatus(run.StatusParked); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if err := gate.Open(gate.Pending{
		RunID: runID, StepID: "S01", Repo: repo, Project: "alpha", Recipe: "gate-demo",
		Nature: types.GateHuman, Label: "Aprovacao de STG", Prompt: "Aplicar esta migration em STG?",
		Title: "Aplicar migration em STG", Evidence: evidence, OpenedAt: now,
	}); err != nil {
		t.Fatalf("opening the gate: %v", err)
	}
	return runID
}

func gateEvidence() []types.Evidence {
	return []types.Evidence{
		{Kind: types.EvidenceSQL, Label: "Migration", RequiredReading: true, Content: "alter table users add column x int;"},
		{Kind: types.EvidenceTestOutput, Label: "Testes", Status: types.EvidencePass, Content: "312/312"},
	}
}

func TestCharacterizeGateHelp(t *testing.T) {
	args := []string{"gate", "--help"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "gate_help", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeGateListEmpty(t *testing.T) {
	f := newFixture(t)
	t.Setenv("CORVEX_HOME", t.TempDir())
	args := []string{"gate", "list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_list_empty", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeGateList(t *testing.T) {
	f := newFixture(t)
	seedGate(t, f.Dir, gateEvidence())
	args := []string{"gate", "list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_list", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeGateShow(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, gateEvidence())
	args := []string{"gate", "show", runID, "--step", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_show", scrub(transcript(args, stdout, stderr, err)))
}

// TestCharacterizeGateApproveWithoutAck is the approval lock as the user meets
// it. If this golden ever loses its refusal, the lock came off.
func TestCharacterizeGateApproveWithoutAck(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, gateEvidence())
	args := []string{"gate", "approve", runID, "--step", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_approve_missing_ack", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeGateApprove(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, gateEvidence())
	args := []string{"gate", "approve", runID, "--step", "S01", "--ack", "Migration"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_approve", scrub(transcript(args, stdout, stderr, err)))

	// The golden shows what was printed; this shows what was written, which is
	// the half another process depends on.
	after, readErr := gate.Read(f.Dir, runID, "S01")
	if readErr != nil {
		t.Fatalf("reading the decided gate: %v", readErr)
	}
	if !after.Decided() || after.Decision.Verdict != gate.Approved {
		t.Fatalf("approval did not reach disk: %+v", after.Decision)
	}
}

func TestCharacterizeGateReject(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, gateEvidence())
	args := []string{"gate", "reject", runID, "--step", "S01", "--reason", "migration is wrong"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_reject", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeGateUnknownRun(t *testing.T) {
	f := newFixture(t)
	t.Setenv("CORVEX_HOME", t.TempDir())
	args := []string{"gate", "show", "run_dead"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_show_unknown_run", scrub(transcript(args, stdout, stderr, err)))
}

// TestGateEvidenceNeverLeavesTheIgnoredDirectory is the leak guard at the CLI
// level: printing evidence must not be the same thing as persisting it anywhere
// a commit can reach.
func TestGateEvidenceNeverLeavesTheIgnoredDirectory(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, []types.Evidence{
		{Kind: types.EvidenceTestOutput, Label: "Testes", RequiredReading: true,
			Content: "ANTHROPIC_API_KEY=sk-ant-CANARY111 /Users/someone/secret"},
	})
	if _, _, err := runCLIIn(t, f.Dir, "gate", "show", runID, "--step", "S01"); err != nil {
		t.Fatalf("gate show: %v", err)
	}
	guard := f.Read(filepath.Join(".corvex", "runs", ".gitignore"))
	if guard != "*\n" {
		t.Fatalf("the ignore guard is %q, want \"*\\n\"", guard)
	}
}
