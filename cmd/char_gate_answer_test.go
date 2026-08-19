package cmd

import (
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/types"
)

// Characterization for `gate answer`, the verb that closes the axis F2 left open
// ("o eixo 'o agente pergunta' não tem evento em disco"). New goldens rather
// than rewrites: a consent gate must still print exactly what it printed before
// this existed, and the only existing golden that moves is `gate_help`, which
// cannot not move when a subcommand is added — the same rule `gate ack` followed.

// seedQuestion opens a question on the run seedGate already registered, at a
// second step. Two gates on one run is the honest shape here: the question does
// not replace the consent gate, it sits beside it in the same directory and in
// the same inbox, which is the whole argument for it being a field on the
// existing format rather than a format of its own.
func seedQuestion(t *testing.T, repo, runID string) {
	t.Helper()
	if err := gate.Open(gate.Pending{
		RunID: runID, StepID: "S02", Repo: repo, Project: "alpha", Recipe: "gate-demo",
		Nature: types.GateQuestion, Label: "Qual base",
		Prompt: "Contra qual base o passo deve rodar?", Title: "Rodar a carga",
		OpenedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("opening the question: %v", err)
	}
}

func TestCharacterizeGateAnswerHelp(t *testing.T) {
	args := []string{"gate", "answer", "--help"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "gate_answer_help", scrub(transcript(args, stdout, stderr, err)))
}

// TestCharacterizeGateShowQuestion: the screen ends on the verb that works. A
// question that printed `corvex gate approve` would be teaching the one command
// this gate refuses.
func TestCharacterizeGateShowQuestion(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, gateEvidence())
	seedQuestion(t, f.Dir, runID)
	args := []string{"gate", "show", runID, "--step", "S02"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_show_question", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeGateAnswer(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, gateEvidence())
	seedQuestion(t, f.Dir, runID)
	args := []string{"gate", "answer", runID, "--step", "S02", "--text", "a replica de leitura"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_answer", scrub(transcript(args, stdout, stderr, err)))

	// The golden shows what was printed; this shows what was written, which is
	// the half the parked run depends on.
	after, readErr := gate.Read(f.Dir, runID, "S02")
	if readErr != nil {
		t.Fatalf("reading the answered gate: %v", readErr)
	}
	if !after.Decided() || after.Decision.Answer != "a replica de leitura" {
		t.Fatalf("the answer did not reach disk: %+v", after.Decision)
	}
	if after.Decision.Verdict != gate.Approved {
		t.Fatalf("verdict = %q, want approved", after.Decision.Verdict)
	}
	// And the consent gate beside it is untouched: two gates on one run are two
	// files, and answering one must not decide the other.
	consent, readErr := gate.Read(f.Dir, runID, "S01")
	if readErr != nil {
		t.Fatalf("reading the consent gate: %v", readErr)
	}
	if consent.Decided() {
		t.Fatal("answering the question also decided the gate next to it")
	}
}

func TestCharacterizeGateAnswerWithoutText(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, gateEvidence())
	seedQuestion(t, f.Dir, runID)
	args := []string{"gate", "answer", runID, "--step", "S02"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_answer_no_text", scrub(transcript(args, stdout, stderr, err)))
}

// TestCharacterizeGateApproveRefusesAQuestion is the negative control at the
// surface a person actually types. Approving a question would unpark the run
// with an empty answer and the step would proceed as though it had been told
// something — the failure this verb exists to prevent.
func TestCharacterizeGateApproveRefusesAQuestion(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, gateEvidence())
	seedQuestion(t, f.Dir, runID)
	args := []string{"gate", "approve", runID, "--step", "S02"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_approve_question", scrub(transcript(args, stdout, stderr, err)))

	after, readErr := gate.Read(f.Dir, runID, "S02")
	if readErr != nil {
		t.Fatalf("reading the question: %v", readErr)
	}
	if after.Decided() {
		t.Fatal("the refused approval still reached disk")
	}
}

// TestCharacterizeGateAnswerRefusesAConsentGate is the other half of the pair:
// the verbs do not accept each other's gates in either direction.
func TestCharacterizeGateAnswerRefusesAConsentGate(t *testing.T) {
	f := newFixture(t)
	runID := seedGate(t, f.Dir, gateEvidence())
	args := []string{"gate", "answer", runID, "--step", "S01", "--text", "sim"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "gate_answer_on_consent_gate", scrub(transcript(args, stdout, stderr, err)))
}
