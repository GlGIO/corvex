package step

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

// TestEvidenceWithoutRequiredReadingRunsAndIsNotCollected records what the
// runner does with an `evidence:` block on a stage whose only gate is
// computational. It is the measurement behind the narrowing of
// validateRequiredReadingHasAReader in internal/recipe.
//
// Two facts, and they are different facts:
//
//  1. The step RUNS. This is the positive control for a user's existing file:
//     Recipe.Compile calls Validate, and `corvex run` compiles through the same
//     door, so a validator that refused this shape would stop a run that works
//     today on a machine whose owner changed nothing.
//  2. The evidence is NOT collected. resolveDeclared is called only by humanGate
//     and questionGate, so the `from:` command never executes — proven here by a
//     `from:` whose only job is to leave a file behind, and does not.
//
// The second fact is why the README calls such a block inert. If a run report is
// ever built that reads declared evidence outside openGate, this test is where
// the change announces itself.
func TestEvidenceWithoutRequiredReadingRunsAndIsNotCollected(t *testing.T) {
	rec := &recorder{}
	e, r := aiExecutor(t, &mockProvider{}, rec)
	repo := t.TempDir()
	r.Identity = RunIdentity{RunID: "run_ev1dnc", Repo: repo, Project: "p"}

	marker := filepath.Join(t.TempDir(), "the-from-ran")
	tk := &types.Task{
		ID: "S01", Title: "Migrar", Kind: "tool", Command: "true",
		Gates: []types.Gate{{Nature: types.GateComputational, Label: "Schema", Command: "true"}},
		Evidence: []types.Evidence{{
			Kind: types.EvidenceDiff, Label: "O que este run mudou",
			From: fmt.Sprintf("touch %q", marker),
		}},
	}

	if err := e.Execute(context.Background(), r, tk); err != nil {
		t.Fatalf("Execute() = %v, want nil: a stage with an inert `evidence:` block is a stage that runs", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the `from:` command ran under a computational gate — if evidence is now collected there, " +
			"the rule in internal/recipe and the README paragraph beside it are both out of date")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", marker, err)
	}
}
