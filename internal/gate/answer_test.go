package gate

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

func sampleQuestion(repo string) Pending {
	return Pending{
		RunID: "run_8f21", StepID: "S02", Repo: repo,
		Project: "demo", Recipe: "feature-pipeline",
		Nature: types.GateQuestion, Label: "Qual base",
		Prompt: "Contra qual base o passo deve rodar?", Title: "Rodar a carga",
		OpenedAt: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
	}
}

// TestAnswerRecordsTheWords: the axis inverted, end to end on disk. The run
// asked, somebody replied, and the reply survives the process that wrote it —
// which is the only property that makes this a gate rather than a prompt.
func TestAnswerRecordsTheWords(t *testing.T) {
	repo := t.TempDir()
	q := sampleQuestion(repo)
	if err := Open(q); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Decide(repo, q.RunID, q.StepID, Decision{
		Verdict: Approved, DecidedAt: time.Now().UTC(), Answer: "a réplica de leitura",
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	after, err := Read(repo, q.RunID, q.StepID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !after.Decided() || after.Decision.Answer != "a réplica de leitura" {
		t.Fatalf("the answer did not reach disk: %+v", after.Decision)
	}
	if after.Decision.Verdict != Approved {
		t.Fatalf("verdict = %q, want approved: answering means the run continues", after.Decision.Verdict)
	}
}

// TestApprovalWithoutWordsSplitsOnTheNature is the positive/negative control for
// the whole design: the SAME decision — approved, no answer — must pass on a
// gate that asked for consent and fail on a gate that asked a question.
//
// Without the negative half, a `gate approve` on a question would unpark the run
// with an empty answer and the step would proceed as though it had been told
// something. That is the failure this verb exists to prevent, and it is invisible
// to any test that only checks the happy path.
func TestApprovalWithoutWordsSplitsOnTheNature(t *testing.T) {
	repo := t.TempDir()

	consent := samplePending(repo)
	consent.Evidence = nil // the reading lock is a different rule; keep this about the answer
	if err := Open(consent); err != nil {
		t.Fatalf("Open (human): %v", err)
	}
	if _, err := Decide(repo, consent.RunID, consent.StepID, Decision{
		Verdict: Approved, DecidedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("positive control: approving a human gate with no words failed: %v", err)
	}

	q := sampleQuestion(repo)
	if err := Open(q); err != nil {
		t.Fatalf("Open (question): %v", err)
	}
	_, err := Decide(repo, q.RunID, q.StepID, Decision{
		Verdict: Approved, DecidedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("negative control: a question was approved with no answer — the run would resume with nothing")
	}
	if !strings.Contains(err.Error(), "gate answer") {
		t.Errorf("the refusal does not name the verb that works: %v", err)
	}
	after, _ := Read(repo, q.RunID, q.StepID)
	if after.Decided() {
		t.Fatal("the refused approval still reached disk")
	}
}

// TestWordsAreRefusedOnAGateThatDidNotAsk is the other direction: free text on a
// human gate would sit in a field nothing renders for that nature, which is how
// a record silently loses information a person believed they had left.
func TestWordsAreRefusedOnAGateThatDidNotAsk(t *testing.T) {
	repo := t.TempDir()
	p := samplePending(repo)
	p.Evidence = nil
	if err := Open(p); err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err := Decide(repo, p.RunID, p.StepID, Decision{
		Verdict: Approved, DecidedAt: time.Now().UTC(), Answer: "quis dizer isto",
	})
	if err == nil {
		t.Fatal("a human gate accepted an answer it never asked for")
	}
}

// TestRefusingToAnswerIsARejection: declining needs no words, and its words go
// in the reason. Expiry takes the same path, which is what keeps the audit's
// three buckets whole.
func TestRefusingToAnswerIsARejection(t *testing.T) {
	repo := t.TempDir()
	q := sampleQuestion(repo)
	if err := Open(q); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Decide(repo, q.RunID, q.StepID, Decision{
		Verdict: Rejected, DecidedAt: time.Now().UTC(), Reason: "não sei",
	}); err != nil {
		t.Fatalf("rejecting a question failed: %v", err)
	}

	other := sampleQuestion(repo)
	other.StepID = "S03"
	if err := Open(other); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Decide(repo, other.RunID, other.StepID, Decision{
		Verdict: Rejected, DecidedAt: time.Now().UTC(), Answer: "isto é uma resposta",
	}); err == nil {
		t.Fatal("a rejection carried an answer: the words belong in the reason")
	}
}

// TestOldGateFileWithoutTheAnswerKey: the format grew a field, and growing is
// only safe if what was written before still reads. A gate file from before this
// change has no `answer` key at all — neither open nor decided — and both have to
// keep working, the decided one for the audit and the open one for the run that
// is still polling it.
func TestOldGateFileWithoutTheAnswerKey(t *testing.T) {
	repo := t.TempDir()
	seed := samplePending(repo)
	seed.Evidence = nil
	if err := Open(seed); err != nil {
		t.Fatalf("Open: %v", err)
	}
	path, err := Path(repo, seed.RunID, seed.StepID)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}

	// Written by hand in the shape the previous version produced: no `answer`,
	// and no `reads` either. Marshalling the current struct would prove nothing,
	// because the current struct is what is under test.
	const old = `{
  "run_id": "run_8f21",
  "step_id": "S06",
  "repo": "REPO",
  "nature": "human",
  "label": "Aprovação de STG",
  "opened_at": "2026-08-17T12:00:00Z"
}`
	if err := os.WriteFile(path, []byte(strings.Replace(old, "REPO", repo, 1)), 0o600); err != nil {
		t.Fatalf("writing the old file: %v", err)
	}

	got, err := Read(repo, seed.RunID, seed.StepID)
	if err != nil {
		t.Fatalf("an old gate file stopped being readable: %v", err)
	}
	if got.Decided() || got.Decision != nil {
		t.Fatalf("old open gate read as decided: %+v", got.Decision)
	}
	if _, err := Decide(repo, seed.RunID, seed.StepID, Decision{
		Verdict: Approved, DecidedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("an old gate could not be decided by the new code: %v", err)
	}

	// And the file a decided gate produces gains no key when nothing was
	// answered: an `answer: ""` on every human gate ever approved would be a
	// format change dressed as a default.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "answer") {
		t.Errorf("a gate that asked nothing wrote an answer key:\n%s", data)
	}

	// The reverse read, for the audit: an old DECIDED gate has a decision with
	// no answer, and that must be an empty string rather than a parse failure.
	var decoded Pending
	const oldDecided = `{"run_id":"run_8f21","step_id":"S06","repo":"x","nature":"human",
	 "opened_at":"2026-08-17T12:00:00Z",
	 "decision":{"verdict":"approved","decided_at":"2026-08-17T13:00:00Z"}}`
	if err := json.Unmarshal([]byte(oldDecided), &decoded); err != nil {
		t.Fatalf("an old decided gate stopped parsing: %v", err)
	}
	if !decoded.Decided() || decoded.Decision.Answer != "" {
		t.Fatalf("old decision decoded wrong: %+v", decoded.Decision)
	}
}
