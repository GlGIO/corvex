package step

import (
	"context"
	"fmt"
	"strings"
	"time"

	charmbraceletlog "github.com/charmbracelet/log"

	"github.com/giovannialves/corvex/internal/event"
	"github.com/giovannialves/corvex/internal/gate"
	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/types"
)

// DefaultGatePoll is how often a parked run re-reads its gate file.
//
// Two seconds, not a file watcher: the wait it is polling is measured in minutes
// or days, so 2s is imperceptible to the person deciding and free for the disk —
// and it costs no dependency and no platform-specific code path. The run is
// asleep in between, so this is not a busy loop.
const DefaultGatePoll = 2 * time.Second

// humanGate blocks the run until somebody decides, from another process.
//
// This replaces the pre-F2 behaviour, which was to abort the run with a message
// telling you to re-run it with --approve-gates. That was never a gate: it threw
// away the work in flight and made approval mean "start over, and this time say
// yes to everything". What a gate has to do is hold.
//
// The protocol is entirely on disk, because the decider is never this process:
//
//  1. write the gate file (prompt + evidence) with O_EXCL;
//  2. set the run record to `parked`, which also appends a snapshot to the
//     global index — that is what lets `corvex gate list` find this gate from
//     another repository without any new global state;
//  3. poll the gate file, with the heartbeat still beating so liveness reads
//     `alive` rather than `stale`: a parked run is up and waiting, and F1 had
//     already written that this is the right answer for `parked`;
//  4. on a decision, record it, return the record to `running`, and continue.
func (e *Executor) humanGate(ctx context.Context, r *Run, t *types.Task, g types.Gate, acc *evidenceSet) error {
	e.emit(event.Event{Type: event.HumanGate, TaskID: t.ID, Phase: event.PhaseGate, Message: gateLabel(g, t.Title)})

	// --approve-gates stays the CI path. Without it a pipeline with a human
	// gate would block until it timed out, which is worse than a documented
	// auto-approval.
	if e.approveGates {
		charmbraceletlog.Info("human gate auto-approved (--approve-gates)", "task", t.ID, "gate", g.Describe())
		// A zero wait is not enough to say "no person was involved": somebody
		// who hits approve in the same second the gate opened also rounds to
		// zero, and duration_ms is `omitempty`, so on disk that zero is
		// indistinguishable from a line that measured nothing. The ledger has
		// no "decided by" field and adding one means adding a key to the user's
		// git history, so the distinction rides where it is already free — in
		// the message, and structurally in the fact that this path emits no
		// gate_pending at all, because no gate file was ever written for
		// anybody to answer. A screen looking for rubber stamps must exclude
		// these rather than average them in at 0s: policy approval is a
		// declared choice, not a fast human.
		e.emit(event.Event{
			Type:    event.GateDecided,
			TaskID:  t.ID,
			Status:  types.StatusPassed,
			Phase:   event.PhaseGate,
			Message: g.Describe() + " (auto-approved by policy, no human waited)",
		})
		return nil
	}

	if r.Identity.Repo == "" || r.Identity.RunID == "" {
		// Without an identity there is no addressable gate file, so nobody
		// could ever approve it. Refusing beats blocking forever.
		return Fatal(fmt.Errorf("task %s: human gate %s cannot be opened — this run has no identity on disk", t.ID, g.Describe()))
	}

	e.resolveDeclared(ctx, r, t, acc)
	pending, err := e.openGate(r, t, g, acc)
	if err != nil {
		return Fatal(fmt.Errorf("task %s: opening human gate: %w", t.ID, err))
	}

	e.emit(event.Event{Type: event.GatePending, TaskID: t.ID, Phase: event.PhaseGate, Message: g.Describe()})
	// Counted, not flagged: see Executor.enterGate. Two gates of the same wave
	// used to un-park each other, and the loser vanished from the inbox.
	leaveGate := e.enterGate()
	defer leaveGate()

	decided, err := e.awaitDecision(ctx, r, t, pending)
	if err != nil {
		return err
	}

	e.emit(event.Event{
		Type:    event.GateDecided,
		TaskID:  t.ID,
		Status:  verdictStatus(decided.Verdict),
		Phase:   event.PhaseGate,
		Message: g.Describe(),
		// The human's clock, not the run's — see humanWaitMs. The run spent
		// this time asleep, which is exactly why it has to be recorded
		// separately from the time it spent working.
		DurationMs: humanWaitMs(pending.OpenedAt, decided, e.now()),
	})
	if decided.Verdict == gate.Approved {
		return nil
	}
	reason := decided.Reason
	if reason == "" {
		reason = string(decided.Verdict)
	}
	return e.gateRefused(t, g, reason)
}

