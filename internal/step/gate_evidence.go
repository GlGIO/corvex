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
	// answer is what a person typed at this step's question gate, if it had
	// one. It lives HERE, and not on the Run, because a Run is shared by every
	// task of a run — including the ones executing in parallel — and one
	// story's answer appearing in another story's environment is the kind of
	// bug that would be found months later by somebody reading a diff.
	//
	// At most one: the recipe validator refuses more than one gate that parks
	// the run on a person per stage, because two of them collide on one gate
	// file.
	answer string
	// question is the label of the gate that asked, so a worker prompt can say
	// what was asked as well as what was answered. An answer without its
	// question is a sentence with no subject.
	question string
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

// mark and rewind bracket evidence that belongs to an attempt the step threw
// away: a refused after-gate that sent the worker back must not leave its
// refusal in front of the person who later approves the repaired work.
func (s *evidenceSet) mark() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

func (s *evidenceSet) rewind(n int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < len(s.items) {
		s.items = s.items[:n]
	}
}

// setAnswer records the reply to this step's question gate, and what was asked.
func (s *evidenceSet) setAnswer(question, a string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.question, s.answer = question, a
}

// gateQuestion is what the step asked, for the prompt that carries the answer.
func (s *evidenceSet) gateQuestion() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.question
}

// gateAnswer is what the step's command receives as $CORVEX_GATE_ANSWER.
func (s *evidenceSet) gateAnswer() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.answer
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
	out, err := e.runShellForTask(ctx, r, t, "git diff --stat HEAD")
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
		out, err := e.runShellForTask(ctx, r, t, decl.From)
		item := decl
		item.From = ""
		item.Content = out
		switch {
		case err != nil:
			item.Status = types.EvidenceFail
			item.Content = out + "\n[corvex: `" + decl.From + "` failed: " + err.Error() + "]"
		case strings.TrimSpace(out) == "":
			// Evidence that came back EMPTY says so, in the content, where every
			// reader already looks.
			//
			// Measured on the first real run of a fan-out recipe: the gate that
			// decides whether a feature may be shipped had one piece of required
			// reading, `git diff --stat` with the runner's own paperwork excluded,
			// and the agent had written no product code — so the box a person is
			// forced to acknowledge was BLANK. "I read it and there was nothing"
			// and "the command printed nothing" look identical on a blank box,
			// and the second one is the interesting case.
			//
			// The note goes in the content rather than into a new field for the
			// same reason the truncation note does (internal/gate/evidence.go):
			// a reader who only sees the text still knows. And the status is
			// `warn` rather than `fail` because an empty diff is often the
			// TRUTH — it is unreadable, not wrong.
			item.Status = types.EvidenceWarn
			item.Content = "[corvex: `" + decl.From + "` ran and printed nothing]"
		case item.Status == "":
			// A declared item with no status is neutral, not green: nothing
			// judged it.
			item.Status = types.EvidenceWarn
		}
		acc.add(item)
	}
}
