package gate

import (
	"fmt"
	"strings"
	"time"

	"github.com/giovannialves/corvex/internal/types"
)

// ReadMark is "somebody opened this evidence, and when".
//
// # Why the timestamp, when the lock only needs the label
//
// Releasing the approval lock needs a set of labels and nothing else. The
// instant is here for the question risk #1 of the roadmap asks instead: a gate
// whose evidence was first seen at 14:03:01 and approved at 14:03:02 is a stamp,
// no matter how correctly the labels were typed. One extra field is what turns
// the lock from a checkbox into something F5's telemetry can measure, and it is
// the only field that can tell a review from a reflex.
//
// # Why the first read wins and a re-read does not move it
//
// The question the sensor asks is "when did this person first face this
// evidence", so re-acknowledging must not be able to refresh the clock — else
// repeating `--ack` at approve time would erase yesterday's genuine reading and
// make every approval look reflexive. Accepted cost: a second, deliberate
// re-read is invisible here. Nothing in F5 asks how many times somebody looked.
//
// # Debt: what a mark still does not prove
//
// That a label was typed. Nothing here observes a person reading anything — the
// argument is made in full in f2-design.md, in "required_reading — a trava, e o
// que ela realmente prova", and F5 does not change a word of it. Persistence
// makes the friction survivable across sessions, which is a usability fix and
// not an epistemic one; if anything it lowers the friction, since a label now
// only has to be typed once. What is bought in exchange is the timestamp: with
// reading and deciding separable, the gap between them becomes measurable, and a
// population of zero-second gaps is the evidence that a gate is theatre. The
// mark answers "when", never "did they understand".
//
// # Debt: parity with F7
//
// The UI will mark reading by scrolling an evidence pane, not by typing a label,
// and that is a different act with a cheaper failure mode. Both have to land in
// this same field, or "closing the tab does not reset it" ends up true of one
// surface only. What is fixed here is the storage; whether a scroll counts as a
// read is F7's call to defend, made knowing this record cannot tell the two
// apart afterwards.
type ReadMark struct {
	Label string    `json:"label"`
	At    time.Time `json:"at"`
}

// UnknownLabelError is `gate ack` refusing a label this gate does not carry.
//
// Refusing rather than storing it is the whole value of the verb: silently
// accepting a typo would let somebody believe they were covered and only find
// out at approve time, which is exactly when a person is least willing to go
// back and read. Typed, not prose, for the same reason UnreadError is: F7 has to
// render the same list.
type UnknownLabelError struct {
	RunID   string
	StepID  string
	Unknown []string
	Known   []string
}

