package ops

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/types"
)

// gateFixture builds a repository holding one parked run with one open gate, and
// a resolver pointed at a scratch home. Everything is injected: a test that
// reads the real $HOME is a defect, not an inconvenience.
type gateFixture struct {
	repo     string
	home     string
	runID    string
	now      time.Time
	lister   GateLister
	resolver run.Resolver
}

func newGateFixture(t *testing.T, status run.Status, evidence []types.Evidence) gateFixture {
	t.Helper()
	repo := t.TempDir()
	home := t.TempDir()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	const runID = "run_8f21"

	reg := run.Registry{
		Repo:    repo,
		Home:    home,
		NewID:   func() (string, error) { return runID, nil },
		Now:     func() time.Time { return now },
		Machine: "test-machine",
	}
	h, err := reg.Start(run.StartOptions{Project: "demo", Recipe: "gate-demo"})
	if err != nil {
		t.Fatalf("registering the run: %v", err)
	}
	if err := h.SetStatus(status); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	if err := gate.Open(gate.Pending{
		RunID: runID, StepID: "S01", Repo: repo, Project: "demo", Recipe: "gate-demo",
		Nature: types.GateHuman, Label: "Aprovação de STG", Prompt: "Aplicar em STG?",
		Title: "Aplicar migration", Evidence: evidence,
		OpenedAt: now.Add(-3 * time.Hour),
	}); err != nil {
		t.Fatalf("opening the gate: %v", err)
	}

	resolver := run.Resolver{
		Home:    home,
		Now:     func() time.Time { return now },
		Alive:   func(int) bool { return true },
		Machine: "test-machine",
	}
	return gateFixture{
		repo: repo, home: home, runID: runID, now: now, resolver: resolver,
		lister: GateLister{Resolver: resolver, Now: func() time.Time { return now }},
	}
}

func requiredEvidence() []types.Evidence {
	return []types.Evidence{
		{Kind: types.EvidenceSQL, Label: "Migration", RequiredReading: true, Content: "alter table users add x int;"},
		{Kind: types.EvidenceTestOutput, Label: "Testes", Status: types.EvidencePass, Content: "312/312"},
	}
}

// TestListGates_FindsParkedRunsThroughTheGlobalIndex is the cross-repository
// inbox, and the point is what it does *not* need: no gate registry in
// $CORVEX_HOME. A parked run appends a snapshot to the index because SetStatus
// does, and the index is the only file that sees other repositories.
func TestListGates_FindsParkedRunsThroughTheGlobalIndex(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	gates, err := f.lister.ListGates()
	if err != nil {
		t.Fatalf("ListGates: %v", err)
	}
	if len(gates) != 1 {
		t.Fatalf("ListGates returned %d, want 1", len(gates))
	}
	g := gates[0]
	if g.Gate.RunID != f.runID || g.Gate.StepID != "S01" {
		t.Errorf("wrong gate: %+v", g.Gate)
	}
	if g.Waiting != 3*time.Hour {
		t.Errorf("waiting = %v, want 3h — a gate parked for days has to be able to shout", g.Waiting)
	}
	if g.Liveness != run.LivenessAlive {
		t.Errorf("liveness = %s, want alive: a parked run is up and waiting", g.Liveness)
	}
}

// TestListGates_IgnoresRunsThatAreNotParked keeps the inbox to what is actually
// blocked on a person.
func TestListGates_IgnoresRunsThatAreNotParked(t *testing.T) {
	f := newGateFixture(t, run.StatusRunning, requiredEvidence())
	gates, err := f.lister.ListGates()
	if err != nil {
		t.Fatalf("ListGates: %v", err)
	}
	if len(gates) != 0 {
		t.Fatalf("ListGates returned %d for a running run, want 0", len(gates))
	}
}

func TestFindGate_InfersTheStepWhenThereIsOnlyOne(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	got, err := f.lister.FindGate(f.runID, "")
	if err != nil {
		t.Fatalf("FindGate: %v", err)
	}
	if got.Gate.StepID != "S01" {
		t.Errorf("inferred step %q, want S01", got.Gate.StepID)
	}
}

