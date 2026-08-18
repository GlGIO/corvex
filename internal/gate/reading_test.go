package gate

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

// twoRequired is a gate where reading is not a single label: with one required
// item, "partially read" cannot be expressed, and partially read is the state
// this whole feature exists to hold between two sessions.
func twoRequired(repo string) Pending {
	p := samplePending(repo)
	p.Evidence = []types.Evidence{
		{Kind: types.EvidenceSQL, Label: "Migration", RequiredReading: true, Content: "alter table users add column x int;"},
		{Kind: types.EvidenceVerdict, Label: "Plano de query", RequiredReading: true, Content: "seq scan on users"},
		{Kind: types.EvidenceTestOutput, Label: "Testes", Status: types.EvidencePass, Content: "312/312"},
	}
	return p
}

func mustOpen(t *testing.T, p Pending) Pending {
	t.Helper()
	if err := Open(p); err != nil {
		t.Fatalf("Open: %v", err)
	}
	return p
}

// TestMarkReadSurvivesTheProcess is the F5 requirement stated as a test: closing
// the terminal does not reset reading state, which can only be true if the mark
// is on disk rather than in the invocation that made it.
func TestMarkReadSurvivesTheProcess(t *testing.T) {
	repo := t.TempDir()
	p := mustOpen(t, twoRequired(repo))
	at := time.Date(2026, 8, 17, 14, 3, 1, 0, time.UTC)

	_, added, err := MarkRead(repo, p.RunID, p.StepID, []string{"migration"}, at)
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if len(added) != 1 || added[0] != "Migration" {
		t.Fatalf("added = %v, want the gate's own spelling [Migration]", added)
	}

	reread, err := Read(repo, p.RunID, p.StepID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	mark, ok := reread.ReadOf("Migration")
	if !ok || !mark.At.Equal(at) {
		t.Fatalf("mark after reload = %+v (%v), want one dated %s", mark, ok, at)
	}
	if got := reread.MissingReading(nil); len(got) != 1 || got[0] != "Plano de query" {
		t.Fatalf("still missing = %v, want [Plano de query]", got)
	}
}

// TestFirstReadingWins: re-acknowledging must not refresh the clock, or every
// approval would look reflexive the moment somebody repeated a --ack.
func TestFirstReadingWins(t *testing.T) {
	repo := t.TempDir()
	p := mustOpen(t, twoRequired(repo))
	first := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	later := first.Add(6 * time.Hour)

	if _, _, err := MarkRead(repo, p.RunID, p.StepID, []string{"Migration"}, first); err != nil {
		t.Fatalf("first MarkRead: %v", err)
	}
	after, added, err := MarkRead(repo, p.RunID, p.StepID, []string{"Migration"}, later)
	if err != nil {
		t.Fatalf("second MarkRead: %v", err)
	}
	if len(added) != 0 {
		t.Fatalf("added = %v, want nothing new on a repeated ack", added)
	}
	mark, _ := after.ReadOf("Migration")
	if !mark.At.Equal(first) {
		t.Fatalf("mark moved to %s; the first reading is the one the sensor asks about", mark.At)
	}
	if n := len(after.Reads); n != 1 {
		t.Fatalf("reads = %d, want one mark per label", n)
	}
}

// TestApproveAcceptsAPriorReading is the point of the phase: read on Monday,
// decide on Tuesday, with no --ack in the decision at all.
func TestApproveAcceptsAPriorReading(t *testing.T) {
	repo := t.TempDir()
	p := mustOpen(t, twoRequired(repo))
	monday := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	tuesday := monday.Add(24 * time.Hour)

	if _, _, err := MarkRead(repo, p.RunID, p.StepID, []string{"Migration", "Plano de query"}, monday); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	after, err := Decide(repo, p.RunID, p.StepID, Decision{Verdict: Approved, DecidedAt: tuesday})
	if err != nil {
		t.Fatalf("Decide with no inline ack after reading everything: %v", err)
	}
	if !after.Decided() || after.Decision.Verdict != Approved {
		t.Fatalf("decision = %+v, want approved", after.Decision)
	}
	// The gap between reading and deciding is what F5 measures, so it has to be
	// reconstructable from the file alone.
	mark, ok := after.ReadOf("Migration")
	if !ok || after.Decision.DecidedAt.Sub(mark.At) != 24*time.Hour {
		t.Fatalf("gap = %v, want 24h between reading and decision", after.Decision.DecidedAt.Sub(mark.At))
	}
}

// TestApproveStillRefusesWhatWasNeverRead: the lock moved, it did not loosen.
func TestApproveStillRefusesWhatWasNeverRead(t *testing.T) {
	repo := t.TempDir()
	p := mustOpen(t, twoRequired(repo))
	if _, _, err := MarkRead(repo, p.RunID, p.StepID, []string{"Migration"}, time.Now().UTC()); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	_, err := Decide(repo, p.RunID, p.StepID, Decision{Verdict: Approved, DecidedAt: time.Now().UTC()})
	var unread *UnreadError
	if !errors.As(err, &unread) {
		t.Fatalf("Decide = %v, want *UnreadError naming the unread half", err)
	}
	if len(unread.Missing) != 1 || unread.Missing[0] != "Plano de query" {
		t.Fatalf("missing = %v, want only the item nobody read", unread.Missing)
	}
}

// TestInlineAckIsRecordedAsAReadingAtTheDecision keeps the stamp visible: an
// approval that acknowledged everything in the same command leaves a zero gap,
// and that zero is the measurement, not an artefact to be smoothed away.
func TestInlineAckIsRecordedAsAReadingAtTheDecision(t *testing.T) {
	repo := t.TempDir()
	p := mustOpen(t, twoRequired(repo))
	at := time.Date(2026, 8, 17, 14, 3, 2, 0, time.UTC)

	after, err := Decide(repo, p.RunID, p.StepID, Decision{
		Verdict: Approved, DecidedAt: at, Acked: []string{"migration", "PLANO DE QUERY"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(after.Reads) != 2 {
		t.Fatalf("reads = %+v, want one per acknowledged item", after.Reads)
	}
	for _, r := range after.Reads {
		if !r.At.Equal(at) {
			t.Fatalf("mark %q dated %s, want the decision instant", r.Label, r.At)
		}
	}
	// Canonical spelling, not the user's casing: two spellings of one label
	// would be two marks, and the count above is what notices.
	if after.Reads[0].Label != "Migration" || after.Reads[1].Label != "Plano de query" {
		t.Fatalf("labels = %+v, want the gate's own spelling", after.Reads)
	}
}

// TestMarkReadRefusesALabelTheGateDoesNotCarry: accepting a typo silently is how
// somebody arrives at approve time believing they were covered.
func TestMarkReadRefusesALabelTheGateDoesNotCarry(t *testing.T) {
	repo := t.TempDir()
	p := mustOpen(t, twoRequired(repo))
	_, _, err := MarkRead(repo, p.RunID, p.StepID, []string{"Migraton"}, time.Now().UTC())
	var unknown *UnknownLabelError
	if !errors.As(err, &unknown) {
		t.Fatalf("MarkRead with a typo = %v, want *UnknownLabelError", err)
	}
	if len(unknown.Known) != 3 || !strings.Contains(err.Error(), "Plano de query") {
		t.Fatalf("the refusal must list what the gate does carry: %v", err)
	}
	after, _ := Read(repo, p.RunID, p.StepID)
	if len(after.Reads) != 0 {
		t.Fatalf("a refused ack wrote %+v; a rejected label must leave no trace", after.Reads)
	}
}

// TestMarkReadOnOptionalEvidence: reading an item that is not required is a real
// fact and is kept, but it unlocks nothing. One rule — the label must exist —
// rather than two.
func TestMarkReadOnOptionalEvidence(t *testing.T) {
	repo := t.TempDir()
	p := mustOpen(t, twoRequired(repo))
	after, added, err := MarkRead(repo, p.RunID, p.StepID, []string{"Testes"}, time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if len(added) != 1 {
		t.Fatalf("added = %v, want the optional item recorded", added)
	}
	if got := after.MissingReading(nil); len(got) != 2 {
		t.Fatalf("missing = %v, want both required items still owed", got)
	}
}

// TestMarkReadRefusesADecidedGate: after the answer is written the run has
// already moved, and a mark that changes nothing would only make the file lie
// about the order things happened in.
func TestMarkReadRefusesADecidedGate(t *testing.T) {
	repo := t.TempDir()
	p := mustOpen(t, twoRequired(repo))
	if _, err := Decide(repo, p.RunID, p.StepID, Decision{
		Verdict: Rejected, DecidedAt: time.Now().UTC(), Reason: "no",
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if _, _, err := MarkRead(repo, p.RunID, p.StepID, []string{"Migration"}, time.Now().UTC()); err == nil {
		t.Fatal("MarkRead on a decided gate succeeded, want a refusal")
	}
}
