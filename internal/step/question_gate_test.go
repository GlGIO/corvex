package step

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/types"
)

// waitForGate blocks until the gate file exists, which is the only moment at
// which another process could answer it. Answering before it is written would
// test nothing — the same reason the human-gate tests wait here.
func waitForGate(t *testing.T, repo, runID, stepID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := gate.Read(repo, runID, stepID); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the gate file for %s/%s was never opened", runID, stepID)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestQuestionGate_HoldsTheRunUntilSomebodyWrites is the axis inverted, running:
// a step asks, the run parks on the same file and the same poll the human gate
// uses, and the words a second process writes come back into the step's evidence.
func TestQuestionGate_HoldsTheRunUntilSomebodyWrites(t *testing.T) {
	rec := &recorder{}
	e, r := aiExecutor(t, &mockProvider{}, rec)
	e.gatePoll = time.Millisecond
	r.Identity = RunIdentity{RunID: "run_c0ffee", Repo: t.TempDir(), Project: "p"}

	tk := &types.Task{ID: "S01", Title: "Carregar"}
	g := types.Gate{Nature: types.GateQuestion, Label: "Qual base", Prompt: "contra qual base?"}
	acc := newEvidenceSet()

	done := make(chan error, 1)
	go func() { done <- e.questionGate(context.Background(), r, tk, g, acc) }()

	waitForGate(t, r.Identity.Repo, "run_c0ffee", "S01")
	opened, err := gate.Read(r.Identity.Repo, "run_c0ffee", "S01")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !opened.Asks() {
		t.Fatalf("the file on disk has nature %q, want question — every reader downstream discriminates on it", opened.Nature)
	}
	if opened.Prompt != "contra qual base?" {
		t.Errorf("prompt = %q, want the question the step asked", opened.Prompt)
	}

	if _, err := gate.Decide(r.Identity.Repo, "run_c0ffee", "S01", gate.Decision{
		Verdict: gate.Approved, DecidedAt: time.Now().UTC(), Answer: "a réplica de leitura",
	}); err != nil {
		t.Fatalf("answering: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("an answered question must let the step through, got %v", err)
	}

	// The answer reaches the step's evidence, which is what every later gate on
	// the same step renders. It is the only consumer wired today; the worker's
	// prompt is not one, and that gap is recorded rather than faked.
	var found bool
	for _, ev := range acc.all() {
		if strings.Contains(ev.Content, "a réplica de leitura") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the answer did not reach the step's evidence: %+v", acc.all())
	}
	if !rec.has(event.GatePending) {
		t.Error("a question somebody had to answer must leave a gate_pending line")
	}
	decided := rec.first(t, event.GateDecided)
	if strings.Contains(decided.Message, "réplica") {
		t.Errorf("the answer leaked into the ledger, which is committed: %q", decided.Message)
	}
}

// TestQuestionGate_RefusalFailsTheStep: declining to answer is a rejection, and
// a rejection fails its step exactly like any other. No fourth verdict was
// needed to express "I will not answer this".
func TestQuestionGate_RefusalFailsTheStep(t *testing.T) {
	rec := &recorder{}
	e, r := aiExecutor(t, &mockProvider{}, rec)
	e.gatePoll = time.Millisecond
	r.Identity = RunIdentity{RunID: "run_c0ffee", Repo: t.TempDir(), Project: "p"}

	tk := &types.Task{ID: "S01", Title: "Carregar"}
	g := types.Gate{Nature: types.GateQuestion, Label: "Qual base", Prompt: "contra qual base?"}

	done := make(chan error, 1)
	go func() { done <- e.questionGate(context.Background(), r, tk, g, newEvidenceSet()) }()

	waitForGate(t, r.Identity.Repo, "run_c0ffee", "S01")
	if _, err := gate.Decide(r.Identity.Repo, "run_c0ffee", "S01", gate.Decision{
		Verdict: gate.Rejected, DecidedAt: time.Now().UTC(), Reason: "não sei",
	}); err != nil {
		t.Fatalf("rejecting: %v", err)
	}
	if err := <-done; err == nil {
		t.Fatal("a refused question let the step through")
	}
}

// TestQuestionGate_ApproveGatesDoesNotInventAnAnswer is the negative control for
// the CI switch. `--approve-gates` grants consent by policy, and consent is a
// verdict; there is no answer a runner could invent to a question whose domain
// it does not know. If this ever starts passing without a writer, a fabricated
// value is being fed into a step that asked for a real one.
func TestQuestionGate_ApproveGatesDoesNotInventAnAnswer(t *testing.T) {
	rec := &recorder{}
	e, r := aiExecutor(t, &mockProvider{}, rec)
	e.approveGates = true
	e.gatePoll = time.Millisecond
	r.Identity = RunIdentity{RunID: "run_c0ffee", Repo: t.TempDir(), Project: "p"}

	tk := &types.Task{ID: "S01", Title: "Carregar"}
	g := types.Gate{Nature: types.GateQuestion, Label: "Qual base", Prompt: "contra qual base?"}

	done := make(chan error, 1)
	go func() { done <- e.questionGate(context.Background(), r, tk, g, newEvidenceSet()) }()

	// The gate file existing at all is the proof: under --approve-gates a human
	// gate writes none, because nothing is ever pending.
	waitForGate(t, r.Identity.Repo, "run_c0ffee", "S01")
	select {
	case err := <-done:
		t.Fatalf("the question resolved itself under --approve-gates (err=%v)", err)
	case <-time.After(50 * time.Millisecond):
	}

	if _, err := gate.Decide(r.Identity.Repo, "run_c0ffee", "S01", gate.Decision{
		Verdict: gate.Approved, DecidedAt: time.Now().UTC(), Answer: "a réplica",
	}); err != nil {
		t.Fatalf("answering: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("questionGate: %v", err)
	}
}
