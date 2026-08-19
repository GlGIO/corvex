package step

import (
	"context"
	"strings"
	"sync"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/types"
)

// evidenceSet accumulates what a step produced, in the order it was produced.
//
// One per step execution, never shared: two tasks of the same DAG level run
// concurrently, and their evidence must not interleave. The mutex is for the
// gate producers that may themselves fan out later; today every writer is on the
// step's own goroutine.
type evidenceSet struct {
	mu    sync.Mutex
	items []types.Evidence
}

func newEvidenceSet() *evidenceSet { return &evidenceSet{} }

func (s *evidenceSet) add(e types.Evidence) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, gate.Cap(e))
}

func (s *evidenceSet) all() []types.Evidence {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.Evidence, len(s.items))
	copy(out, s.items)
	return out
}

// addDiffEvidence records what a code step actually changed.
//
// `--stat` rather than the full patch: a gate screen needs to show the shape of
// the change, and a whole diff would blow past the evidence ceiling on any real
// task while being unreadable anyway. Whoever needs the patch has the repo.
//
// A git failure is silent here — the step succeeded, and "we could not summarise
// the diff" is not worth failing a task that passed review.
func (e *Executor) addDiffEvidence(ctx context.Context, r *Run, t *types.Task, acc *evidenceSet) {
	out, err := e.runShell(ctx, r, "git diff --stat HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	acc.add(gate.FromDiff("Diff de "+stageEvidenceLabel(t), out))
}

// resolveDeclared turns the evidence a recipe declared into evidence with
// content, running the `from` command of each item that has one.
//
// A `from` that fails becomes a fail-status item rather than failing the step:
// the evidence exists to inform a decision, and "the command that was supposed
// to show you the query plan did not run" is information the decider needs, not
// a reason to refuse work that has not been judged yet. The gate itself is what
// refuses.
func (e *Executor) resolveDeclared(ctx context.Context, r *Run, t *types.Task, acc *evidenceSet) {
	for _, decl := range t.Evidence {
		if decl.From == "" {
			acc.add(decl)
			continue
		}
		out, err := e.runShell(ctx, r, decl.From)
		item := decl
		item.From = ""
		item.Content = out
		if err != nil {
			item.Status = types.EvidenceFail
			item.Content = out + "\n[corvex: `" + decl.From + "` failed: " + err.Error() + "]"
		} else if item.Status == "" {
			// A declared item with no status is neutral, not green: nothing
			// judged it.
			item.Status = types.EvidenceWarn
		}
		acc.add(item)
	}
}