// questionGate parks the run until a person answers it in words — the human
// gate's axis inverted, the run asking instead of proposing.
//
// The barrier is the human gate's, unchanged: the same file under
// `.corvex/runs/gates`, the same O_EXCL claim, the same awaitDecision poll with
// the heartbeat still beating so the run reads `alive` rather than `stale`.
// Nothing new was built to wait, which is the practical half of the argument for
// making the answer a field on gate.Decision instead of a second protocol —
// a sibling type would have needed a second poll loop here too.
//
// # Why --approve-gates does not auto-answer
//
// The CI switch means "consent is granted by policy", and consent is a verdict.
// There is no answer a runner can invent to a question whose domain it does not
// know, and inventing one would feed a fabricated value into a step that asked
// for a real one — worse than blocking, because the run would look like it
// worked. So under --approve-gates a question still waits, and a recipe meant to
// run unattended must not ask one.
//
// # What the answer reaches
//
// The step's evidence, which is what every later gate on the same step renders
// and what `corvex gate show` prints. It does NOT reach the worker's prompt: see
// the debt recorded in .corvex/tasks/rebrand/f7-registro.md, which names the
// file and line where that would have to change.
func (e *Executor) questionGate(ctx context.Context, r *Run, t *types.Task, g types.Gate, acc *evidenceSet) error {
	// The same event the human gate emits, because it means the same thing to
	// every reader: the run is parked on a person. Its NAME predates this axis,
	// so the plain renderer prints "human-gate" for a question too
	// (cmd/plain_renderer.go:108). Minting a second event type would widen the
	// ledger's vocabulary — activity.jsonl is committed and internal/activity
	// buckets by type — which is a change to the user's git history, not a
	// rendering fix. Recorded rather than done.
	e.emit(event.Event{Type: event.HumanGate, TaskID: t.ID, Phase: event.PhaseGate, Message: gateLabel(g, t.Title)})

	if r.Identity.Repo == "" || r.Identity.RunID == "" {
		return Fatal(fmt.Errorf("task %s: question %s cannot be opened — this run has no identity on disk", t.ID, g.Describe()))
	}

	e.resolveDeclared(ctx, r, t, acc)
	pending, err := e.openGate(r, t, g, acc)
	if err != nil {
		return Fatal(fmt.Errorf("task %s: opening question: %w", t.ID, err))
	}

	e.emit(event.Event{Type: event.GatePending, TaskID: t.ID, Phase: event.PhaseGate, Message: g.Describe()})
	leaveGate := e.enterGate()
	defer leaveGate()

	decided, err := e.awaitDecision(ctx, r, t, pending)
	if err != nil {
		return err
	}

	e.emit(event.Event{
		Type:   event.GateDecided,
		TaskID: t.ID,
		Status: verdictStatus(decided.Verdict),
		Phase:  event.PhaseGate,
		// The label, never the answer. activity.jsonl is committed by
		// auto_commit, and an answer is free text a person typed about their own
		// systems — the same reason evidence content has never been in there.
		Message:    g.Describe(),
		DurationMs: humanWaitMs(pending.OpenedAt, decided, e.now()),
	})
	if decided.Verdict != gate.Approved {
		reason := decided.Reason
		if reason == "" {
			reason = string(decided.Verdict)
		}
		return e.gateRefused(t, g, reason)
	}
	acc.add(gate.Note(gateLabel(g, "question"), decided.Answer))
	// And the answer reaches the WORK, not only the screen.
	//
	// The type's own contract says approving a question means "continue, and
	// here is the answer". Measured before this line existed: a recipe asked
	// "which branch should receive this PR?", a person typed `release/1.8.0`,
	// the run resumed — and the step ran exactly the command it would have run
	// without asking. The answer was evidence a reader could see and the step
	// could not use, which makes a question a note with extra steps.
	//
	// It travels in the context rather than on the task: it is scoped to this
	// execution, it dies with the call, and it never touches tasks.md — an
	// answer is free text a person typed about their own systems, and the same
	// argument that keeps it out of the committed ledger keeps it out of the
	// committed task file.
	//
	// Unambiguous by construction: the recipe validator refuses more than one
	// gate that parks a run on a person per stage (they would collide on one
	// gate file), so a stage has at most one answer.
	acc.setAnswer(questionText(g), decided.Answer)
	return nil
}

