package gate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

func samplePending(repo string) Pending {
	return Pending{
		RunID: "run_8f21", StepID: "S06", Repo: repo,
		Project: "demo", Recipe: "feature-pipeline",
		Nature: types.GateHuman, Label: "Aprovação de STG",
		Prompt: "Aplicar esta migration em STG?", Title: "Aplicar migration em STG",
		Evidence: []types.Evidence{
			{Kind: types.EvidenceSQL, Label: "Migration", RequiredReading: true, Content: "alter table users add column x int;"},
			{Kind: types.EvidenceTestOutput, Label: "Testes", Status: types.EvidencePass, Content: "312/312"},
		},
		OpenedAt: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
	}
}

func TestOpenReadDecide(t *testing.T) {
	repo := t.TempDir()
	p := samplePending(repo)
	if err := Open(p); err != nil {
		t.Fatalf("Open: %v", err)
	}

	got, err := Read(repo, p.RunID, p.StepID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Decided() {
		t.Fatal("a freshly opened gate must not be decided")
	}
	if got.Prompt != p.Prompt || len(got.Evidence) != 2 {
		t.Fatalf("round trip lost detail: %+v", got)
	}

	after, err := Decide(repo, p.RunID, p.StepID, Decision{
		Verdict: Approved, DecidedAt: time.Now().UTC(), Acked: []string{"Migration"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !after.Decided() || after.Decision.Verdict != Approved {
		t.Fatalf("decision not recorded: %+v", after.Decision)
	}

	reread, err := Read(repo, p.RunID, p.StepID)
	if err != nil {
		t.Fatalf("Read after decide: %v", err)
	}
	if !reread.Decided() {
		t.Fatal("decision did not reach disk — the whole cross-process contract is this")
	}
}

// TestApproveRefusesUnacknowledgedReading is the approval lock. It is friction
// rather than proof, and it is the friction the gate screen depends on.
func TestApproveRefusesUnacknowledgedReading(t *testing.T) {
	repo := t.TempDir()
	p := samplePending(repo)
	if err := Open(p); err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err := Decide(repo, p.RunID, p.StepID, Decision{Verdict: Approved, DecidedAt: time.Now()})
	var unread *UnreadError
	if !errors.As(err, &unread) {
		t.Fatalf("Decide without acks = %v, want *UnreadError", err)
	}
	if len(unread.Missing) != 1 || unread.Missing[0] != "Migration" {
		t.Fatalf("missing list = %v, want [Migration]", unread.Missing)
	}
	// The gate must still be open: a refused approval decides nothing.
	after, _ := Read(repo, p.RunID, p.StepID)
	if after.Decided() {
		t.Fatal("a refused approval must leave the gate open")
	}
	// Rejecting needs no acknowledgement — you do not have to read the
	// evidence to say no.
	if _, err := Decide(repo, p.RunID, p.StepID, Decision{Verdict: Rejected, DecidedAt: time.Now(), Reason: "nope"}); err != nil {
		t.Fatalf("Decide(reject): %v", err)
	}
}

func TestDecideRefusesSecondAnswer(t *testing.T) {
	repo := t.TempDir()
	p := samplePending(repo)
	if err := Open(p); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Decide(repo, p.RunID, p.StepID, Decision{Verdict: Rejected, DecidedAt: time.Now()}); err != nil {
		t.Fatalf("first Decide: %v", err)
	}
	_, err := Decide(repo, p.RunID, p.StepID, Decision{Verdict: Approved, DecidedAt: time.Now(), Acked: []string{"Migration"}})
	if err == nil || !strings.Contains(err.Error(), "already rejected") {
		t.Fatalf("second Decide = %v, want a refusal naming the first verdict", err)
	}
}

func TestOpenIsExclusive(t *testing.T) {
	repo := t.TempDir()
	p := samplePending(repo)
	if err := Open(p); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := Open(p); err == nil {
		t.Fatal("re-opening the same gate must fail; silently discarding an existing decision is worse")
	}
}

// TestGateFileIsIgnoredScratch is the leak guard, checked structurally: the gate
// directory sits under `.corvex/runs/`, whose `*` .gitignore covers it, and the
// store writes that guard itself rather than assuming a record came first.
func TestGateFileIsIgnoredScratch(t *testing.T) {
	repo := t.TempDir()
	p := samplePending(repo)
	if err := Open(p); err != nil {
		t.Fatalf("Open: %v", err)
	}
	guard := filepath.Join(repo, ".corvex", "runs", ".gitignore")
	data, err := os.ReadFile(guard)
	if err != nil {
		t.Fatalf("gate store did not write the ignore guard: %v", err)
	}
	if strings.TrimSpace(string(data)) != "*" {
		t.Fatalf("ignore guard = %q, want \"*\"", data)
	}
	path, err := Path(repo, p.RunID, p.StepID)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !strings.HasPrefix(path, filepath.Join(repo, ".corvex", "runs")+string(filepath.Separator)) {
		t.Fatalf("gate file %q escaped the ignored scratch directory", path)
	}
}

// TestPathEscapesMintedStepIDs: a fan-out step id contains slashes, which must
// become part of the filename rather than part of the path.
func TestPathEscapesMintedStepIDs(t *testing.T) {
	repo := t.TempDir()
	path, err := Path(repo, "run_8f21", "S06/003/apply")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if filepath.Dir(path) != Dir(repo) {
		t.Fatalf("minted step id escaped into a subdirectory: %q", path)
	}
	if !strings.Contains(filepath.Base(path), "%2F") {
		t.Fatalf("expected the slash to be escaped, got %q", filepath.Base(path))
	}
	// Injective: two ids that differ must never share a file.
	other, _ := Path(repo, "run_8f21", "S06.003.apply")
	if other == path {
		t.Fatal("two distinct step ids collided on one gate file")
	}
}

func TestPathRejectsTraversal(t *testing.T) {
	repo := t.TempDir()
	for _, id := range []string{"../escape", "..", "a b"} {
		if _, err := Path(repo, "run_8f21", id); err == nil {
			t.Errorf("Path accepted step id %q", id)
		}
	}
	if _, err := Path(repo, "not-a-run-id", "S01"); err == nil {
		t.Error("Path accepted a malformed run id")
	}
}

func TestListAndListOpen(t *testing.T) {
	repo := t.TempDir()
	base := samplePending(repo)
	for i, id := range []string{"S01", "S02", "S03"} {
		p := base
		p.StepID = id
		p.OpenedAt = base.OpenedAt.Add(time.Duration(i) * time.Minute)
		if err := Open(p); err != nil {
			t.Fatalf("Open %s: %v", id, err)
		}
	}
	if _, err := Decide(repo, base.RunID, "S02", Decision{Verdict: Rejected, DecidedAt: time.Now()}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	all, err := List(repo)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List returned %d, want 3", len(all))
	}
	if all[0].StepID != "S03" {
		t.Errorf("List is not newest-first: %q", all[0].StepID)
	}

	open, err := ListOpen(repo)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(open) != 2 {
		t.Fatalf("ListOpen returned %d, want 2", len(open))
	}
	for _, p := range open {
		if p.StepID == "S02" {
			t.Error("ListOpen returned a decided gate")
		}
	}
}

func TestListSkipsGarbageAndMissingDir(t *testing.T) {
	repo := t.TempDir()
	if got, err := List(repo); err != nil || got != nil {
		t.Fatalf("List on a repo with no gates = %v, %v; want nil, nil", got, err)
	}
	p := samplePending(repo)
	if err := Open(p); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := os.WriteFile(filepath.Join(Dir(repo), "run_dead-S99.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := List(repo)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].StepID != "S06" {
		t.Fatalf("one mangled file hid the real gates: %+v", got)
	}
}

func TestExpired(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(-time.Minute)
	p := Pending{ExpiresAt: &deadline}
	if !p.Expired(now) {
		t.Error("a gate past its deadline must report expired")
	}
	p.Decision = &Decision{Verdict: Approved}
	if p.Expired(now) {
		t.Error("a decided gate cannot expire")
	}
	if (Pending{}).Expired(now) {
		t.Error("a gate with no deadline never expires — that is the default on purpose")
	}
}