func (e *UnknownLabelError) Error() string {
	known := "it carries no evidence at all"
	if len(e.Known) > 0 {
		known = "it carries: " + strings.Join(e.Known, ", ")
	}
	return fmt.Sprintf("gate %s/%s: no evidence labelled %s — %s "+
		"(run `corvex gate show %s --step %s`)",
		e.RunID, e.StepID, strings.Join(quoteAll(e.Unknown), ", "), known, e.RunID, e.StepID)
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// EvidenceLabels lists every label on the gate, required or not.
func EvidenceLabels(items []types.Evidence) []string {
	out := make([]string, 0, len(items))
	for _, e := range items {
		out = append(out, e.Label)
	}
	return out
}

// ReadLabels lists the labels this gate already has a mark for.
func (p Pending) ReadLabels() []string {
	out := make([]string, 0, len(p.Reads))
	for _, r := range p.Reads {
		out = append(out, r.Label)
	}
	return out
}

// ReadOf returns the mark for a label, matched the way the lock matches. The
// mark carries the gate's own spelling, so a caller can echo the canonical label
// back instead of the user's casing of it.
func (p Pending) ReadOf(label string) (ReadMark, bool) {
	for _, r := range p.Reads {
		if normalizeLabel(r.Label) == normalizeLabel(label) {
			return r, true
		}
	}
	return ReadMark{}, false
}

// MissingReading is the approval lock, asking about a gate rather than about two
// lists: required reading that has neither a persisted mark nor an inline --ack.
//
// The lock does not loosen by accepting marks made earlier — it still demands
// that every required item was acknowledged. What changes is only *when* the
// acknowledgement may have happened, which is the difference between a tool that
// lets somebody read on Monday and decide on Tuesday and one that forces both
// into the same keystroke.
func (p Pending) MissingReading(inline []string) []string {
	acked := make([]string, 0, len(p.Reads)+len(inline))
	acked = append(acked, p.ReadLabels()...)
	acked = append(acked, inline...)
	return MissingAcks(p.Evidence, acked)
}

// canonicalLabels maps what a person typed onto the gate's own spelling, so the
// stored mark always matches `gate show`. Matching is the lock's matching
// (case-insensitive, trimmed); storing the canonical form is what keeps
// "migration" and "Migration" from becoming two marks for one piece of evidence.
func canonicalLabels(items []types.Evidence, typed []string) (known, unknown []string) {
	byNorm := make(map[string]string, len(items))
	for _, e := range items {
		byNorm[normalizeLabel(e.Label)] = e.Label
	}
	seen := make(map[string]bool, len(typed))
	for _, t := range typed {
		canon, ok := byNorm[normalizeLabel(t)]
		if !ok {
			unknown = append(unknown, t)
			continue
		}
		if seen[canon] {
			continue
		}
		seen[canon] = true
		known = append(known, canon)
	}
	return known, unknown
}

// applyReads adds a mark per label that has none, and reports which were new.
// Order is chronological, because that is the order a reader reconstructs the
// session in: mark, mark, decide.
func applyReads(existing []ReadMark, labels []string, now time.Time) (out []ReadMark, added []string) {
	out = existing
	have := make(map[string]bool, len(existing))
	for _, r := range existing {
		have[normalizeLabel(r.Label)] = true
	}
	for _, label := range labels {
		if have[normalizeLabel(label)] {
			continue
		}
		have[normalizeLabel(label)] = true
		out = append(out, ReadMark{Label: label, At: now})
		added = append(added, label)
	}
	return out, added
}

// MarkRead records that evidence was read, deciding nothing.
//
// # Why this is not simply `approve --ack` typed earlier
//
// It writes to the same file the run is polling, but never touches Decision, so
// the run stays parked. That separation is the feature: reading and deciding
// become two acts with a measurable gap between them, instead of one keystroke
// that asserts both at once.
//
// Liveness is deliberately NOT checked, unlike a decision. Whether the run is
// still up is a fact about the machine; that a person read the migration is a
// fact about the person, and it stays true after the process dies. Refusing the
// mark would only push somebody to read again later under time pressure — and
// `approve` still refuses on a dead run, so nothing slips through.
func MarkRead(repo, runID, stepID string, labels []string, now time.Time) (Pending, []string, error) {
	path, err := Path(repo, runID, stepID)
	if err != nil {
		return Pending{}, nil, err
	}
	p, err := readFile(path)
	if err != nil {
		return Pending{}, nil, err
	}
	if p.Decided() {
		return p, nil, fmt.Errorf("gate %s/%s was already %s, so reading it now changes nothing",
			runID, stepID, p.Decision.Verdict)
	}
	known, unknown := canonicalLabels(p.Evidence, labels)
	if len(unknown) > 0 {
		return p, nil, &UnknownLabelError{
			RunID: runID, StepID: stepID,
			Unknown: unknown, Known: EvidenceLabels(p.Evidence),
		}
	}
	reads, added := applyReads(p.Reads, known, now)
	if len(added) == 0 {
		// Nothing changed on disk, so nothing is written: a repeated `ack` must
		// not rewrite the file the run is polling, and must not look like an
		// event in a directory whose mtimes F5 may end up reading.
		return p, nil, nil
	}
	p.Reads = reads
	if err := writeGate(path, p); err != nil {
		return p, nil, err
	}
	return p, added, nil
}
