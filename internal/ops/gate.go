package ops

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/run"
)

// GateView is one row of the gate inbox: the gate, plus enough of the run to
// decide whether answering it can still do anything.
type GateView struct {
	Gate     gate.Pending `json:"gate"`
	Liveness run.Liveness `json:"liveness"`
	// Waiting is how long the gate has been open. The screen that matters most
	// is "what is blocked on me", and a gate parked for three days should be
	// able to shout.
	Waiting time.Duration `json:"waiting_ns"`
}

// GateLister finds gates. The resolver is injectable for the same reason it is
// everywhere else in this codebase: a test must never read the real $HOME.
type GateLister struct {
	Resolver run.Resolver
	Now      func() time.Time
}

func (g GateLister) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now().UTC()
}

// ListGates answers "what is waiting for me, everywhere", across repositories.
//
// It costs no new global state, which is the whole point: a run that parks calls
// SetStatus, SetStatus appends a snapshot to the global index, and the index is
// the one file that sees runs in other repositories. So the inbox is: read the
// index, keep the parked runs, and open each one's gate directory in its own
// repo. Nothing about gates lives in $CORVEX_HOME.
//
// A repository that has been deleted or moved simply contributes nothing rather
// than failing the listing — one dead repo must not hide every live gate.
func (g GateLister) ListGates() ([]GateView, error) {
	views, err := g.Resolver.List()
	if err != nil {
		return nil, err
	}
	now := g.now()
	seen := make(map[string]bool, len(views))
	out := make([]GateView, 0, len(views))
	for _, v := range views {
		if v.Record.Status != run.StatusParked || seen[v.Record.Repo+"\x00"+v.Record.RunID] {
			continue
		}
		seen[v.Record.Repo+"\x00"+v.Record.RunID] = true
		pendings, gerr := gate.ListOpen(v.Record.Repo)
		if gerr != nil {
			continue
		}
		for _, p := range pendings {
			if p.RunID != v.Record.RunID {
				continue
			}
			out = append(out, GateView{Gate: p, Liveness: v.Liveness, Waiting: now.Sub(p.OpenedAt)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Waiting > out[j].Waiting })
	return out, nil
}

// UnknownGateError reports a gate that could not be addressed, with the facts a
// caller needs to explain it.
type UnknownGateError struct {
	RunID  string
	StepID string
	Reason string
}

func (e *UnknownGateError) Error() string {
	if e.StepID == "" {
		return fmt.Sprintf("run %s: %s", e.RunID, e.Reason)
	}
	return fmt.Sprintf("gate %s --step %s: %s", e.RunID, e.StepID, e.Reason)
}

// FindGate resolves a run id to its repository and reads one gate.
//
// The step id is optional: with exactly one gate open, naming it is ceremony.
// With more than one it is required, because approving the wrong migration
// because the tool guessed is the failure this whole phase exists to prevent.
func (g GateLister) FindGate(runID, stepID string) (GateView, error) {
	view, ok, err := g.Resolver.Get(runID)
	if err != nil {
		return GateView{}, err
	}
	if !ok {
		return GateView{}, &UnknownGateError{RunID: runID, StepID: stepID, Reason: "no such run in the index"}
	}
	if stepID != "" {
		p, rerr := gate.Read(view.Record.Repo, runID, stepID)
		if rerr != nil {
			return GateView{}, &UnknownGateError{RunID: runID, StepID: stepID, Reason: "no gate recorded for that step"}
		}
		return GateView{Gate: p, Liveness: view.Liveness, Waiting: g.now().Sub(p.OpenedAt)}, nil
	}

	open, rerr := gate.ListOpen(view.Record.Repo)
	if rerr != nil {
		return GateView{}, rerr
	}
	mine := open[:0:0]
	for _, p := range open {
		if p.RunID == runID {
			mine = append(mine, p)
		}
	}
	switch len(mine) {
	case 0:
		return GateView{}, &UnknownGateError{RunID: runID, Reason: "no gate is waiting on this run"}
	case 1:
		return GateView{Gate: mine[0], Liveness: view.Liveness, Waiting: g.now().Sub(mine[0].OpenedAt)}, nil
	default:
		ids := make([]string, 0, len(mine))
		for _, p := range mine {
			ids = append(ids, p.StepID)
		}
		return GateView{}, &UnknownGateError{
			RunID:  runID,
			Reason: "several gates are waiting; name one with --step " + strings.Join(ids, " | "),
		}
	}
}

// DecideGate records an answer to a gate.
//
// It refuses to answer a gate whose run is no longer up. Approving a gate that
// nobody will read is worse than an error: it looks like the pipeline advanced.
// The liveness read carries F1's documented pid-reuse window, and the failure
// mode that window produces here is benign — the worst case is refusing an
// answer that was valid, which the user simply repeats.
func (g GateLister) DecideGate(runID, stepID string, verdict gate.Verdict, acked []string, reason string) (gate.Pending, error) {
	return g.write(runID, stepID, gate.Decision{
		Verdict: verdict,
		Acked:   acked,
		Reason:  reason,
	})
}

// AnswerGate records what a person wrote back to a gate that asked.
//
// It is the same act as DecideGate with the same guards — a dead run cannot read
// an answer any more than it can read an approval — and deliberately NOT a
// second path to disk: both go through gate.Decide, which is where the rule that
// a question needs words and an approval does not lives. What is different here
// is only the shape of what is written, which is why the verdict is not a
// parameter: `answer` means "continue, with this", and declining to answer is
// `gate reject`, the verb that already means "this step does not proceed".
//
// Empty text is refused by gate.Decide rather than here, so the same refusal
// reaches somebody typing it in a terminal and somebody posting an empty
// textarea from the UI.
//
// acked is carried for the same reason approve carries it: a recipe may declare
// required_reading evidence on the step that asks, and a lock the answering verb
// could not satisfy would be a dead end rather than a control — the person would
// be told to read something with no command that records having read it.
func (g GateLister) AnswerGate(runID, stepID string, acked []string, text string) (gate.Pending, error) {
	return g.write(runID, stepID, gate.Decision{
		Verdict: gate.Approved,
		Acked:   acked,
		Answer:  strings.TrimSpace(text),
	})
}

// write resolves the gate, refuses a run that is no longer up, and stamps the
// decision. Shared so the liveness rule cannot come to differ between deciding
// and answering — the two surfaces would then disagree about which gates are
// still addressable.
func (g GateLister) write(runID, stepID string, d gate.Decision) (gate.Pending, error) {
	view, err := g.FindGate(runID, stepID)
	if err != nil {
		return gate.Pending{}, err
	}
	if view.Liveness != run.LivenessAlive {
		return gate.Pending{}, fmt.Errorf("run %s is %s, so nothing is waiting to read this decision "+
			"(the gate file stays on disk; re-run to reach the gate again)", runID, view.Liveness)
	}
	d.DecidedAt = g.now()
	return gate.Decide(view.Gate.Repo, view.Gate.RunID, view.Gate.StepID, d)
}