// questionText is what the run asked, in the words the recipe wrote: the prompt
// when there is one, the label otherwise. A worker that receives an answer
// without the question has to guess what it answers.
func questionText(g types.Gate) string {
	if q := strings.TrimSpace(g.Prompt); q != "" {
		return q
	}
	return strings.TrimSpace(g.Label)
}

func verdictStatus(v gate.Verdict) types.TaskStatus {
	if v == gate.Approved {
		return types.StatusPassed
	}
	return types.StatusFailed
}

// openGate writes the pending gate, evidence and all.
func (e *Executor) openGate(r *Run, t *types.Task, g types.Gate, acc *evidenceSet) (gate.Pending, error) {
	p := gate.Pending{
		RunID:   r.Identity.RunID,
		StepID:  t.ID,
		Repo:    r.Identity.Repo,
		Project: r.Identity.Project,
		Recipe:  r.Identity.Recipe,
		// The gate's own nature, not a constant: openGate serves both the human
		// gate and the question, and the nature is what tells every reader
		// downstream — the inbox, `gate show`, the audit — which of the two it
		// is looking at.
		Nature:   g.Nature,
		Label:    gateLabel(g, t.Title),
		Prompt:   g.Prompt,
		Title:    t.Title,
		Evidence: gate.CapAll(acc.all()),
		OpenedAt: e.now(),
	}
	if g.ExpiresAfter != "" {
		if d, err := time.ParseDuration(g.ExpiresAfter); err == nil {
			deadline := p.OpenedAt.Add(d)
			p.ExpiresAt = &deadline
		}
	}
	return p, gate.Open(p)
}

// awaitDecision polls until somebody answers, the deadline passes, or the run is
// cancelled.
func (e *Executor) awaitDecision(ctx context.Context, r *Run, t *types.Task, p gate.Pending) (gate.Decision, error) {
	interval := e.gatePoll
	if interval <= 0 {
		interval = DefaultGatePoll
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		cur, err := gate.Read(r.Identity.Repo, r.Identity.RunID, t.ID)
		if err == nil {
			if cur.Decided() {
				return *cur.Decision, nil
			}
			if cur.Expired(e.now()) {
				// Expiry is a rejection with a reason, never an approval — an
				// unattended gate that lets the work through is a rubber stamp
				// on a timer.
				d := gate.Decision{Verdict: gate.Expired, DecidedAt: e.now(), Reason: "no decision before expires_after"}
				if _, derr := gate.Decide(r.Identity.Repo, r.Identity.RunID, t.ID, d); derr != nil {
					charmbraceletlog.Warn("recording gate expiry", "task", t.ID, "err", derr)
				}
				return d, nil
			}
		} else {
			// A gate file that vanished mid-wait is somebody cleaning scratch
			// under a live run. Nothing good can come of continuing to wait for
			// a file nobody will write.
			return gate.Decision{}, Fatal(fmt.Errorf("task %s: gate file became unreadable while waiting: %w", t.ID, err))
		}

		select {
		case <-ctx.Done():
			return gate.Decision{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// setRunStatus reports the run's own state to whatever is supervising it. A
// failure here is warned, never fatal: losing the `parked` bit costs a listing
// its accuracy, and refusing to hold the gate over it would be worse.
func (e *Executor) setRunStatus(s run.Status) {
	if e.setStatus == nil {
		return
	}
	if err := e.setStatus(s); err != nil {
		charmbraceletlog.Warn("updating run status", "status", s, "err", err)
	}
}

func (e *Executor) now() time.Time {
	if e.nowFn != nil {
		return e.nowFn()
	}
	return time.Now().UTC()
}