// TestFindGate_RefusesToGuessBetweenSeveral: approving the wrong migration
// because the tool guessed is the exact failure this phase exists to prevent.
func TestFindGate_RefusesToGuessBetweenSeveral(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	if err := gate.Open(gate.Pending{
		RunID: f.runID, StepID: "S02", Repo: f.repo, Nature: types.GateHuman,
		Label: "Merge", OpenedAt: f.now,
	}); err != nil {
		t.Fatalf("opening a second gate: %v", err)
	}
	_, err := f.lister.FindGate(f.runID, "")
	if err == nil || !strings.Contains(err.Error(), "--step") {
		t.Fatalf("FindGate = %v, want a refusal listing the choices", err)
	}
	if !strings.Contains(err.Error(), "S01") || !strings.Contains(err.Error(), "S02") {
		t.Errorf("the refusal does not list both steps: %v", err)
	}
}

func TestFindGate_UnknownRunAndStep(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	var unknown *UnknownGateError
	if _, err := f.lister.FindGate("run_dead", ""); !errors.As(err, &unknown) {
		t.Fatalf("unknown run = %v, want *UnknownGateError", err)
	}
	if _, err := f.lister.FindGate(f.runID, "S99"); !errors.As(err, &unknown) {
		t.Fatalf("unknown step = %v, want *UnknownGateError", err)
	}
}

func TestDecideGate_ApprovalNeedsTheAcknowledgement(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	_, err := f.lister.DecideGate(f.runID, "S01", gate.Approved, nil, "")
	var unread *gate.UnreadError
	if !errors.As(err, &unread) {
		t.Fatalf("approve without --ack = %v, want *gate.UnreadError", err)
	}
	if _, err := f.lister.DecideGate(f.runID, "S01", gate.Approved, []string{"Migration"}, ""); err != nil {
		t.Fatalf("approve with --ack = %v, want nil", err)
	}
	after, err := gate.Read(f.repo, f.runID, "S01")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !after.Decided() || after.Decision.Verdict != gate.Approved {
		t.Fatalf("decision not on disk: %+v", after.Decision)
	}
	if after.Decision.DecidedAt != f.now {
		t.Errorf("decided_at = %v, want the injected clock %v", after.Decision.DecidedAt, f.now)
	}
}

// TestDecideGate_RefusesWhenTheRunIsGone: approving a gate nobody will read is
// worse than an error, because it looks like the pipeline advanced.
func TestDecideGate_RefusesWhenTheRunIsGone(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, requiredEvidence())
	dead := f.resolver
	dead.Alive = func(int) bool { return false }
	lister := GateLister{Resolver: dead, Now: func() time.Time { return f.now }}

	_, err := lister.DecideGate(f.runID, "S01", gate.Approved, []string{"Migration"}, "")
	if err == nil || !strings.Contains(err.Error(), "nothing is waiting") {
		t.Fatalf("DecideGate on a dead run = %v, want a refusal", err)
	}
	after, _ := gate.Read(f.repo, f.runID, "S01")
	if after.Decided() {
		t.Error("a refused decision must not be written")
	}
}

// TestGateFilesNeverReachTheVersionedTree is the leak guard for evidence: it
// lives under the directory F1 gitignores with `*`, and nothing about it goes
// anywhere a commit can find.
func TestGateFilesNeverReachTheVersionedTree(t *testing.T) {
	f := newGateFixture(t, run.StatusParked, []types.Evidence{
		{Kind: types.EvidenceTestOutput, Label: "Testes", RequiredReading: true,
			Content: "panic: boom\n\t/Users/someone/secret-project/main.go:42\nANTHROPIC_API_KEY=sk-ant-CANARY111"},
	})
	path, err := gate.Path(f.repo, f.runID, "S01")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	// The evidence really does hold the dangerous string — otherwise this test
	// proves nothing.
	got, err := gate.Read(f.repo, f.runID, "S01")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(got.Evidence[0].Content, "CANARY111") {
		t.Fatal("the canary is not in the evidence; this test would pass vacuously")
	}
	guard := filepath.Join(f.repo, ".corvex", "runs", ".gitignore")
	if _, statErr := gate.Path(f.repo, f.runID, "S01"); statErr != nil {
		t.Fatal(statErr)
	}
	if !strings.HasPrefix(path, filepath.Dir(guard)) {
		t.Fatalf("gate file %q is not under the ignored directory %q", path, filepath.Dir(guard))
	}
}
