// Package gate is the on-disk contract between a run that is blocked on a gate
// and whatever process decides it.
//
// # Why this is a file and not a channel
//
// A human gate can hold a run for days, across sessions and across machines
// being closed. The process that approves is never the process that asked: it is
// a second `corvex gate approve`, or (F7) an HTTP handler. So the contract that
// matters is the format on disk, exactly as it was for run identity in F1 — a
// Go type shared between two goroutines proves nothing about two processes.
//
// # Why the evidence lives here and not in the ledger
//
// activity.jsonl is committed: corvex's own auto_commit puts it in the user's
// git history. Evidence content is the opposite of committable — a test_output
// carries an absolute path in its first stack frame, a diff carries whatever is
// in the working tree, a sql carries production schema names. So the ledger gets
// metadata only (nature and the user's own label) and the content stays here,
// under `.corvex/runs/`, which is gitignored with `*` on first write.
//
// # What is deliberately absent
//
// There is no "decided by" field. corvex is single-user with no RBAC (and the
// roadmap keeps it that way for now), so a username would buy nothing a reader
// can act on — while being exactly the kind of machine-describing value that
// leaked out of the ledger in F1. If multi-user arrives, the field arrives with
// the access control that gives it meaning.
package gate

import (
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/giovannialves/corvex/internal/run"
	"github.com/giovannialves/corvex/internal/task"
	"github.com/giovannialves/corvex/internal/types"
)

// Verdict is what a decider said.
type Verdict string

const (
	// Approved lets the guarded work proceed.
	Approved Verdict = "approved"
	// Rejected fails the step, which cascades to its dependents exactly like
	// any other task-level failure — rejecting is not a special case.
	Rejected Verdict = "rejected"
	// Expired is what an unattended gate becomes when the recipe declared
	// expires_after. It is a rejection with a different reason, never an
	// approval: a gate that approves itself after N hours is a rubber stamp on
	// a timer.
	Expired Verdict = "expired"
)

// IsValid reports whether the verdict is one of the three.
func (v Verdict) IsValid() bool {
	switch v {
	case Approved, Rejected, Expired:
		return true
	}
	return false
}

// Decision is the answer written by whoever decided.
type Decision struct {
	Verdict   Verdict   `json:"verdict"`
	DecidedAt time.Time `json:"decided_at"`
	// Acked lists the required-reading evidence labels the decider named.
	// Recorded rather than merely checked, so F5 can ask later whether the
	// acknowledgement is real or reflexive.
	Acked []string `json:"acked,omitempty"`
	// Reason is free text from a rejection, or the expiry note.
	Reason string `json:"reason,omitempty"`
	// Answer is what a person wrote back to a `question` gate — the axis
	// inverted, the run asking and the human replying in words.
	//
	// # Why a field here and not a sibling type
	//
	// A question could have been a second file format with its own store, its
	// own list and its own inbox field. The test that decided it is what such a
	// type would cost the two readers that already exist: gate.List (and through
	// it ops.LoadGateAudit) would need a second walk of the same directory, and
	// ops.Inbox would need a third slice that every caller has to remember to
	// render. Both would then have to be taught which of the two files a given
	// run id belongs to. As a field, the audit sees the row through the nature
	// it already carries and the inbox lists it without knowing it exists —
	// neither reader gains a special case, which was the criterion.
	//
	// # Why the verdict is not a fourth value
	//
	// An answered question is `approved`: the run may continue. Minting
	// `answered` as a fourth Verdict would break the invariant the audit is
	// built on (Decided == Approved+Rejected+Expired, guarded by
	// TestGateVerdict_ThreeBucketsAndNoMore) and would make every consumer of
	// verdictStatus decide again what "continue" means. Refusing to answer is a
	// rejection, and a question nobody answered expires — both already mean the
	// step fails. So the verdict axis is untouched and the words ride beside it.
	//
	// Optional on disk, like every field added after the format shipped: gates
	// written before this existed have no key, and unmarshalling gives the zero
	// value. The refusal of an empty answer lives in Decide, where the writer
	// is, not in the reader — otherwise old files would stop being readable.
	Answer string `json:"answer,omitempty"`
}

// Pending is one gate as it exists on disk: what is being asked, what the person
// needs to read to answer it, and — once answered — the decision.
type Pending struct {
	RunID   string `json:"run_id"`
	StepID  string `json:"step_id"`
	Repo    string `json:"repo"`
	Project string `json:"project,omitempty"`
	Recipe  string `json:"recipe,omitempty"`

	Nature types.GateNature `json:"nature"`
	Label  string           `json:"label,omitempty"`
	Prompt string           `json:"prompt,omitempty"`
	// Title is the step's own title, so a listing reads like the pipeline
	// rather than like a set of ids.
	Title string `json:"title,omitempty"`

	Evidence []types.Evidence `json:"evidence,omitempty"`
	// Reads is the persistent reading state F5 asked for: closing the terminal
	// does not reset it, because it was never in the terminal. It lives in the
	// gate file rather than anywhere near the ledger for the reason stated at
	// the top of this file — the ledger is committed, and who-read-what is
	// still evidence-shaped state about a working session.
	Reads []ReadMark `json:"reads,omitempty"`

	OpenedAt  time.Time  `json:"opened_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	Decision *Decision `json:"decision,omitempty"`
}

// Decided reports whether an answer has been written.
func (p Pending) Decided() bool { return p.Decision != nil }

// Asks reports whether this gate is the run asking a person for words rather
// than for a verdict.
func (p Pending) Asks() bool { return p.Nature == types.GateQuestion }

// Describe is the one-line label this gate contributes to the ledger: nature
// plus the user's own words, and nothing that describes the machine.
func (p Pending) Describe() string {
	return types.Gate{Nature: p.Nature, Label: p.Label, Prompt: p.Prompt}.Describe()
}

// Expired reports whether an undecided gate has passed its deadline.
func (p Pending) Expired(now time.Time) bool {
	return !p.Decided() && p.ExpiresAt != nil && now.After(*p.ExpiresAt)
}

// Dir is where gate state lives: `<repo>/.corvex/runs/gates`.
//
// Under RecordsDir rather than beside it, so the `*` .gitignore that F1 writes
// for run records covers it too — the property evidence needs most is the one
// that directory already has.
func Dir(repo string) string {
	return filepath.Join(run.RecordsDir(repo), "gates")
}

// Path is the file holding one step's gate state.
//
// The step id is percent-escaped because fan-out mints ids containing `/`
// (`S06/003/apply`), which would otherwise turn into directories. Escaping
// rather than substituting keeps it injective: two different ids can never
// collide on one filename.
func Path(repo, runID, stepID string) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("gate: empty repo path")
	}
	if !run.ValidID(runID) {
		return "", fmt.Errorf("gate: invalid run id %q", runID)
	}
	if !task.ValidTaskID(stepID) {
		return "", fmt.Errorf("gate: invalid step id %q", stepID)
	}
	return filepath.Join(Dir(repo), runID+"-"+url.PathEscape(stepID)+".json"), nil
}
