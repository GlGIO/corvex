package cmd

import (
	"testing"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/types"
)

// Characterization for `gate ack`, the F5 verb that separates reading from
// deciding. The goldens here are new files rather than rewrites of F2's: a gate
// nobody has opened yet must still print exactly what it printed before this
// existed, and the only existing golden that moves is `gate_help`, which cannot
// not move when a subcommand is added.

// ackEvidence has two required items so that "half read" is expressible. With a
// single one the interesting state — persisted, incomplete, still refusing —
// cannot occur.
func ackEvidence() []types.Evidence {
	return []types.Evidence{
		{Kind: types.EvidenceSQL, Label: "Migration", RequiredReading: true, Content: "alter table users add column x int;"},
		{Kind: types.EvidenceVerdict, Label: "Plano de query", RequiredReading: true, Content: "seq scan on users"},
		{Kind: types.EvidenceTestOutput, Label: "Testes", Status: types.EvidencePass, Content: "312/312"},
	}
}

func TestCharacterizeGateAck(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, ackEvidence())
	args := []string{"gate", "ack", runID, "--step", "S01", "--ack", "Migration"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_ack", scrub(transcript(args, stdout, stderr, err)))

	// The screen is half of it; the other half is that the run is still parked.
	// An `ack` that decided anything would be `approve` under another name.
	after, readErr := gate.Read(f.Dir, runID, "S01")
	if readErr != nil {
		t.Fatalf("reading the gate: %v", readErr)
	}
	if after.Decided() {
		t.Fatalf("ack decided the gate: %+v", after.Decision)
	}
	if len(after.Reads) != 1 || after.Reads[0].Label != "Migration" {
		t.Fatalf("reads on disk = %+v, want one mark for Migration", after.Reads)
	}
}

// TestCharacterizeGateAckThenApprove is the whole feature in two invocations:
// two processes, no --ack at the decision, and the lock still satisfied.
func TestCharacterizeGateAckThenApprove(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, ackEvidence())
	if _, _, err := runCLIIn(t, f.Dir, "gate", "ack", runID, "--step", "S01",
		"--ack", "Migration", "--ack", "Plano de query"); err != nil {
		t.Fatalf("gate ack: %v", err)
	}
	args := []string{"gate", "approve", runID, "--step", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_approve_after_ack", scrub(transcript(args, stdout, stderr, err)))

	after, readErr := gate.Read(f.Dir, runID, "S01")
	if readErr != nil {
		t.Fatalf("reading the gate: %v", readErr)
	}
	if !after.Decided() || after.Decision.Verdict != gate.Approved {
		t.Fatalf("approval did not reach disk: %+v", after.Decision)
	}
	if len(after.Decision.Acked) != 0 {
		t.Fatalf("Acked = %v, want empty: nothing was acknowledged in the decision itself", after.Decision.Acked)
	}
	if len(after.Reads) != 2 {
		t.Fatalf("reads = %+v, want the two marks the earlier process wrote", after.Reads)
	}
}

// TestCharacterizeGateApprovePartiallyRead: the lock did not loosen. Half read
// is still refused, and the refusal names only the half that is owed.
func TestCharacterizeGateApprovePartiallyRead(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, ackEvidence())
	if _, _, err := runCLIIn(t, f.Dir, "gate", "ack", runID, "--step", "S01", "--ack", "Migration"); err != nil {
		t.Fatalf("gate ack: %v", err)
	}
	args := []string{"gate", "approve", runID, "--step", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_approve_partially_read", scrub(transcript(args, stdout, stderr, err)))
}

// TestCharacterizeGateShowAfterAck: `gate show` is where the labels are
// discovered, so it is also where progress has to be visible.
func TestCharacterizeGateShowAfterAck(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, ackEvidence())
	if _, _, err := runCLIIn(t, f.Dir, "gate", "ack", runID, "--step", "S01", "--ack", "Migration"); err != nil {
		t.Fatalf("gate ack: %v", err)
	}
	args := []string{"gate", "show", runID, "--step", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_show_after_ack", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeGateAckUnknownLabel(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, ackEvidence())
	args := []string{"gate", "ack", runID, "--step", "S01", "--ack", "Migraton"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_ack_unknown_label", scrub(transcript(args, stdout, stderr, err)))
}

// TestCharacterizeGateAckWithoutLabels pins the refusal to acknowledge nothing:
// there is no --all, because a switch that marks everything read in one word is
// the rubber stamp the lock exists to make expensive.
func TestCharacterizeGateAckWithoutLabels(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, ackEvidence())
	args := []string{"gate", "ack", runID, "--step", "S01"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_ack_no_labels", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeGateAckHelp(t *testing.T) {
	args := []string{"gate", "ack", "--help"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "gate_ack_help", scrub(transcript(args, stdout, stderr, err)))
}
