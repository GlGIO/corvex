package step

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/types"
)

// TestTwoGatesOnAPersonCollideOnOneFile records what the runner does, not what
// it should do: it is the measurement the validator's refusal rests on.
//
// One step, two gates that wait on a person. The first opens its file, a person
// approves it from outside, the step's work runs — and then the second gate
// calls the same gate.Open on the same (run id, step id) path, gets `file
// exists` back from O_EXCL, and step.Fatal kills the entire run. The damage is
// maximal precisely because everything before it worked: somebody spent a real
// decision on the first gate and the step did its work, and all of it is thrown
// away at the second door.
//
// It is not fixed here. Making the runner survive this means giving the gate
// file a second key, and that is an on-disk format change to files already
// written on users' machines — see the note on validateOnePersonGate in
// internal/recipe/validate_stage.go. What the fix does instead is make
// `recipe validate` refuse the recipe, so no run ever reaches this line.
func TestTwoGatesOnAPersonCollideOnOneFile(t *testing.T) {
	cases := map[string][]types.Gate{
		"human before, human after": {
			{Nature: types.GateHuman, When: types.GateBefore, Label: "antes"},
			{Nature: types.GateHuman, When: types.GateAfter, Label: "depois"},
		},
		// `when` is not a separator: both positions run runGates against the
		// same task, so the path is identical either way.
		"both human before": {
			{Nature: types.GateHuman, When: types.GateBefore, Label: "antes"},
			{Nature: types.GateHuman, When: types.GateBefore, Label: "tambem antes"},
		},
		// The question is the human gate's axis inverted and goes through the
		// same openGate, so it collides with a human gate exactly as hard.
		"human before, question after": {
			{Nature: types.GateHuman, When: types.GateBefore, Label: "antes"},
			{Nature: types.GateQuestion, When: types.GateAfter, Label: "depois", Prompt: "contra qual base?"},
		},
	}

	for name, gates := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			e, r := aiExecutor(t, &mockProvider{}, rec)
			e.gatePoll = time.Millisecond
			repo := t.TempDir()
			r.Identity = RunIdentity{RunID: "run_c0ffee", Repo: repo, Project: "p"}

			tk := &types.Task{ID: "S01", Title: "Migrar", Kind: "tool", Command: "true", Gates: gates}

			done := make(chan error, 1)
			go func() { done <- e.Execute(context.Background(), r, tk) }()

			// A person answers the first gate, from another process, exactly as
			// `corvex gate approve` would.
			waitForGate(t, repo, "run_c0ffee", "S01")
			if _, err := gate.Decide(repo, "run_c0ffee", "S01", gate.Decision{
				Verdict: gate.Approved, DecidedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("approving the first gate: %v", err)
			}

			select {
			case err := <-done:
				if err == nil {
					t.Fatal("two gates on one step no longer collide — if the gate file gained a second key, " +
						"validateOnePersonGate in internal/recipe is now refusing recipes the runner can execute")
				}
				if !strings.Contains(err.Error(), "file exists") {
					t.Fatalf("the second gate failed for some other reason: %v", err)
				}
				if !IsFatal(err) {
					t.Errorf("the collision is fatal today (it kills the run); got a task-level error: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the run hung instead of failing")
			}
		})
	}
}
