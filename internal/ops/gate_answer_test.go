package ops

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/types"
)

// openQuestion adds a second gate to the fixture's run — one that asks instead
// of proposing. Same directory, same writer, no registry of its own: if that
// were not true, every reader below would need to be taught about a second
// place to look, which is the cost the sibling type would have carried.
func openQuestion(t *testing.T, f gateFixture, stepID string) {
	t.Helper()
	if err := gate.Open(gate.Pending{
		RunID: f.runID, StepID: stepID, Repo: f.repo, Project: "demo", Recipe: "gate-demo",
		Nature: types.GateQuestion, Label: "Qual base",
		Prompt: "Contra qual base o passo deve rodar?", Title: "Rodar a carga",
		OpenedAt: f.now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("opening the question: %v", err)
	}
}

// TestInbox_ListsAQuestionBesideAGate is the criterion the field-versus-type
// choice was made on: the inbox gained no branch, no third slice and no second
// walk, and a question shows up in "what is waiting on me" because it is a gate
// file like any other.
func TestInbox_ListsAQuestionBesideAGate(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	openQuestion(t, f, "S02")

	inbox, err := f.lister.LoadInbox(f.repo)
	if err != nil {
		t.Fatalf("LoadInbox: %v", err)
	}
	if len(inbox.Gates) != 2 {
		t.Fatalf("inbox has %d gate(s), want the consent gate and the question", len(inbox.Gates))
	}
	var asks int
	for _, g := range inbox.Gates {
		if g.Gate.Asks() {
			asks++
			if g.Gate.Prompt == "" {
				t.Error("the question reached the inbox without its question")
			}
		}
	}
	if asks != 1 {
		t.Fatalf("%d of the rows ask something, want exactly 1", asks)
	}
}

// TestAnswerGate_WritesTheWordsAndUnparks: the verb, through ops, with the
// injected clock — the same guards DecideGate has, because it is the same path.
func TestAnswerGate_WritesTheWordsAndUnparks(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	openQuestion(t, f, "S02")

	answered, err := f.lister.AnswerGate(f.runID, "S02", nil, "  a réplica de leitura  ")
	if err != nil {
		t.Fatalf("AnswerGate: %v", err)
	}
	if answered.Decision.Answer != "a réplica de leitura" {
		t.Errorf("answer = %q, want it trimmed", answered.Decision.Answer)
	}
	if answered.Decision.DecidedAt != f.now {
		t.Errorf("decided_at = %v, want the injected clock %v", answered.Decision.DecidedAt, f.now)
	}
	on, err := gate.Read(f.repo, f.runID, "S02")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !on.Decided() || on.Decision.Answer == "" {
		t.Fatalf("the answer did not reach disk: %+v", on.Decision)
	}

	// And the run that was parked on it no longer has it waiting.
	inbox, err := f.lister.LoadInbox(f.repo)
	if err != nil {
		t.Fatalf("LoadInbox: %v", err)
	}
	for _, g := range inbox.Gates {
		if g.Gate.StepID == "S02" {
			t.Error("an answered question is still in the inbox")
		}
	}
}

// TestAnswerGate_RefusesNothing: an empty --text is not an answer, and the
// refusal comes from the disk contract rather than from the CLI — so the UI's
// empty textarea is refused by the same sentence.
func TestAnswerGate_RefusesNothing(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	openQuestion(t, f, "S02")

	if _, err := f.lister.AnswerGate(f.runID, "S02", nil, "   \n\t "); err == nil {
		t.Fatal("whitespace was accepted as an answer")
	}
	after, _ := gate.Read(f.repo, f.runID, "S02")
	if after.Decided() {
		t.Error("a refused answer still reached disk")
	}
}

// TestAnswerGate_RefusesWhenTheRunIsGone: the shared liveness guard covers the
// new verb. Answering a run nobody is running writes words into a file no
// process will ever read, while looking like the step was unblocked.
func TestAnswerGate_RefusesWhenTheRunIsGone(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	openQuestion(t, f, "S02")
	dead := f.resolver
	dead.Alive = func(int) bool { return false }
	lister := GateLister{Resolver: dead, Now: func() time.Time { return f.now }}

	_, err := lister.AnswerGate(f.runID, "S02", nil, "a réplica")
	if err == nil || !strings.Contains(err.Error(), "nothing is waiting") {
		t.Fatalf("AnswerGate on a dead run = %v, want a refusal", err)
	}
}

// TestGateAudit_SeesTheQuestionThroughItsNature is the other half of the
// criterion: the sensor over the sensors needed no case for this. The question
// arrives as a row like any other, its nature travels with it, and the existing
// Nature filter is enough to keep "how long to type an answer" out of a
// distribution of "how long to approve a migration".
func TestGateAudit_SeesTheQuestionThroughItsNature(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	openQuestion(t, f, "S02")
	if _, err := f.lister.AnswerGate(f.runID, "S02", nil, "a réplica de leitura"); err != nil {
		t.Fatalf("AnswerGate: %v", err)
	}

	all, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{})
	if err != nil {
		t.Fatalf("LoadGateAudit: %v", err)
	}
	var row *GateAuditRow
	for i := range all.Rows {
		if all.Rows[i].StepID == "S02" {
			row = &all.Rows[i]
		}
	}
	if row == nil {
		t.Fatal("the answered question is invisible to the audit")
	}
	if row.Nature != types.GateQuestion {
		t.Errorf("nature = %q, want question", row.Nature)
	}
	if row.Verdict != gate.Approved {
		t.Errorf("verdict = %q, want approved — a fourth verdict would break the audit's three buckets", row.Verdict)
	}
	if row.LatencyMs == nil {
		t.Error("the question has no latency; the two stamps on disk are the same ones the audit reads")
	}

	// The answer itself never enters the audit. That surface is handed whole to
	// a browser tab (GET /api/gates/audit), and an answer is free text a person
	// typed about their own systems — the same class as the evidence the audit
	// has always refused to carry.
	if blob, merr := json.Marshal(all); merr == nil && strings.Contains(string(blob), "réplica") {
		t.Errorf("the audit published the answer text:\n%s", blob)
	}

	// The audit carries the LABEL and no free text, exactly as for every other
	// nature: an answer is the person's own words about their systems, and the
	// audit is the surface that is safe to hand to a browser tab.
	humanOnly, err := f.lister.LoadGateAudit(f.repo, GateAuditOptions{Nature: types.GateHuman})
	if err != nil {
		t.Fatalf("LoadGateAudit(human): %v", err)
	}
	for _, r := range humanOnly.Rows {
		if r.Nature == types.GateQuestion {
			t.Error("the nature filter let a question into the human distribution")
		}
	}
}
