package step

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

// TestReviewer_ATreeTheJudgeEditedVoidsTheVerdict: the reviewer runs with Bash,
// so nothing but this check stops it from "fixing" the work and then passing it.
func TestReviewer_ATreeTheJudgeEditedVoidsTheVerdict(t *testing.T) {
	dir := t.TempDir()
	gitInitForRecovery(t, dir)
	// The worker's change is on disk, uncommitted, before the judge arrives.
	if err := os.WriteFile(filepath.Join(dir, "work.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &mockProvider{executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
		_ = os.WriteFile(filepath.Join(req.WorkDir, "work.go"), []byte("package x // fixed by the judge\n"), 0o644)
		return &types.ExecuteResult{Output: "VERDICT: PASS", CostUSD: 0.42}, nil
	}}
	res, err := NewReviewer(p, "m", dir, "").Review(context.Background(), &types.Task{ID: "S01"})
	if err == nil || !strings.Contains(err.Error(), "changed the working tree") {
		t.Fatalf("Review = %v, want the verdict discarded because the tree moved", err)
	}
	if res == nil || res.CostUSD != 0.42 {
		t.Errorf("the discarded call's spend must still reach the caller, got %+v", res)
	}
}

// TestReviewer_ReadingAndIgnoredLeftoversAreNotEdits: the false positive the
// check must not have — a judge that runs the tests leaves ignored artefacts
// behind, and that is reading, not editing.
func TestReviewer_ReadingAndIgnoredLeftoversAreNotEdits(t *testing.T) {
	dir := t.TempDir()
	gitInitForRecovery(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("coverage.out\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &mockProvider{executeFn: func(_ context.Context, req types.ExecuteRequest) (*types.ExecuteResult, error) {
		_ = os.WriteFile(filepath.Join(req.WorkDir, "coverage.out"), []byte("mode: set\n"), 0o644)
		return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
	}}
	res, err := NewReviewer(p, "m", dir, "").Review(context.Background(), &types.Task{ID: "S01"})
	if err != nil {
		t.Fatalf("Review = %v, want a clean pass", err)
	}
	if res.Verdict != VerdictPass {
		t.Errorf("verdict = %s, want PASS", res.Verdict)
	}
}

// TestTreeState_NotARepoMakesNoClaim: outside a work tree there is nothing to
// compare, and an empty fingerprint is how that is said.
func TestTreeState_NotARepoMakesNoClaim(t *testing.T) {
	if got := treeState(context.Background(), t.TempDir()); got != "" {
		t.Errorf("treeState outside a repo = %q, want empty", got)
	}
}

// TestReviewer_TheRunsOwnBookkeepingIsNotAnEdit: the ledger and tasks.md live in
// the tree and the runner writes them while the reviewer works. Measured: before
// this exclusion every characterised run in cmd/ discarded a clean PASS.
func TestReviewer_TheRunsOwnBookkeepingIsNotAnEdit(t *testing.T) {
	dir := t.TempDir()
	gitInitForRecovery(t, dir)
	state := filepath.Join(dir, ".corvex", "tasks", "p")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &mockProvider{executeFn: func(_ context.Context, _ types.ExecuteRequest) (*types.ExecuteResult, error) {
		_ = os.WriteFile(filepath.Join(state, "activity.jsonl"), []byte("{}\n"), 0o644)
		return &types.ExecuteResult{Output: "VERDICT: PASS"}, nil
	}}
	if _, err := NewReviewer(p, "m", dir, "").Review(context.Background(), &types.Task{ID: "S01"}); err != nil {
		t.Fatalf("Review = %v, want a clean pass: the runner's ledger is not the judge's edit", err)
	}
}
